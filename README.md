# IoT Hub Status Page

A lightweight, real-time monitoring dashboard for smart home and IoT environments. Built with Go and optimized for resource-constrained devices like Raspberry Pi.

## Features

- **Real-time Service Monitoring** - Monitors services via HAProxy admin socket
- **System Metrics Collection** - CPU, memory, disk, and network statistics
- **Live Dashboard** - Server-Sent Events (SSE) for real-time updates without polling
- **Historical Data** - PostgreSQL storage with automatic 7-day retention
- **Docker Support** - Optional Docker container monitoring
- **Resource Efficient** - Designed for low-memory environments
- **Progressive Enhancement** - Works without JavaScript

## Quick Start

### Using Docker Compose (Recommended)

1. Clone the repository:
```bash
git clone https://github.com/yourusername/iot-hub-statuspage.git
cd iot-hub-statuspage
```

2. Create a `.env` file:
```bash
POSTGRES_PASSWORD=your_secure_password_here
```

3. Start the services:
```bash
docker-compose up -d
```

4. Access the dashboard at `http://localhost:8080`

### Running Locally

1. Ensure PostgreSQL is running and accessible
2. Set environment variables:
```bash
export POSTGRES_PASSWORD=your_password
export POSTGRES_HOST=localhost
export POSTGRES_USER=statuspage
export POSTGRES_DB=statuspage
```

3. Build and run:
```bash
go build -o statuspage ./cmd/statuspage
./statuspage
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `POSTGRES_HOST` | PostgreSQL host | `localhost` |
| `POSTGRES_PORT` | PostgreSQL port | `5432` |
| `POSTGRES_USER` | Database user | `statuspage` |
| `POSTGRES_PASSWORD` | Database password | **Required** |
| `POSTGRES_DB` | Database name | `statuspage` |
| `POSTGRES_SSLMODE` | SSL mode | `disable` |
| `PORT` | HTTP server port | `8080` |
| `HAPROXY_SOCKET` | HAProxy admin socket path | `/var/run/haproxy/admin.sock` |

### HAProxy Configuration

To enable monitoring, configure HAProxy with an admin socket:

```
global
    stats socket /var/run/haproxy/admin.sock mode 600 level admin
```

## Architecture

```
iot-hub-statuspage/
├── cmd/statuspage/      # Application entry point
├── internal/
│   ├── haproxy/        # HAProxy client
│   ├── metrics/        # System metrics collector
│   ├── storage/        # PostgreSQL persistence
│   ├── types/          # Shared data structures
│   └── web/            # HTTP server & SSE
├── templates/          # HTML templates
├── static/             # Static assets
├── docker-compose.yml  # Docker configuration
└── Dockerfile         # Multi-stage build
```

## API Endpoints

- `GET /` - Main dashboard
- `GET /api/status` - Current status (JSON)
- `GET /api/metrics` - Historical metrics
- `GET /api/events` - SSE stream for real-time updates
- `GET /health` - Health check

## Development

### Prerequisites

- Go 1.24 or later
- PostgreSQL 12+
- HAProxy (for service monitoring)
- Docker & Docker Compose (optional)

### Building

```bash
# Build binary
go build -o statuspage ./cmd/statuspage

# Build Docker image
docker build -t statuspage .

# Run tests (when available)
go test ./...
```

### Project Structure

The project follows Go's standard layout with clean architecture principles:

- **Dependency Injection** - Clean separation of concerns
- **Graceful Shutdown** - Proper resource cleanup
- **Automatic Migrations** - No manual database setup required
- **SSE Broadcasting** - Efficient real-time updates

## Deployment

### Docker Deployment

The included `docker-compose.yml` provides a complete deployment:

```yaml
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: statuspage
      POSTGRES_DB: statuspage
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U statuspage"]

  statuspage:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - /var/run/haproxy:/var/run/haproxy:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
    depends_on:
      postgres:
        condition: service_healthy
```

### Kubernetes Deployment

For Kubernetes deployments, ensure:
1. PostgreSQL is accessible
2. HAProxy socket is mounted as a volume
3. Environment variables are configured via ConfigMap/Secrets

## Monitoring Multiple Services

The dashboard automatically discovers and monitors all services configured in HAProxy. Add backends to your HAProxy configuration:

```
backend web_servers
    server web1 192.168.1.10:80 check
    server web2 192.168.1.11:80 check

backend api_servers
    server api1 192.168.1.20:8080 check
    server api2 192.168.1.21:8080 check
```

## Troubleshooting

### Common Issues

1. **Cannot connect to PostgreSQL**
   - Verify `POSTGRES_PASSWORD` is set
   - Check PostgreSQL is running and accessible
   - Ensure database user has proper permissions

2. **No service data displayed**
   - Verify HAProxy admin socket path
   - Check socket permissions (needs read access)
   - Ensure HAProxy has stats socket enabled

3. **High memory usage**
   - Data retention is set to 7 days by default
   - Adjust cleanup schedule if needed
   - Check PostgreSQL query performance

## Contributing

Contributions are welcome! Please:
1. Fork the repository
2. Create a feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## License

This project is released into the public domain under The Unlicense. See [LICENSE.md](LICENSE.md) for details.

## Acknowledgments

Built with:
- [Gin Web Framework](https://github.com/gin-gonic/gin)
- [gopsutil](https://github.com/shirou/gopsutil) for system metrics
- [templ](https://templ.guide) for HTML templating
- [HTMX](https://htmx.org) for progressive enhancement