#!/bin/bash

# Quick Lustre Client Installer
# Auto-detects Ubuntu version and installs appropriate Lustre client

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

log() { echo -e "${BLUE}[INFO]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1" >&2; exit 1; }
success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }

# Check root
[[ $EUID -eq 0 ]] || error "Run as root: sudo $0"

# Detect Ubuntu version and set Lustre URL
source /etc/os-release
case "$VERSION_ID" in
    "22.04")
        LUSTRE_VERSION="2.15.3"
        URL="https://downloads.whamcloud.com/public/lustre/lustre-2.15.3/ubuntu2204/client/"
        ;;
    "24.04")
        LUSTRE_VERSION="2.16.1"
        URL="https://downloads.whamcloud.com/public/lustre/lustre-2.16.1/ubuntu2404/client/"
        ;;
    *) error "Unsupported Ubuntu version: $VERSION_ID (only 22.04 and 24.04 supported)" ;;
esac

log "Ubuntu $VERSION_ID detected, Lustre version: $LUSTRE_VERSION"
log "Using URL: $URL"

# Install dependencies
log "Installing dependencies..."
apt-get update -qq
apt-get install -y -qq wget curl

# Download and install
DOWNLOAD_DIR="/var/lustre_$LUSTRE_VERSION"
log "Creating download directory: $DOWNLOAD_DIR"
mkdir -p "$DOWNLOAD_DIR"
cd "$DOWNLOAD_DIR"

log "Downloading packages to $DOWNLOAD_DIR..."

# Get package list and download all .deb files
wget -q -O- "$URL" | grep -o 'href="[^"]*\.deb"' | sed 's/href="//g;s/"//g' | while read pkg; do
    log "Downloading $pkg..."
    wget -q "$URL$pkg"
done

# Install packages
log "Installing Lustre client..."
dpkg -i *.deb 2>/dev/null || apt-get install -f -y -qq

# Load modules
log "Loading kernel modules..."
modprobe lustre 2>/dev/null || true
modprobe llite 2>/dev/null || true

# Cleanup (optional - keep packages for future use)
log "Packages saved in: $DOWNLOAD_DIR"
log "To remove packages later, run: rm -rf $DOWNLOAD_DIR"

# Verify
if command -v lfs >/dev/null 2>&1; then
    success "Lustre client installed successfully!"
    echo "Version: $(lfs --version 2>/dev/null || echo 'Unknown')"
    echo "Use 'lfs' command to interact with Lustre filesystems"
else
    error "Installation may have failed - lfs command not found"
fi