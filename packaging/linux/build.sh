#!/bin/bash
# Private LLM Linux Package Build Script
# Run from repo root: ./packaging/linux/build.sh <version> <arch>
# Example: ./packaging/linux/build.sh 1.0.0 amd64

set -e

VERSION="${1:-1.0.0}"
ARCH="${2:-amd64}"

# Map arch to GOARCH
case "$ARCH" in
    amd64) GOARCH="amd64" ;;
    arm64) GOARCH="arm64" ;;
    *)
        echo "Unsupported architecture: $ARCH"
        echo "Supported: amd64, arm64"
        exit 1
        ;;
esac

echo "Building private-llm v$VERSION for $ARCH..."

# Create staging directory
STAGING="packaging/linux/staging/pkg"
rm -rf "$STAGING"
mkdir -p "$STAGING/usr/bin"
mkdir -p "$STAGING/etc/private-llm"
mkdir -p "$STAGING/var/lib/private-llm"
mkdir -p "$STAGING/usr/lib/systemd/system"

# Copy systemd service
cp packaging/linux/systemd/private-llm.service "$STAGING/usr/lib/systemd/system/"

# Build the binary
echo "Building binary for $GOARCH..."
CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -o "$STAGING/usr/bin/private-llm" -ldflags "-s -w -X main.version=$VERSION" ./cli

# Set permissions
chmod 755 "$STAGING/usr/bin/private-llm"

# Output paths
echo "Staging complete. Files ready for packaging:"
find "$STAGING" -type f | sort
