# Skyline

A self-hosting tool to deploy multiple apps on your own server with SQLite first-class support.

## Features

- Single binary deployment with embedded components
- SQLite + Litestream integration for reliable database storage
- Automated SSL/TLS with Caddy reverse proxy
- GitHub integration for continuous deployment
- Process supervision for deployed applications
- Simple web UI for application management

## Architecture

The platform comprises several core components:

- **API Server**: RESTful API for app management
- **Proxy Manager**: Dynamic Caddy configuration
- **Supervisor**: Process management for deployed apps
- **Backup Manager**: SQLite backup with Litestream
- **Event Bus**: Internal communication between components
- **Database**: SQLite storage for application state

## Getting Started

### Prerequisites

- Linux server (recommended Ubuntu 20.04+)
- Go 1.18+ (for development)
- Caddy 2.x
- Litestream

### Installation

1. Download the latest release:
   ```bash
   curl -L -o deploy-platform https://github.com/your-username/deploy-platform/releases/latest/download/deploy-platform
   chmod +x deploy-platform
   ```

2. Create a configuration file:
   ```bash
   cp config.example.yaml config.yaml
   # Edit config.yaml with your settings
   ```

3. Start the platform:
   ```bash
   ./deploy-platform -config config.yaml
   ```

### Configuration

See `config.yaml` for available configuration options:

```yaml
server:
  host: "0.0.0.0"
  port: 8080

database:
  path: "data/system/deploy-platform.db"

# Additional configuration sections...
```

## Usage

### Creating an Application

1. Access the web UI at `http://your-server:8080`
2. Click "Create New App"
3. Enter app details including GitHub repository URL
4. Click "Create"

### API Usage

The platform provides a RESTful API:

```bash
# List all apps
curl -X GET http://your-server:8080/api/v1/apps

# Create a new app
curl -X POST http://your-server:8080/api/v1/apps \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-app",
    "repo_url": "https://github.com/example/repo",
    "branch": "main",
    "domain": "my-app.example.com"
  }'
```

See API documentation for all available endpoints.

## Development

### Building from Source

```bash
git clone https://github.com/your-username/deploy-platform.git
cd deploy-platform
go build -o deploy-platform ./cmd/server
```

### Running Tests

```bash
go test ./...
```

### Test API Script

Use the included test script to verify API functionality:

```bash
chmod +x test-api.sh
./test-api.sh
```

## License

MIT License
