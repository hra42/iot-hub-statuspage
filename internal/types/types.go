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
	CPUPercent    float64
	MemoryPercent float64
	DiskPercent   float64
	NetworkIn     float64
	NetworkOut    float64
	Uptime        time.Duration
}