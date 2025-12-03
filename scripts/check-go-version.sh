#!/bin/bash

# Check Go version and provide upgrade instructions if needed
# This script detects security vulnerabilities in the Go standard library

set -e

REQUIRED_GO_VERSION="1.25.5"
CURRENT_GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')

echo "Current Go version: $CURRENT_GO_VERSION"
echo "Required Go version: $REQUIRED_GO_VERSION (or later)"
echo ""

# Simple version comparison
version_ge() {
    # Returns 0 (success) if $1 >= $2
    [ "$(printf '%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]
}

if version_ge "$CURRENT_GO_VERSION" "$REQUIRED_GO_VERSION"; then
    echo "✅ Go version is up to date!"
    exit 0
else
    echo "❌ Go version is outdated and has known security vulnerabilities"
    echo ""
    echo "How to upgrade Go:"
    echo ""

    # Detect platform and provide specific instructions
    OS=$(uname -s)
    case "$OS" in
        Darwin)
            echo "macOS detected. Choose one method:"
            echo ""
            echo "1. Using Homebrew (recommended):"
            echo "   brew update"
            echo "   brew upgrade go"
            echo ""
            echo "2. Manual download:"
            echo "   Visit: https://go.dev/dl/"
            echo "   Download go$REQUIRED_GO_VERSION.darwin-arm64.pkg (Apple Silicon)"
            echo "   Or:    go$REQUIRED_GO_VERSION.darwin-amd64.pkg (Intel)"
            ;;
        Linux)
            echo "Linux detected. Choose one method:"
            echo ""
            echo "1. Using package manager:"
            echo "   # For Ubuntu/Debian:"
            echo "   sudo add-apt-repository ppa:longsleep/golang-backports"
            echo "   sudo apt update"
            echo "   sudo apt install golang-go"
            echo ""
            echo "   # For Fedora:"
            echo "   sudo dnf upgrade golang"
            echo ""
            echo "2. Manual download:"
            echo "   Visit: https://go.dev/dl/"
            echo "   Download and install go$REQUIRED_GO_VERSION.linux-amd64.tar.gz"
            ;;
        *)
            echo "Visit https://go.dev/dl/ and download Go $REQUIRED_GO_VERSION for your platform"
            ;;
    esac

    echo ""
    echo "After upgrading, run:"
    echo "  go version  # Verify upgrade"
    echo "  go clean -modcache  # Clear module cache"
    echo "  go mod tidy  # Refresh dependencies"
    echo ""

    exit 1
fi
