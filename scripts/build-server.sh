#!/bin/bash

# Build server only script for dolphin-agent project
# Builds just the main server binary without plugins

set -e  # Exit on any error

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
BINARY_NAME="dolphin-agent"
OUTPUT_DIR="bin"
VERSION_FILE="VERSION"
BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Read version from VERSION file
VERSION="unknown"
if [ -f "$VERSION_FILE" ]; then
    VERSION=$(cat "$VERSION_FILE" | tr -d '\n\r')
fi

echo -e "${BLUE}🔨 Building dolphin-agent server${NC}"
echo -e "${CYAN}Version: $VERSION${NC}"
echo -e "${CYAN}Commit: $GIT_COMMIT${NC}"
echo -e "${CYAN}Build Date: $BUILD_DATE${NC}"
echo ""

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Build flags for version information
LDFLAGS="-X 'github.com/johnjallday/dolphin-agent/internal/version.Version=$VERSION' -X 'github.com/johnjallday/dolphin-agent/internal/version.GitCommit=$GIT_COMMIT' -X 'github.com/johnjallday/dolphin-agent/internal/version.BuildDate=$BUILD_DATE'"

# Check if we should build for specific OS/ARCH
TARGET_OS=${GOOS:-$(go env GOOS)}
TARGET_ARCH=${GOARCH:-$(go env GOARCH)}

echo -e "${YELLOW}Building for $TARGET_OS/$TARGET_ARCH...${NC}"

# Build the main server binary
if [ "$TARGET_OS" = "windows" ]; then
    BINARY_NAME="${BINARY_NAME}.exe"
fi

GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" go build \
    -ldflags "$LDFLAGS" \
    -o "$OUTPUT_DIR/$BINARY_NAME" \
    "./cmd/server"

if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Server binary built successfully: $OUTPUT_DIR/$BINARY_NAME${NC}"
    echo ""
    echo -e "${BLUE}Output file:${NC}"
    ls -la "$OUTPUT_DIR/$BINARY_NAME"
    echo ""
    echo -e "${CYAN}To run the server:${NC}"
    echo -e "${CYAN}  ./$OUTPUT_DIR/$BINARY_NAME${NC}"
else
    echo -e "${RED}✗ Failed to build server binary${NC}"
    exit 1
fi