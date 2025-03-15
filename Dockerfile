FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install required dependencies
RUN apk add --no-cache git gcc musl-dev

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN GOOS=linux go build -o skyline ./cmd/skyline

FROM alpine:3.19

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

# Copy binary from builder stage
COPY --from=builder /app/skyline /usr/local/bin/skyline

# Create necessary directories
RUN mkdir -p /app/data /app/deployed

# Set environment variables
ENV SKYLINE_DATA_DIR=/app/data
ENV SKYLINE_CONFIG_FILE=/app/config.yaml
ENV SKYLINE_LOG_LEVEL=info

# Expose necessary ports
# API server
EXPOSE 8080
# Caddy default HTTPS port
EXPOSE 443
# Caddy default HTTP port (for ACME challenges)
EXPOSE 80

# Create a non-root user and set permissions
RUN addgroup -S skyline && adduser -S skyline -G skyline
RUN chown -R skyline:skyline /app
USER skyline

# Set the entrypoint
CMD ["/usr/local/bin/skyline"]
