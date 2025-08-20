#!/bin/bash

# Cross-platform release build script for dolphin-agent
# Builds binaries for multiple platforms

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
OUTPUT_DIR="dist"
VERSION_FILE="VERSION"
BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Read version from VERSION file
VERSION="unknown"
if [ -f "$VERSION_FILE" ]; then
    VERSION=$(cat "$VERSION_FILE" | tr -d '\n\r')
fi

# Platform targets
PLATFORMS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
)

echo -e "${BLUE}🚀 Building dolphin-agent release binaries${NC}"
echo -e "${CYAN}Version: $VERSION${NC}"
echo -e "${CYAN}Commit: $GIT_COMMIT${NC}"
echo -e "${CYAN}Build Date: $BUILD_DATE${NC}"
echo -e "${CYAN}Platforms: ${#PLATFORMS[@]}${NC}"
echo ""

# Clean and create output directory
rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"

# Build flags for version information
LDFLAGS="-X 'github.com/johnjallday/dolphin-agent/internal/version.Version=$VERSION' -X 'github.com/johnjallday/dolphin-agent/internal/version.GitCommit=$GIT_COMMIT' -X 'github.com/johnjallday/dolphin-agent/internal/version.BuildDate=$BUILD_DATE'"

# Build for each platform
for platform in "${PLATFORMS[@]}"; do
    IFS='/' read -r os arch <<< "$platform"
    
    echo -e "${YELLOW}Building for $os/$arch...${NC}"
    
    # Set binary name with extension for Windows
    binary_name="$BINARY_NAME"
    if [ "$os" = "windows" ]; then
        binary_name="${binary_name}.exe"
    fi
    
    # Create platform-specific directory
    platform_dir="$OUTPUT_DIR/$os-$arch"
    mkdir -p "$platform_dir"
    
    # Build binary
    GOOS="$os" GOARCH="$arch" go build \
        -ldflags "$LDFLAGS" \
        -o "$platform_dir/$binary_name" \
        "./cmd/server"
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ Built $os/$arch successfully${NC}"
        
        # Create archive
        archive_name="$BINARY_NAME-$VERSION-$os-$arch"
        if [ "$os" = "windows" ]; then
            archive_name="${archive_name}.zip"
            (cd "$OUTPUT_DIR" && zip -r "$archive_name" "$os-$arch/")
        else
            archive_name="${archive_name}.tar.gz"
            (cd "$OUTPUT_DIR" && tar -czf "$archive_name" "$os-$arch/")
        fi
        echo -e "${CYAN}  → Archive: $archive_name${NC}"
    else
        echo -e "${RED}✗ Failed to build $os/$arch${NC}"
        exit 1
    fi
done

# Build plugins once (they're platform-specific anyway due to cgo)
echo -e "${YELLOW}Building plugins...${NC}"
if [ -f "scripts/build-plugins.sh" ]; then
    BUILD_PLUGINS=true ./scripts/build-plugins.sh
    if [ -d "uploaded_plugins" ] && [ "$(ls -A uploaded_plugins 2>/dev/null)" ]; then
        # Copy plugins to each platform directory
        for platform in "${PLATFORMS[@]}"; do
            IFS='/' read -r os arch <<< "$platform"
            platform_dir="$OUTPUT_DIR/$os-$arch"
            mkdir -p "$platform_dir/plugins"
            cp uploaded_plugins/*.so "$platform_dir/plugins/" 2>/dev/null || true
        done
        echo -e "${GREEN}✓ Plugins built and distributed${NC}"
    fi
fi

echo ""
echo -e "${GREEN}🎉 Release build completed successfully!${NC}"
echo -e "${BLUE}Output directory: $OUTPUT_DIR${NC}"
echo ""
echo -e "${BLUE}Release files:${NC}"
ls -la "$OUTPUT_DIR"/*.tar.gz "$OUTPUT_DIR"/*.zip 2>/dev/null || true
echo ""
echo -e "${BLUE}Platform directories:${NC}"
ls -la "$OUTPUT_DIR"/*/