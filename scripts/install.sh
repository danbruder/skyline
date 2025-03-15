#!/bin/bash
set -e

# Variables
VERSION="latest"
INSTALL_DIR="/usr/local/bin"
GITHUB_REPO="danbruder/skyline"

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Logging functions
log() {
  echo -e "${GREEN}[INFO]${NC} $1"
}

log_error() {
  echo -e "${RED}[ERROR]${NC} $1"
  exit 1
}

# Check requirements
if ! command -v curl &> /dev/null; then
  log_error "curl is required but not installed."
fi

# Parse arguments
while [[ "$#" -gt 0 ]]; do
  case $1 in
    -v|--version) VERSION="$2"; shift ;;
    -d|--directory) INSTALL_DIR="$2"; shift ;;
    *) log_error "Unknown parameter: $1" ;;
  esac
  shift
done

# Determine architecture
ARCH=$(uname -m)
case $ARCH in
  x86_64) GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *) log_error "Unsupported architecture: $ARCH" ;;
esac

# Determine OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case $OS in
  linux) GOOS="linux" ;;
  darwin) GOOS="darwin" ;;
  *) log_error "Unsupported OS: $OS" ;;
esac

# Get latest release version if needed
if [ "$VERSION" = "latest" ]; then
  log "Finding latest release..."
  VERSION=$(curl -s https://api.github.com/repos/${GITHUB_REPO}/releases/latest | 
            grep '"tag_name":' | 
            sed -E 's/.*"([^"]+)".*/\1/')
  
  if [ -z "$VERSION" ]; then
    log_error "Could not determine latest version"
  fi
  
  log "Latest version is $VERSION"
fi

# Remove 'v' prefix if present
VERSION_NUM=${VERSION#v}

# Download URL
FILENAME="skyline-${VERSION}-${GOOS}-${GOARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${FILENAME}"

# Create temp directory
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

# Download and extract
log "Downloading Skyline $VERSION for $GOOS/$GOARCH..."
curl -L "$DOWNLOAD_URL" -o "$TMP_DIR/$FILENAME"

log "Extracting..."
tar -xzf "$TMP_DIR/$FILENAME" -C "$TMP_DIR"

# Find the binary (name might vary with version)
BINARY=$(find "$TMP_DIR" -type f -executable | head -n 1)

if [ -z "$BINARY" ]; then
  log_error "Could not find executable in the archive"
fi

# Install the binary
log "Installing to $INSTALL_DIR..."
[ -d "$INSTALL_DIR" ] || mkdir -p "$INSTALL_DIR"
cp "$BINARY" "$INSTALL_DIR/skyline"
chmod +x "$INSTALL_DIR/skyline"

log "Installation complete! Skyline $VERSION installed at $INSTALL_DIR/skyline"
log "Run 'skyline setup' to set up dependencies and service"
