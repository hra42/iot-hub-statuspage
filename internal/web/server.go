package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hra42/iot-hub-statuspage/internal/haproxy"
	"github.com/hra42/iot-hub-statuspage/internal/metrics"
	"github.com/hra42/iot-hub-statuspage/internal/storage"
	"github.com/hra42/iot-hub-statuspage/internal/types"
	"github.com/hra42/iot-hub-statuspage/internal/web/templates"
)

type Server struct {
	db         *storage.DB
	haproxy    *haproxy.Client
	collector  *metrics.Collector
	router     *gin.Engine
	sseClients map[chan Event]bool
	sseMutex   sync.RWMutex
}

type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type StatusResponse struct {
	Services    []types.ServiceStatus `json:"services"`
	System      SystemStatus          `json:"system"`
	LastUpdated time.Time            `json:"last_updated"`
}

type SystemStatus struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	DiskPercent   float64 `json:"disk_percent"`
	NetworkIn     float64 `json:"network_in"`
	NetworkOut    float64 `json:"network_out"`
	Uptime        string  `json:"uptime"`
	DatabaseSize  int64   `json:"database_size"`
}

func NewServer(db *storage.DB, haproxy *haproxy.Client, collector *metrics.Collector) *Server {
	s := &Server{
		db:         db,
		haproxy:    haproxy,
		collector:  collector,
		router:     gin.New(),
		sseClients: make(map[chan Event]bool),
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	// Add recovery and logger middleware
	s.router.Use(gin.Recovery())
	s.router.Use(gin.Logger())

	// Static files
	s.router.Static("/static", "./static")

	// Routes
	s.router.GET("/", s.handleDashboard)
	s.router.GET("/api/status", s.handleAPIStatus)
	s.router.GET("/api/metrics", s.handleAPIMetrics)
	s.router.GET("/events", s.handleSSE)
	s.router.GET("/health", s.handleHealth)

	// Start SSE broadcaster
	go s.broadcastUpdates()
}

func (s *Server) Router() *gin.Engine {
	return s.router
}

func (s *Server) handleDashboard(c *gin.Context) {
	status, err := s.getCurrentStatus()
	if err != nil {
		c.String(http.StatusInternalServerError, "Error getting status")
		return
	}

	dashboardData := templates.DashboardData{
		Services: status.Services,
		System: templates.SystemStatus{
			CPUPercent:    status.System.CPUPercent,
			MemoryPercent: status.System.MemoryPercent,
			DiskPercent:   status.System.DiskPercent,
			NetworkIn:     status.System.NetworkIn,
			NetworkOut:    status.System.NetworkOut,
			Uptime:        status.System.Uptime,
			DatabaseSize:  status.System.DatabaseSize,
		},
		LastUpdated: status.LastUpdated,
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	
	// Use templ to render the dashboard
	component := templates.Dashboard(dashboardData)
	if err := component.Render(c.Request.Context(), c.Writer); err != nil {
		log.Printf("Template render error: %v", err)
		c.String(http.StatusInternalServerError, "Template render error")
		return
	}
}

func (s *Server) handleAPIStatus(c *gin.Context) {
	status, err := s.getCurrentStatus()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, status)
}

func (s *Server) handleAPIMetrics(c *gin.Context) {
	period := c.DefaultQuery("period", "24h")
	duration, err := time.ParseDuration(period)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid period"})
		return
	}

	metrics := make(map[string]interface{})
	
	// Get CPU metrics
	cpuMetrics, _ := s.db.GetSystemMetricsHistory("cpu", duration)
	metrics["cpu"] = cpuMetrics

	// Get memory metrics
	memMetrics, _ := s.db.GetSystemMetricsHistory("memory", duration)
	metrics["memory"] = memMetrics

	// Get service status history
	serviceHistory := make(map[string]interface{})
	services, _ := s.db.GetLatestServiceStatuses()
	for service := range services {
		history, _ := s.db.GetServiceStatusHistory(service, duration)
		serviceHistory[service] = history
	}
	metrics["services"] = serviceHistory

	c.JSON(http.StatusOK, metrics)
}

