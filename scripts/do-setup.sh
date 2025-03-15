#!/bin/bash
set -e

# Configuration
DO_API_TOKEN="" # Set your Digital Ocean API token
SSH_KEY_ID=""   # Your Digital Ocean SSH key ID
DROPLET_NAME="skyline-platform"
DROPLET_REGION="nyc1"
DROPLET_SIZE="s-1vcpu-1gb"
REPO_URL="https://github.com/danbruder/skyline.git"

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

if ! command -v jq &> /dev/null; then
  log_error "jq is required but not installed. Please install it with 'apt install jq' or equivalent."
fi

if [[ -z "$DO_API_TOKEN" ]]; then
  log_error "Please set your Digital Ocean API token in the script."
fi

if [[ -z "$SSH_KEY_ID" ]]; then
  log_error "Please set your SSH key ID in the script. You can find this in the Digital Ocean console or API."
fi

# Create droplet
log "Creating Digital Ocean droplet..."
DROPLET_DATA=$(curl -s -X POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DO_API_TOKEN" \
  -d "{
    \"name\":\"$DROPLET_NAME\",
    \"region\":\"$DROPLET_REGION\",
    \"size\":\"$DROPLET_SIZE\",
    \"image\":\"debian-12-x64\",
    \"ssh_keys\":[\"$SSH_KEY_ID\"],
    \"backups\":false,
    \"ipv6\":false,
    \"monitoring\":true,
    \"tags\":[\"skyline\",\"deployment-platform\"]
  }" \
  "https://api.digitalocean.com/v2/droplets")

DROPLET_ID=$(echo $DROPLET_DATA | jq -r '.droplet.id')

if [[ "$DROPLET_ID" == "null" ]]; then
  log_error "Failed to create droplet: $(echo $DROPLET_DATA | jq -r '.message')"
fi

log "Droplet created with ID: $DROPLET_ID"

# Wait for droplet to be active
log "Waiting for droplet to become active..."
while true; do
  DROPLET_STATUS=$(curl -s -X GET \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $DO_API_TOKEN" \
    "https://api.digitalocean.com/v2/droplets/$DROPLET_ID" | jq -r '.droplet.status')
  
  if [[ "$DROPLET_STATUS" == "active" ]]; then
    break
  fi
  
  echo -n "."
  sleep 5
done

# Get droplet IP
DROPLET_IP=$(curl -s -X GET \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DO_API_TOKEN" \
  "https://api.digitalocean.com/v2/droplets/$DROPLET_ID" | jq -r '.droplet.networks.v4[0].ip_address')

log "Droplet is active with IP: $DROPLET_IP"

# Wait for SSH to be available
log "Waiting for SSH to become available..."
while ! nc -z $DROPLET_IP 22 &>/dev/null; do
  echo -n "."
  sleep 5
done
echo ""

# Give SSH a moment to fully initialize
sleep 10

log "Deployment complete!"
log "Skyline platform is now running at http://$DROPLET_IP:8080"
log "You may need to create firewall rules in Digital Ocean console to allow HTTP/HTTPS traffic"
