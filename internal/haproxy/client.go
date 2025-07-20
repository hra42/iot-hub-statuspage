package haproxy

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	socketPath string
}

type Stats struct {
	Backends []Backend
}

type Backend struct {
	Name         string
	Status       string
	Active       bool
	CheckStatus  string
	CheckCode    int
	CheckDuration int
	LastChange   int
	Downtime     int
	ConnRate     int
	ConnRateMax  int
	SessionRate  int
	SessionCur   int
	SessionMax   int
	BytesIn      int64
	BytesOut     int64
}

func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
	}
}

func (c *Client) GetStats() (*Stats, error) {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to HAProxy socket: %w", err)
	}
	defer conn.Close()

	// Set timeout
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send command
	_, err = conn.Write([]byte("show stat\n"))
	if err != nil {
		return nil, fmt.Errorf("failed to send command: %w", err)
	}

	// Read response
	reader := csv.NewReader(bufio.NewReader(conn))
	reader.Comment = '#'
	
	// Read header
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	// Create column index map
	colIndex := make(map[string]int)
	for i, col := range header {
		colIndex[strings.TrimSpace(col)] = i
	}

	stats := &Stats{
		Backends: make([]Backend, 0),
	}

	// Read data rows
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read row: %w", err)
		}

		// Only process BACKEND type entries
		if record[colIndex["svname"]] == "BACKEND" {
			backend := Backend{
				Name:        record[colIndex["pxname"]],
				Status:      record[colIndex["status"]],
				Active:      record[colIndex["status"]] == "UP",
			}

			// Parse numeric fields
			if val := record[colIndex["check_status"]]; val != "" {
				backend.CheckStatus = val
			}
			if val := record[colIndex["check_code"]]; val != "" {
				backend.CheckCode, _ = strconv.Atoi(val)
			}
			if val := record[colIndex["check_duration"]]; val != "" {
				backend.CheckDuration, _ = strconv.Atoi(val)
			}
			if val := record[colIndex["lastchg"]]; val != "" {
				backend.LastChange, _ = strconv.Atoi(val)
			}
			if val := record[colIndex["downtime"]]; val != "" {
				backend.Downtime, _ = strconv.Atoi(val)
			}
			if val := record[colIndex["rate"]]; val != "" {
				backend.ConnRate, _ = strconv.Atoi(val)
			}
			if val := record[colIndex["rate_max"]]; val != "" {
				backend.ConnRateMax, _ = strconv.Atoi(val)
			}
			if val := record[colIndex["stot"]]; val != "" {
				backend.SessionRate, _ = strconv.Atoi(val)
			}
			if val := record[colIndex["scur"]]; val != "" {
				backend.SessionCur, _ = strconv.Atoi(val)
			}
			if val := record[colIndex["smax"]]; val != "" {
				backend.SessionMax, _ = strconv.Atoi(val)
			}
			if val := record[colIndex["bin"]]; val != "" {
				backend.BytesIn, _ = strconv.ParseInt(val, 10, 64)
			}
			if val := record[colIndex["bout"]]; val != "" {
				backend.BytesOut, _ = strconv.ParseInt(val, 10, 64)
			}

			stats.Backends = append(stats.Backends, backend)
		}
	}

	return stats, nil
}

func (c *Client) IsHealthy() bool {
	stats, err := c.GetStats()
	if err != nil {
		return false
	}
	
	// Check if any backend is down
	for _, backend := range stats.Backends {
		if !backend.Active {
			return false
		}
	}
	
	return true
}