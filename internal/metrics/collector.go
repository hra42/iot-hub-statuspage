package metrics

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	
	"github.com/hra42/iot-hub-statuspage/internal/haproxy"
	"github.com/hra42/iot-hub-statuspage/internal/storage"
	"github.com/hra42/iot-hub-statuspage/internal/types"
)

type Collector struct {
	db           *storage.DB
	haproxy      *haproxy.Client
	dockerClient *client.Client
	mu           sync.RWMutex
	current      types.SystemMetrics
	dockerStatus []types.ServiceStatus
	lastNetworkIn  float64
	lastNetworkOut float64
	lastCollectTime time.Time
}

func NewCollector(db *storage.DB, haproxy *haproxy.Client) *Collector {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Printf("Warning: Failed to create Docker client: %v. Docker monitoring disabled.", err)
		dockerClient = nil
	}

	return &Collector{
		db:           db,
		haproxy:      haproxy,
		dockerClient: dockerClient,
	}
}

func (c *Collector) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Collect initial metrics
	c.collect()

	for {
		select {
		case <-ticker.C:
			c.collect()
		case <-ctx.Done():
			return
		}
	}
}

func (c *Collector) collect() {
	// Collect system metrics
	metrics := types.SystemMetrics{}

	// Prepare slices for bulk insert
	var systemMetrics []storage.SystemMetric
	var serviceStatuses []storage.ServiceStatus

	// CPU usage
	cpuPercent, err := cpu.Percent(time.Second, false)
	if err == nil && len(cpuPercent) > 0 {
		metrics.CPUPercent = cpuPercent[0]
		systemMetrics = append(systemMetrics, storage.SystemMetric{
			MetricType: "cpu",
			Value:      metrics.CPUPercent,
		})
	}

	// Memory usage
	vmStat, err := mem.VirtualMemory()
	if err == nil {
		metrics.MemoryPercent = vmStat.UsedPercent
		metrics.MemoryUsed = vmStat.Used
		metrics.MemoryTotal = vmStat.Total
		systemMetrics = append(systemMetrics, storage.SystemMetric{
			MetricType: "memory",
			Value:      metrics.MemoryPercent,
		})
		systemMetrics = append(systemMetrics, storage.SystemMetric{
			MetricType: "memory_used",
			Value:      float64(vmStat.Used),
		})
		systemMetrics = append(systemMetrics, storage.SystemMetric{
			MetricType: "memory_total",
			Value:      float64(vmStat.Total),
		})
	} else {
		log.Printf("Error getting memory stats: %v", err)
	}

	// Disk usage
	diskStat, err := disk.Usage("/")
	if err == nil {
		metrics.DiskPercent = diskStat.UsedPercent
		metrics.DiskUsed = diskStat.Used
		metrics.DiskTotal = diskStat.Total
		systemMetrics = append(systemMetrics, storage.SystemMetric{
			MetricType: "disk",
			Value:      metrics.DiskPercent,
		})
		systemMetrics = append(systemMetrics, storage.SystemMetric{
			MetricType: "disk_used",
			Value:      float64(diskStat.Used),
		})
		systemMetrics = append(systemMetrics, storage.SystemMetric{
			MetricType: "disk_total",
			Value:      float64(diskStat.Total),
		})
	}

	// Network stats - calculate rate (bytes per second)
	netStats, err := net.IOCounters(false)
	if err == nil && len(netStats) > 0 {
		currentNetworkIn := float64(netStats[0].BytesRecv)
		currentNetworkOut := float64(netStats[0].BytesSent)
		
		// Calculate rates if we have previous values
		if c.lastCollectTime.IsZero() {
			// First collection, just store the values
			metrics.NetworkIn = 0
			metrics.NetworkOut = 0
		} else {
			timeDiff := time.Since(c.lastCollectTime).Seconds()
			if timeDiff > 0 {
				metrics.NetworkIn = (currentNetworkIn - c.lastNetworkIn) / timeDiff
				metrics.NetworkOut = (currentNetworkOut - c.lastNetworkOut) / timeDiff
				
				// Ensure non-negative values (in case of counter reset)
				if metrics.NetworkIn < 0 {
					metrics.NetworkIn = 0
				}
				if metrics.NetworkOut < 0 {
					metrics.NetworkOut = 0
				}
			}
		}
		
		// Store current values for next calculation
		c.lastNetworkIn = currentNetworkIn
		c.lastNetworkOut = currentNetworkOut
		
		systemMetrics = append(systemMetrics, storage.SystemMetric{
			MetricType: "network_in_rate",
			Value:      metrics.NetworkIn,
		})
		systemMetrics = append(systemMetrics, storage.SystemMetric{
			MetricType: "network_out_rate",
			Value:      metrics.NetworkOut,
		})
	}

	// System uptime
	uptimeInfo, err := host.Uptime()
	if err == nil {
		metrics.Uptime = time.Duration(uptimeInfo) * time.Second
	}

	// Database size and connection check
	dbSize, err := c.db.GetDatabaseSize()
	if err == nil {
		metrics.DatabaseSize = dbSize
		metrics.DatabaseConnected = true
		systemMetrics = append(systemMetrics, storage.SystemMetric{
			MetricType: "database_size",
			Value:      float64(dbSize),
		})
	} else {
		log.Printf("Error getting database size: %v", err)
		metrics.DatabaseConnected = false
		// Try a simple ping to check if it's just a size query issue
		if pingErr := c.db.Ping(); pingErr == nil {
			metrics.DatabaseConnected = true
		}
	}

	// Collect HAProxy stats
	if stats, err := c.haproxy.GetStats(); err == nil {
		metrics.HAProxyConnected = true
		for _, backend := range stats.Backends {
			status := "UP"
			if !backend.Active {
				status = "DOWN"
			}
			serviceStatuses = append(serviceStatuses, storage.ServiceStatus{
				Service: fmt.Sprintf("haproxy_%s", backend.Name),
				Status:  status,
				Details: "",
			})
		}
	} else {
		metrics.HAProxyConnected = false
		log.Printf("Error getting HAProxy stats: %v", err)
	}

	// Collect Docker container stats
	if c.dockerClient != nil {
		c.collectDockerStats(&serviceStatuses)
		metrics.DockerConnected = true
	} else {
		metrics.DockerConnected = false
	}

	// Check host connectivity
	metrics.Pi5Connected = pingHost("192.168.2.136")
	metrics.Pi52Connected = pingHost("192.168.2.135")

	// Update current metrics and last collect time
	c.mu.Lock()
	c.current = metrics
	c.lastCollectTime = time.Now()
	c.mu.Unlock()

	// Perform bulk insert in a single transaction
	if err := c.db.BulkInsert(systemMetrics, serviceStatuses); err != nil {
		log.Printf("Failed to perform bulk insert: %v", err)
	}
}

