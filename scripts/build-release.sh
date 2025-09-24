#!/bin/bash

# build-release.sh - Cross-platform release build script for dolphin-agent
# This script is called by devops-manager create-release process

set -e

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

echo -e "${BLUE}🚀 Building cross-platform release binaries for dolphin-agent${NC}"

# Configuration
BINARY_NAME="dolphin-agent"
DIST_DIR="dist"
VERSION_FILE="VERSION"
BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Read version from VERSION file
VERSION="unknown"
if [ -f "$VERSION_FILE" ]; then
    VERSION=$(cat "$VERSION_FILE" | tr -d '\n\r')
fi

echo -e "${CYAN}Version: $VERSION${NC}"
echo -e "${CYAN}Commit: $GIT_COMMIT${NC}"
echo -e "${CYAN}Build Date: $BUILD_DATE${NC}"
echo ""

# Create dist directory
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

# Build flags for version information
LDFLAGS="-X 'github.com/johnjallday/dolphin-agent/internal/version.Version=$VERSION' -X 'github.com/johnjallday/dolphin-agent/internal/version.GitCommit=$GIT_COMMIT' -X 'github.com/johnjallday/dolphin-agent/internal/version.BuildDate=$BUILD_DATE'"

# Platform configurations
declare -a PLATFORMS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
)

BUILD_SUCCESS=true

# Build for each platform
for platform in "${PLATFORMS[@]}"; do
    IFS='/' read -r -a platform_split <<< "$platform"
    GOOS="${platform_split[0]}"
    GOARCH="${platform_split[1]}"

    output_name="${BINARY_NAME}-${GOOS}-${GOARCH}"
    if [ "$GOOS" = "windows" ]; then
        output_name+='.exe'
    fi

    echo -e "${YELLOW}Building for $GOOS/$GOARCH...${NC}"

    if GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags "$LDFLAGS" \
        -o "$DIST_DIR/$output_name" \
        "./cmd/server"; then

        echo -e "${GREEN}✓ Built $output_name${NC}"

        # Create archive
        cd "$DIST_DIR"
        if [ "$GOOS" = "windows" ]; then
            zip "${BINARY_NAME}-${GOOS}-${GOARCH}.zip" "$output_name"
            echo -e "${GREEN}✓ Created ${BINARY_NAME}-${GOOS}-${GOARCH}.zip${NC}"
        else
            tar -czf "${BINARY_NAME}-${GOOS}-${GOARCH}.tar.gz" "$output_name"
            echo -e "${GREEN}✓ Created ${BINARY_NAME}-${GOOS}-${GOARCH}.tar.gz${NC}"
        fi
        rm "$output_name"  # Remove the binary, keep only the archive
        cd ..

    else
        echo -e "${RED}✗ Failed to build for $GOOS/$GOARCH${NC}"
        BUILD_SUCCESS=false
    fi
done

if [ "$BUILD_SUCCESS" = true ]; then
    echo ""
    echo -e "${GREEN}🎉 All cross-platform builds completed successfully!${NC}"
    echo -e "${BLUE}Release artifacts in $DIST_DIR:${NC}"
    ls -la "$DIST_DIR"/*.tar.gz "$DIST_DIR"/*.zip 2>/dev/null || true
    exit 0
else
    echo ""
    echo -e "${RED}❌ Some builds failed. Check the output above.${NC}"
    exit 1
fi