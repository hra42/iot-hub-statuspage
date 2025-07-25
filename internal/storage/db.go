package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

type DB struct {
	conn *sql.DB
}

type ServiceStatus struct {
	ID        int64
	Service   string
	Status    string
	Timestamp time.Time
	Details   string
}

type SystemMetric struct {
	ID         int64
	MetricType string
	Value      float64
	Timestamp  time.Time
}

func NewDB(connStr string) (*DB, error) {
	conn, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{conn: conn}
	
	// Set PostgreSQL connection pool settings
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)
	
	if err := db.createTables(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	// Start cleanup routine
	go db.cleanupOldData()

	return db, nil
}

func (db *DB) createTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS service_status (
			id SERIAL PRIMARY KEY,
			service VARCHAR(255) NOT NULL,
			status VARCHAR(50) NOT NULL,
			timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			details TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS system_metrics (
			id SERIAL PRIMARY KEY,
			metric_type VARCHAR(50) NOT NULL,
			value DOUBLE PRECISION NOT NULL,
			timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_service_status_timestamp ON service_status(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_service_status_service ON service_status(service, timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_system_metrics_timestamp ON system_metrics(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_system_metrics_type ON system_metrics(metric_type, timestamp)`,
	}

	for _, query := range queries {
		if _, err := db.conn.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query: %w", err)
		}
	}

	return nil
}

func (db *DB) InsertServiceStatus(service, status, details string) error {
	query := `INSERT INTO service_status (service, status, details) VALUES ($1, $2, $3)`
	_, err := db.conn.Exec(query, service, status, details)
	return err
}

func (db *DB) InsertSystemMetric(metricType string, value float64) error {
	query := `INSERT INTO system_metrics (metric_type, value) VALUES ($1, $2)`
	_, err := db.conn.Exec(query, metricType, value)
	return err
}

// BulkInsert performs bulk inserts for both system metrics and service statuses in a single transaction
func (db *DB) BulkInsert(metrics []SystemMetric, statuses []ServiceStatus) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Prepare statements for bulk inserts
	metricStmt, err := tx.Prepare(`INSERT INTO system_metrics (metric_type, value) VALUES ($1, $2)`)
	if err != nil {
		return fmt.Errorf("failed to prepare metric statement: %w", err)
	}
	defer metricStmt.Close()

	statusStmt, err := tx.Prepare(`INSERT INTO service_status (service, status, details) VALUES ($1, $2, $3)`)
	if err != nil {
		return fmt.Errorf("failed to prepare status statement: %w", err)
	}
	defer statusStmt.Close()

	// Insert all metrics
	for _, metric := range metrics {
		if _, err := metricStmt.Exec(metric.MetricType, metric.Value); err != nil {
			return fmt.Errorf("failed to insert metric: %w", err)
		}
	}

	// Insert all statuses
	for _, status := range statuses {
		if _, err := statusStmt.Exec(status.Service, status.Status, status.Details); err != nil {
			return fmt.Errorf("failed to insert status: %w", err)
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (db *DB) GetServiceStatusHistory(service string, duration time.Duration) ([]ServiceStatus, error) {
	since := time.Now().Add(-duration)
	query := `
		SELECT id, service, status, timestamp, details 
		FROM service_status 
		WHERE service = $1 AND timestamp >= $2 
		ORDER BY timestamp DESC
	`
	
	rows, err := db.conn.Query(query, service, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var statuses []ServiceStatus
	for rows.Next() {
		var s ServiceStatus
		if err := rows.Scan(&s.ID, &s.Service, &s.Status, &s.Timestamp, &s.Details); err != nil {
			return nil, err
		}
		statuses = append(statuses, s)
	}

	return statuses, rows.Err()
}

func (db *DB) GetSystemMetricsHistory(metricType string, duration time.Duration) ([]SystemMetric, error) {
	since := time.Now().Add(-duration)
	query := `
		SELECT id, metric_type, value, timestamp 
		FROM system_metrics 
		WHERE metric_type = $1 AND timestamp >= $2 
		ORDER BY timestamp ASC
	`
	
	rows, err := db.conn.Query(query, metricType, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []SystemMetric
	for rows.Next() {
		var m SystemMetric
		if err := rows.Scan(&m.ID, &m.MetricType, &m.Value, &m.Timestamp); err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}

	return metrics, rows.Err()
}

func (db *DB) GetLatestServiceStatuses() (map[string]ServiceStatus, error) {
	query := `
		WITH latest AS (
			SELECT service, MAX(timestamp) as max_timestamp
			FROM service_status
			GROUP BY service
		)
		SELECT s.id, s.service, s.status, s.timestamp, s.details
		FROM service_status s
		INNER JOIN latest l ON s.service = l.service AND s.timestamp = l.max_timestamp
	`
	
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	statuses := make(map[string]ServiceStatus)
	for rows.Next() {
		var s ServiceStatus
		if err := rows.Scan(&s.ID, &s.Service, &s.Status, &s.Timestamp, &s.Details); err != nil {
			return nil, err
		}
		statuses[s.Service] = s
	}

	return statuses, rows.Err()
}

func (db *DB) cleanupOldData() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		retention := 7 * 24 * time.Hour
		cutoff := time.Now().Add(-retention)

		queries := []string{
			`DELETE FROM service_status WHERE timestamp < $1`,
			`DELETE FROM system_metrics WHERE timestamp < $1`,
		}

		for _, query := range queries {
			if _, err := db.conn.Exec(query, cutoff); err != nil {
				// Log error but don't stop the cleanup routine
				fmt.Printf("cleanup error: %v\n", err)
			}
		}
	}
}

func (db *DB) Ping() error {
	return db.conn.Ping()
}

func (db *DB) GetDatabaseSize() (int64, error) {
	var sizeBytes int64
	
	query := `SELECT pg_database_size(current_database())`
	
	err := db.conn.QueryRow(query).Scan(&sizeBytes)
	if err != nil {
		return 0, fmt.Errorf("failed to get database size: %w", err)
	}
	
	return sizeBytes, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}