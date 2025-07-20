# Fetch dependencies
FROM golang:1.24-alpine AS fetch-stage
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Generate templ files
FROM ghcr.io/a-h/templ:latest AS generate-stage
COPY --chown=65532:65532 . /app
WORKDIR /app
RUN ["templ", "generate"]

# Build stage
FROM golang:1.24-alpine AS builder

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum first
COPY --from=fetch-stage /app/go.mod /app/go.sum ./

# Copy the downloaded modules
COPY --from=fetch-stage /go/pkg/mod /go/pkg/mod

# Copy source code with generated templ files
COPY --from=generate-stage /app ./

# Download any missing dependencies (in case templ generated code needs them)
RUN go mod download

# Build the application with maximum size optimization (no CGO needed!)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags '-s -w' \
    -tags 'netgo osusergo' \
    -trimpath \
    -o statuspage ./cmd/statuspage

# Final stage - using Alpine for minimal size
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates

# No need for data directory with PostgreSQL

# Copy binary from builder
COPY --from=builder /app/statuspage /usr/local/bin/statuspage

# Copy static files and templates
COPY --from=builder /app/templates /app/templates
COPY --from=builder /app/static /app/static

# Run as root by default for Docker socket access

# Set working directory
WORKDIR /app

# Expose port
EXPOSE 8080

# Environment variables
ENV PORT=8080 \
    HAPROXY_SOCKET=/var/run/haproxy/admin.sock \
    POSTGRES_HOST=postgres \
    POSTGRES_PORT=5432 \
    POSTGRES_USER=statuspage \
    POSTGRES_DB=statuspage \
    POSTGRES_SSLMODE=disable

# Run the application
CMD ["statuspage"]