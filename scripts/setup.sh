#!/bin/bash
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

log "Starting installation of skyline dependencies..."

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
    ufw allow 8080/tcp comment "skyline API"
    
    # Enable UFW if not already enabled
    if ! ufw status | grep -q "Status: active"; then
      log "Enabling UFW firewall..."
      ufw --force enable
    fi
    
    log "UFW firewall configured."
  else
    log_error "No supported firewall detected (ufw or firewalld)."
    log "Please configure your firewall manually to allow ports 22, 80, 443, and 8080."
  fi
}

# Create skyline directories
create_directories() {
  log "Creating skyline directories..."
  if id "skyline" &>/dev/null; then
    log "User skyline already exists"
  else
    sudo useradd -r -s /bin/false skyline
    log "User skyline created"
  fi
  
  mkdir -p /opt/skyline/data
  mkdir -p /opt/skyline/config
  mkdir -p /opt/skyline/deployed
  mkdir -p /opt/skyline/logs
  
  # Set appropriate permissions (adjust user/group as needed)
  chown -R skyline:skyline /opt/skyline
  chmod -R 755 /opt/skyline
  
  log "skyline directories created at /opt/skyline"
}

# Create service file
create_service_file() {
  log "Creating systemd service file..."
  
  # Create the service file
  cat > /etc/systemd/system/skyline.service << EOL
[Unit]
Description=Skyline service
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/skyline serve -config /opt/skyline/config.yaml
Restart=on-failure
User=skyline
Group=skyline
WorkingDirectory=/opt/skyline
StandardOutput=append:/opt/skyline/logs/skyline.log
StandardError=append:/opt/skyline/logs/skyline.log

[Install]
WantedBy=multi-user.target
EOL

  # Reload systemd to recognize the new service
  systemctl daemon-reload
  
  log "Service file created. You can start the skyline with: systemctl start skyline"
}

# Main installation
main() {
  # Update package lists
  log "Updating package lists..."
  if [[ "$OS" == "debian" || "$OS" == "ubuntu" ]]; then
    apt clean 
    apt update -y
  fi
  
  # Install dependencies
  log "Installing required dependencies..."
  if [[ "$OS" == "debian" || "$OS" == "ubuntu" ]]; then
    apt install -y curl wget gnupg2 git sqlite3
  fi
  
  # Install components
  install_caddy
  install_litestream
  
  # Configure system
  configure_firewall
  create_directories
  create_service_file
  
  log "Installation completed successfully."
  log "Your skyline is now ready to be deployed."
  log "Place your configuration in /opt/skyline/config/config.yaml"
  log "Place your binary at /usr/local/bin/skyline"
  log "Data will be stored in /opt/skyline/data"
  log "Deployed applications will be in /opt/skyline/deployed"
  log "Logs will be written to /opt/skyline/logs"
  log "Start the service with: systemctl enable --now skyline"
}

# Run the main function
main