func (c *Collector) collectDockerStats(serviceStatuses *[]storage.ServiceStatus) {
	containers, err := c.dockerClient.ContainerList(context.Background(), dockercontainer.ListOptions{All: true})
	if err != nil {
		log.Printf("Failed to list containers: %v", err)
		// Mark Docker as disconnected if we can't list containers
		c.mu.Lock()
		c.current.DockerConnected = false
		c.mu.Unlock()
		return
	}

	dockerStatus := make([]types.ServiceStatus, 0)
	
	for _, container := range containers {
		name := container.Names[0]
		if len(name) > 0 && name[0] == '/' {
			name = name[1:] // Remove leading slash
		}

		status := types.ServiceStatus{
			Name:    name,
			Status:  container.State,
			Healthy: container.State == "running",
		}

		// Get more detailed status
		inspect, err := c.dockerClient.ContainerInspect(context.Background(), container.ID)
		if err == nil {
			if inspect.State.Health != nil {
				status.Healthy = inspect.State.Health.Status == "healthy"
				// Only set details if there's an actual issue
				if inspect.State.Health.Status != "healthy" {
					status.Details = inspect.State.Health.Status
				}
			}
			
			if inspect.State.StartedAt != "" {
				startTime, _ := time.Parse(time.RFC3339, inspect.State.StartedAt)
				uptime := time.Since(startTime)
				status.Uptime = formatDuration(uptime)
			}
		}

		dockerStatus = append(dockerStatus, status)
		
		// Add to bulk insert
		healthStatus := "UP"
		if !status.Healthy {
			healthStatus = "DOWN"
		}
		*serviceStatuses = append(*serviceStatuses, storage.ServiceStatus{
			Service: fmt.Sprintf("docker_%s", name),
			Status:  healthStatus,
			Details: status.Details,
		})
	}

	c.mu.Lock()
	c.dockerStatus = dockerStatus
	c.mu.Unlock()
}

func (c *Collector) GetCurrentMetrics() types.SystemMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

func (c *Collector) GetDockerStatus() []types.ServiceStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dockerStatus
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	} else if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func pingHost(host string) bool {
	// Use ping command with timeout
	cmd := exec.Command("ping", "-c", "1", "-W", "2", host)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Ping to %s failed: %v, output: %s", host, err, string(output))
		return false
	}
	return true
}