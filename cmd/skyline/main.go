package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/danbruder/skyline/internal/api"
	"github.com/danbruder/skyline/internal/config"
	"github.com/danbruder/skyline/internal/db"
	"github.com/danbruder/skyline/internal/proxy"
	"github.com/danbruder/skyline/internal/supervisor"
	"github.com/danbruder/skyline/pkg/errors"
	"github.com/danbruder/skyline/pkg/events"
)

const (
	defaultConfigPath = "config.yaml"
)

func main() {
	// Define command-line flags
	rootCmd := flag.NewFlagSet("skyline", flag.ExitOnError)
	configPath := rootCmd.String("config", defaultConfigPath, "Path to configuration file")

	// Define subcommands
	serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
	serveConfigPath := serveCmd.String("config", defaultConfigPath, "Path to configuration file")

	// Check if any command-line arguments are provided
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Parse the subcommand
	switch os.Args[1] {
	case "serve":
		serveCmd.Parse(os.Args[2:])
		runServer(*serveConfigPath)
	case "setup":
		runSetup(false)
	case "version":
		fmt.Println("Skyline Deployment Platform v0.1.0")
	case "help":
		printUsage()
	default:
		// Handle the case when the first argument is a flag, not a subcommand
		rootCmd.Parse(os.Args[1:])
		if rootCmd.Parsed() {
			runServer(*configPath)
		} else {
			printUsage()
			os.Exit(1)
		}
	}
}

func printUsage() {
	fmt.Println("Skyline Deployment Platform")
	fmt.Println("\nUsage:")
	fmt.Println("  skyline [command] [options]")
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  serve     Start the Skyline server")
	fmt.Println("  setup     Prepare the host for running Skyline")
	fmt.Println("  version   Print the version information")
	fmt.Println("  help      Show this help message")
	fmt.Println("\nOptions:")
	fmt.Println("  -config   Path to configuration file (default: config.yaml)")
	fmt.Println("\nUse \"skyline [command] --help\" for more information about a command.")
}

func runSetup(force bool) {
	logger := log.New(os.Stdout, "[skyline] ", log.LstdFlags)
	logger.Println("Running setup...")

	// Check if running as root
	if os.Geteuid() != 0 {
		logger.Println("Setup requires root privileges. Please run with sudo.")
		os.Exit(1)
	}

	// Extract the embedded script
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "skyline-setup")
	if err != nil {
		logger.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create the script file
	scriptPath := filepath.Join(tempDir, "install.sh")
	if err := os.WriteFile(scriptPath, []byte(setupScript), 0755); err != nil {
		logger.Fatalf("Failed to write setup script: %v", err)
	}

	// Build the command
	cmd := exec.Command("bash", scriptPath)
	if force {
		cmd.Env = append(os.Environ(), "FORCE=true")
	}

	// Connect to standard output and error
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run the script
	logger.Println("Executing setup script...")
	if err := cmd.Run(); err != nil {
		logger.Fatalf("Setup script failed: %v", err)
	}

	logger.Println("Setup completed successfully")
}
func runServer(configPath string) {
	// Initialize logger
	logger := log.New(os.Stdout, "[skyline] ", log.LstdFlags)
	logger.Println("Starting Skyline deployment platform...")

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize event bus for internal communication
	eventBus := events.NewEventBus()
	standardLogger := errors.NewStandardLogger(logger)

	// Initialize database
	database, err := db.New(ctx, cfg.Database.Path, standardLogger)
	if err != nil {
		logger.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// Initialize supervisor
	sup := supervisor.New(ctx, cfg.Supervisor, logger, eventBus)
	go func() {
		if err := sup.Start(); err != nil {
			logger.Printf("Supervisor error: %v", err)
			cancel()
		}
	}()

	// Initialize Caddy proxy
	proxyManager := proxy.NewCaddyManager(cfg.Proxy, logger)
	if err := proxyManager.Start(); err != nil {
		logger.Fatalf("Failed to start proxy: %v", err)
	}
	defer proxyManager.Stop()

	// Initialize API server
	apiServer := api.NewServer(cfg.API, logger, database, eventBus)
	go func() {
		if err := apiServer.Start(); err != nil {
			logger.Printf("API server error: %v", err)
			cancel()
		}
	}()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigChan:
		logger.Printf("Received signal: %v, shutting down...", sig)
	case <-ctx.Done():
		logger.Println("Context cancelled, shutting down...")
	}

	// Cleanup and graceful shutdown logic
	apiServer.Stop()
	sup.Stop()
	logger.Println("Deployment platform stopped")
}

