package types

import "time"

type ServiceStatus struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Healthy     bool      `json:"healthy"`
	LastChange  string    `json:"last_change"`
	Uptime      string    `json:"uptime"`
	Details     string    `json:"details,omitempty"`
}

type SystemMetrics struct {
	CPUPercent       float64
	MemoryPercent    float64
	MemoryUsed       uint64
	MemoryTotal      uint64
	DiskPercent      float64
	DiskUsed         uint64
	DiskTotal        uint64
	NetworkIn        float64
	NetworkOut       float64
	Uptime           time.Duration
	DatabaseSize     int64
	DatabaseConnected bool
	HAProxyConnected bool
	DockerConnected  bool
	Pi5Connected     bool
	Pi52Connected    bool
}