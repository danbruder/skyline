#!/bin/bash
set -e

# Variables
IP=167.172.236.15
SSH_TARGET=root@167.172.236.15
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

log "Building skyline..."
 CC=x86_64-linux-musl-gcc CXX=x86_64-linux-musl-g++ GOARCH=amd64 GOOS=linux CGO_ENABLED=1 go build -o build/skyline -ldflags "-linkmode external -extldflags -static" cmd/skyline/main.go

log "Setting up the VM..."
scp -r scripts $SSH_TARGET:/tmp/skyline-scripts
ssh $SSH_TARGET "chmod +x /tmp/skyline-scripts/setup.sh && /tmp/skyline-scripts/setup.sh"
scp config.prod.yml $SSH_TARGET:/opt/skyline/config.yaml
log "Uploading the build..."
scp build/skyline $SSH_TARGET:/usr/local/bin/skyline
ssh $SSH_TARGET "chown skyline:skyline /opt/skyline"
log "Starting skyline..."
ssh $SSH_TARGET "systemctl restart skyline"