func (s *Server) handleSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("X-Accel-Buffering", "no")

	clientChan := make(chan Event)
	
	// Add client with mutex lock
	s.sseMutex.Lock()
	s.sseClients[clientChan] = true
	s.sseMutex.Unlock()
	
	log.Printf("SSE client connected, total clients: %d", len(s.sseClients))
	
	// Send initial update immediately
	go func() {
		status, err := s.getCurrentStatus()
		if err == nil {
			signals := map[string]interface{}{
				"cpuPercent":    fmt.Sprintf("%.1f", status.System.CPUPercent),
				"memoryPercent": fmt.Sprintf("%.1f", status.System.MemoryPercent),
				"diskPercent":   fmt.Sprintf("%.1f", status.System.DiskPercent),
				"networkIn":     formatBytes(status.System.NetworkIn),
				"networkOut":    formatBytes(status.System.NetworkOut),
				"uptime":        status.System.Uptime,
				"databaseSize":  formatBytes(float64(status.System.DatabaseSize)),
				"lastUpdated":   time.Now().Format("2006-01-02 15:04:05"),
			}
			
			// Add service signals
			for i, service := range status.Services {
				signals[fmt.Sprintf("service%d_status", i)] = service.Status
				signals[fmt.Sprintf("service%d_healthy", i)] = service.Healthy
				signals[fmt.Sprintf("service%d_details", i)] = service.Details
				signals[fmt.Sprintf("service%d_uptime", i)] = service.Uptime
			}
			
			event := Event{
				Type: "signals",
				Data: signals,
			}
			
			select {
			case clientChan <- event:
				log.Printf("Sent initial update to new client")
			default:
				log.Printf("Failed to send initial update to new client")
			}
		}
	}()

	defer func() {
		// Remove client with mutex lock
		s.sseMutex.Lock()
		delete(s.sseClients, clientChan)
		s.sseMutex.Unlock()
		close(clientChan)
	}()

	c.Stream(func(w io.Writer) bool {
		select {
		case event := <-clientChan:
			// Send Datastar-compatible SSE event
			if signals, ok := event.Data.(map[string]interface{}); ok {
				// Send as datastar patch-signals event
				// Datastar expects the signals directly, not wrapped
				data, err := json.Marshal(signals)
				if err != nil {
					log.Printf("Error marshaling signals: %v", err)
					return true
				}
				
				// Send in exact format Datastar expects
				fmt.Fprintf(w, "event: datastar-patch-signals\n")
				// Split data across multiple lines with "data: signals " prefix
				lines := strings.Split(string(data), "\n")
				for i, line := range lines {
					if i == 0 {
						fmt.Fprintf(w, "data: signals %s\n", line)
					} else if line != "" {
						fmt.Fprintf(w, "data: signals %s\n", line)
					}
				}
				fmt.Fprintf(w, "\n")
				
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

func (s *Server) handleHealth(c *gin.Context) {
	healthy := true
	details := make(map[string]string)

	// Check database
	if err := s.db.Ping(); err != nil {
		healthy = false
		details["database"] = "unhealthy"
	} else {
		details["database"] = "healthy"
	}

	// Check HAProxy connection
	if s.haproxy.IsHealthy() {
		details["haproxy"] = "healthy"
	} else {
		healthy = false
		details["haproxy"] = "unhealthy"
	}

	if healthy {
		c.JSON(http.StatusOK, gin.H{"status": "healthy", "details": details})
	} else {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "details": details})
	}
}

func (s *Server) getCurrentStatus() (*StatusResponse, error) {
	// Get HAProxy stats
	haproxyStats, err := s.haproxy.GetStats()
	if err != nil {
		return nil, err
	}

	// Get system metrics
	systemMetrics := s.collector.GetCurrentMetrics()

	// Build service status list
	var services []types.ServiceStatus
	for _, backend := range haproxyStats.Backends {
		status := types.ServiceStatus{
			Name:    backend.Name,
			Status:  backend.Status,
			Healthy: backend.Active,
		}
		
		// Calculate uptime/downtime
		if backend.LastChange > 0 {
			duration := time.Duration(backend.LastChange) * time.Second
			status.LastChange = formatDuration(duration)
		}
		
		if backend.Active {
			status.Uptime = status.LastChange
		} else {
			status.Details = fmt.Sprintf("Down for %s", status.LastChange)
		}

		services = append(services, status)
	}

	// Get Docker container status
	dockerServices := s.collector.GetDockerStatus()
	services = append(services, dockerServices...)

	systemStatus := SystemStatus{
		CPUPercent:    systemMetrics.CPUPercent,
		MemoryPercent: systemMetrics.MemoryPercent,
		DiskPercent:   systemMetrics.DiskPercent,
		NetworkIn:     systemMetrics.NetworkIn,
		NetworkOut:    systemMetrics.NetworkOut,
		Uptime:        formatDuration(systemMetrics.Uptime),
		DatabaseSize:  systemMetrics.DatabaseSize,
	}
	
	return &StatusResponse{
		Services: services,
		System: systemStatus,
		LastUpdated: time.Now(),
	}, nil
}

func (s *Server) broadcastUpdates() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		status, err := s.getCurrentStatus()
		if err != nil {
			continue
		}

		// Create signals update for Datastar
		signals := map[string]interface{}{
			"cpuPercent":    fmt.Sprintf("%.1f", status.System.CPUPercent),
			"memoryPercent": fmt.Sprintf("%.1f", status.System.MemoryPercent),
			"diskPercent":   fmt.Sprintf("%.1f", status.System.DiskPercent),
			"networkIn":     formatBytes(status.System.NetworkIn),
			"networkOut":    formatBytes(status.System.NetworkOut),
			"uptime":        status.System.Uptime,
			"databaseSize":  formatBytes(float64(status.System.DatabaseSize)),
			"lastUpdated":   time.Now().Format("2006-01-02 15:04:05"),
		}
		
		// Add service signals
		for i, service := range status.Services {
			signals[fmt.Sprintf("service%d_status", i)] = service.Status
			signals[fmt.Sprintf("service%d_healthy", i)] = service.Healthy
			signals[fmt.Sprintf("service%d_details", i)] = service.Details
			signals[fmt.Sprintf("service%d_uptime", i)] = service.Uptime
		}

		event := Event{
			Type: "signals",
			Data: signals,
		}

		// Send event to all clients with read lock
		s.sseMutex.RLock()
		clients := make([]chan Event, 0, len(s.sseClients))
		for client := range s.sseClients {
			clients = append(clients, client)
		}
		clientCount := len(s.sseClients)
		s.sseMutex.RUnlock()
		
		// Send to clients outside of lock
		for _, client := range clients {
			select {
			case client <- event:
				// Successfully sent
			default:
				// Client buffer full, skip
			}
		}
		
		if clientCount > 0 {
			log.Printf("Broadcasted updates to %d clients", clientCount)
		}
	}
}

func formatBytes(bytes float64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%.0f B", bytes)
	}
	
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	
	return fmt.Sprintf("%.1f %cB", bytes/float64(div), "KMGTPE"[exp])
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

