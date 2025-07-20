package metrics

import (
	"context"
	"fmt"
	"log"
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

	// CPU usage
	cpuPercent, err := cpu.Percent(time.Second, false)
	if err == nil && len(cpuPercent) > 0 {
		metrics.CPUPercent = cpuPercent[0]
		c.db.InsertSystemMetric("cpu", metrics.CPUPercent)
	}

	// Memory usage
	vmStat, err := mem.VirtualMemory()
	if err == nil {
		metrics.MemoryPercent = vmStat.UsedPercent
		c.db.InsertSystemMetric("memory", metrics.MemoryPercent)
		c.db.InsertSystemMetric("memory_used", float64(vmStat.Used))
		c.db.InsertSystemMetric("memory_total", float64(vmStat.Total))
		log.Printf("Memory: %.2f%% (Used: %d, Total: %d)", vmStat.UsedPercent, vmStat.Used, vmStat.Total)
	} else {
		log.Printf("Error getting memory stats: %v", err)
	}

	// Disk usage
	diskStat, err := disk.Usage("/")
	if err == nil {
		metrics.DiskPercent = diskStat.UsedPercent
		c.db.InsertSystemMetric("disk", metrics.DiskPercent)
		c.db.InsertSystemMetric("disk_used", float64(diskStat.Used))
		c.db.InsertSystemMetric("disk_total", float64(diskStat.Total))
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
		
		c.db.InsertSystemMetric("network_in_rate", metrics.NetworkIn)
		c.db.InsertSystemMetric("network_out_rate", metrics.NetworkOut)
	}

	// System uptime
	uptimeInfo, err := host.Uptime()
	if err == nil {
		metrics.Uptime = time.Duration(uptimeInfo) * time.Second
	}

	// Update current metrics and last collect time
	c.mu.Lock()
	c.current = metrics
	c.lastCollectTime = time.Now()
	c.mu.Unlock()

	// Collect HAProxy stats
	if stats, err := c.haproxy.GetStats(); err == nil {
		for _, backend := range stats.Backends {
			status := "UP"
			if !backend.Active {
				status = "DOWN"
			}
			c.db.InsertServiceStatus(fmt.Sprintf("haproxy_%s", backend.Name), status, "")
		}
	}

	// Collect Docker container stats
	if c.dockerClient != nil {
		c.collectDockerStats()
	}
}

func (c *Collector) collectDockerStats() {
	containers, err := c.dockerClient.ContainerList(context.Background(), dockercontainer.ListOptions{All: true})
	if err != nil {
		log.Printf("Failed to list containers: %v", err)
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
				status.Details = inspect.State.Health.Status
				status.Healthy = inspect.State.Health.Status == "healthy"
			}
			
			if inspect.State.StartedAt != "" {
				startTime, _ := time.Parse(time.RFC3339, inspect.State.StartedAt)
				uptime := time.Since(startTime)
				status.Uptime = formatDuration(uptime)
			}
		}

		dockerStatus = append(dockerStatus, status)
		
		// Store in database
		healthStatus := "UP"
		if !status.Healthy {
			healthStatus = "DOWN"
		}
		c.db.InsertServiceStatus(fmt.Sprintf("docker_%s", name), healthStatus, status.Details)
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