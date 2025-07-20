package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hra42/iot-hub-statuspage/internal/haproxy"
	"github.com/hra42/iot-hub-statuspage/internal/metrics"
	"github.com/hra42/iot-hub-statuspage/internal/storage"
	"github.com/hra42/iot-hub-statuspage/internal/web"
)

func main() {
	// Log the current user
	log.Printf("Starting statuspage as UID: %d, GID: %d", os.Getuid(), os.Getgid())
	
	// Build PostgreSQL connection string from environment variables
	dbHost := getEnv("POSTGRES_HOST", "localhost")
	dbPort := getEnv("POSTGRES_PORT", "5432")
	dbUser := getEnv("POSTGRES_USER", "statuspage")
	dbPassword := getEnv("POSTGRES_PASSWORD", "")
	dbName := getEnv("POSTGRES_DB", "statuspage")
	dbSSLMode := getEnv("POSTGRES_SSLMODE", "disable")
	
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		dbHost, dbPort, dbUser, dbPassword, dbName, dbSSLMode)
	
	// Initialize storage
	db, err := storage.NewDB(connStr)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize HAProxy client
	haproxyClient := haproxy.NewClient(getEnv("HAPROXY_SOCKET", "/var/run/haproxy/admin.sock"))

	// Initialize metrics collector
	collector := metrics.NewCollector(db, haproxyClient)

	// Start metrics collection
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go collector.Start(ctx)

	// Initialize web server
	server := web.NewServer(db, haproxyClient, collector)
	
	srv := &http.Server{
		Addr:    ":" + getEnv("PORT", "8080"),
		Handler: server.Router(),
	}

	// Handle graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Server starting on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-quit
	log.Println("Shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exiting")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}