const setupScript = `#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Function to check if a command exists
command_exists() {
  command -v "$1" >/dev/null 2>&1
}

# Function to display status messages
log() {
  echo -e "${GREEN}[INFO]${NC} $1"
}

log_error() {
  echo -e "${RED}[ERROR]${NC} $1"
}

# Check if script is run as root
if [ "$(id -u)" -ne 0 ]; then
  log_error "This script must be run as root. Try using sudo."
  exit 1
fi

# Check for FORCE flag
if [ "${FORCE}" = "true" ]; then
  log "Force mode enabled. Will reinstall components even if already present."
fi

log "Starting installation of platform dependencies..."

# Detect OS
if [ -f /etc/os-release ]; then
  . /etc/os-release
  OS=$ID
else
  log_error "Unable to detect operating system."
  exit 1
fi

# Install Caddy
install_caddy() {
  if command_exists caddy && [ "${FORCE}" != "true" ]; then
    log "Caddy is already installed. Skipping installation."
    return 0
  fi
  
  log "Installing Caddy..."
  
  case $OS in
    debian|ubuntu)
      apt update
      apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
      curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
      curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
      apt update
      apt install -y caddy
      ;;
    fedora|rhel|centos|rocky|almalinux)
      dnf install -y 'dnf-command(copr)'
      dnf copr enable -y @caddy/caddy
      dnf install -y caddy
      ;;
    *)
      log_error "Unsupported OS for automatic Caddy installation."
      log "Please install Caddy manually from https://caddyserver.com/docs/install"
      return 1
      ;;
  esac
  
  # Enable and start Caddy service
  systemctl enable caddy
  systemctl start caddy
  
  log "Caddy installed successfully."
}

# Install Litestream
install_litestream() {
  if command_exists litestream && [ "${FORCE}" != "true" ]; then
    log "Litestream is already installed. Skipping installation."
    return 0
  fi
  
  log "Installing Litestream..."
  
  # Download the latest Litestream binary
  LITESTREAM_VERSION="0.3.11"
  
  case "$(uname -m)" in
    x86_64)
      ARCH="amd64"
      ;;
    aarch64|arm64)
      ARCH="arm64"
      ;;
    armv7l)
      ARCH="arm"
      ;;
    *)
      log_error "Unsupported architecture: $(uname -m)"
      exit 1
      ;;
  esac
  
  LITESTREAM_URL="https://github.com/benbjohnson/litestream/releases/download/v${LITESTREAM_VERSION}/litestream-v${LITESTREAM_VERSION}-linux-${ARCH}.tar.gz"
  
  # Create temporary directory
  TMP_DIR=$(mktemp -d)
  
  # Download and extract
  curl -L "$LITESTREAM_URL" | tar xz -C "$TMP_DIR"
  
  # Move binary to a location in PATH
  mv "$TMP_DIR/litestream" /usr/local/bin/
  
  # Cleanup
  rm -rf "$TMP_DIR"
  
  # Verify installation
  if command_exists litestream; then
    log "Litestream installed successfully."
  else
    log_error "Litestream installation failed."
    exit 1
  fi
}

# Configure firewall
configure_firewall() {
  log "Configuring firewall..."
  
  if command_exists ufw; then
    # Ubuntu/Debian with UFW
    ufw allow 22/tcp comment "SSH"
    ufw allow 80/tcp comment "HTTP"
    ufw allow 443/tcp comment "HTTPS"
    ufw allow 8080/tcp comment "Platform API"
    
    # Enable UFW if not already enabled
    if ! ufw status | grep -q "Status: active"; then
      log "Enabling UFW firewall..."
      ufw --force enable
    fi
    
    log "UFW firewall configured."
  elif command_exists firewall-cmd; then
    # RHEL/CentOS/Fedora with firewalld
    firewall-cmd --permanent --add-service=ssh
    firewall-cmd --permanent --add-service=http
    firewall-cmd --permanent --add-service=https
    firewall-cmd --permanent --add-port=8080/tcp
    firewall-cmd --reload
    
    # Enable firewalld if not already enabled
    if ! systemctl is-active --quiet firewalld; then
      log "Enabling firewalld..."
      systemctl enable --now firewalld
    fi
    
    log "Firewalld configured."
  else
    log_error "No supported firewall detected (ufw or firewalld)."
    log "Please configure your firewall manually to allow ports 22, 80, 443, and 8080."
  fi
}

# Create platform directories
create_directories() {
  log "Creating platform directories..."
  
  mkdir -p /opt/platform/data
  mkdir -p /opt/platform/config
  mkdir -p /opt/platform/deployed
  mkdir -p /opt/platform/logs
  
  # Set appropriate permissions (adjust user/group as needed)
  chown -R root:root /opt/platform
  chmod -R 755 /opt/platform
  
  log "Platform directories created at /opt/platform"
}

# Create service file
create_service_file() {
  log "Creating systemd service file..."
  
  # Create the service file
  cat > /etc/systemd/system/platform.service << EOL
[Unit]
Description=Self-Hosting Deployment Platform
After=network.target

[Service]
ExecStart=/usr/local/bin/platform serve -config /opt/platform/config/config.yaml
Restart=on-failure
User=root
Group=root
WorkingDirectory=/opt/platform
StandardOutput=append:/opt/platform/logs/platform.log
StandardError=append:/opt/platform/logs/platform.log

[Install]
WantedBy=multi-user.target
EOL

  # Reload systemd to recognize the new service
  systemctl daemon-reload
  
  log "Service file created. You can start the platform with: systemctl start platform"
}

# Main installation
main() {
  # Update package lists
  log "Updating package lists..."
  if [[ "$OS" == "debian" || "$OS" == "ubuntu" ]]; then
    apt clean 
    apt update -y
  elif [[ "$OS" == "fedora" || "$OS" == "rhel" || "$OS" == "centos" || "$OS" == "rocky" || "$OS" == "almalinux" ]]; then
    dnf check-update || true
  fi
  
  # Install dependencies
  log "Installing required dependencies..."
  if [[ "$OS" == "debian" || "$OS" == "ubuntu" ]]; then
    apt install -y curl wget gnupg2 git
  elif [[ "$OS" == "fedora" || "$OS" == "rhel" || "$OS" == "centos" || "$OS" == "rocky" || "$OS" == "almalinux" ]]; then
    dnf install -y curl wget gnupg2 git
  fi
  
  # Install components
  install_caddy
  install_litestream
  
  # Configure system
  configure_firewall
  create_directories
  create_service_file
  
  log "Installation completed successfully."
  log "Your platform is now ready to be deployed."
  log "Place your configuration in /opt/platform/config/config.yaml"
  log "Place your binary at /usr/local/bin/platform"
  log "Data will be stored in /opt/platform/data"
  log "Deployed applications will be in /opt/platform/deployed"
  log "Logs will be written to /opt/platform/logs"
  log "Start the service with: systemctl enable --now platform"
}

# Run the main function
main
`
