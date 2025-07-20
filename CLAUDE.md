# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

iot-hub-statuspage is a Go-based monitoring dashboard for smart home/IoT environments. It monitors services via HAProxy admin socket and collects system metrics, displaying them in a real-time web dashboard.

## Development Commands

```bash
# Build the application
go build -o statuspage ./cmd/statuspage

# Run locally
go run ./cmd/statuspage

# Docker operations
docker-compose up -d        # Start all services
docker-compose down         # Stop services
docker build -t statuspage . # Build Docker image
```

## Architecture

The codebase follows Go's standard project layout with clean architecture principles:

- `cmd/statuspage/` - Application entry point that initializes all components
- `internal/` - Core business logic packages:
  - `haproxy/` - HAProxy admin socket client for service status
  - `metrics/` - System metrics collector using gopsutil
  - `storage/` - PostgreSQL data persistence layer
  - `web/` - HTTP server with SSE endpoints for real-time updates
  - `types/` - Shared data structures

Key architectural decisions:
- Server-Sent Events (SSE) for real-time dashboard updates at `/api/events`
- PostgreSQL for time-series metric storage with automatic schema migration
- Dependency injection pattern throughout
- Graceful shutdown handling for all components

## Important Environment Variables

When running locally or configuring Docker:
- `POSTGRES_PASSWORD` - Required, no default
- `HAPROXY_SOCKET` - Path to HAProxy admin socket (default: /var/run/haproxy/admin.sock)
- `PORT` - HTTP server port (default: 8080)

## Testing and Development Notes

- No test files currently exist in the codebase
- Use `go mod tidy` to clean up dependencies
- The application expects HAProxy admin socket to be accessible
- PostgreSQL connection is required for startup
- Dashboard templates are in `templates/` directory