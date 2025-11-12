#!/bin/bash

# Build script for ori-agent project
# Builds the main server binary and all plugins

set -e # Exit on any error

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
BINARY_NAME="ori-agent"
MENUBAR_BINARY_NAME="ori-menubar"
OUTPUT_DIR="bin"
VERSION_FILE="VERSION"
BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Read version from VERSION file
VERSION="unknown"
if [ -f "$VERSION_FILE" ]; then
  VERSION=$(cat "$VERSION_FILE" | tr -d '\n\r')
fi

echo -e "${BLUE}🔨 Building ori-agent${NC}"
echo -e "${CYAN}Version: $VERSION${NC}"
echo -e "${CYAN}Commit: $GIT_COMMIT${NC}"
echo -e "${CYAN}Build Date: $BUILD_DATE${NC}"
echo ""

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Build flags for version information
LDFLAGS="-X 'github.com/johnjallday/ori-agent/internal/version.Version=$VERSION' -X 'github.com/johnjallday/ori-agent/internal/version.GitCommit=$GIT_COMMIT' -X 'github.com/johnjallday/ori-agent/internal/version.BuildDate=$BUILD_DATE'"

# Check if we should build for specific OS/ARCH
TARGET_OS=${GOOS:-$(go env GOOS)}
TARGET_ARCH=${GOARCH:-$(go env GOARCH)}

echo -e "${YELLOW}Building for $TARGET_OS/$TARGET_ARCH...${NC}"

# Build the main server binary
echo -e "${YELLOW}Building server binary...${NC}"
if [ "$TARGET_OS" = "windows" ]; then
  BINARY_NAME="${BINARY_NAME}.exe"
fi

GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" go build \
  -ldflags "$LDFLAGS" \
  -o "$OUTPUT_DIR/$BINARY_NAME" \
  "./cmd/server"

if [ $? -eq 0 ]; then
  echo -e "${GREEN}✓ Server binary built successfully: $OUTPUT_DIR/$BINARY_NAME${NC}"
else
  echo -e "${RED}✗ Failed to build server binary${NC}"
  exit 1
fi

# Build the menu bar app (macOS only)
BUILD_MENUBAR=${BUILD_MENUBAR:-true}
if [ "$BUILD_MENUBAR" = "true" ] && [ "$TARGET_OS" = "darwin" ]; then
  echo -e "${YELLOW}Building menu bar app...${NC}"
  MENUBAR_BINARY="${MENUBAR_BINARY_NAME}"
  if [ "$TARGET_OS" = "windows" ]; then
    MENUBAR_BINARY="${MENUBAR_BINARY}.exe"
  fi

  GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" go build \
    -ldflags "$LDFLAGS" \
    -o "$OUTPUT_DIR/$MENUBAR_BINARY" \
    "./cmd/menubar"

  if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Menu bar app built successfully: $OUTPUT_DIR/$MENUBAR_BINARY${NC}"
  else
    echo -e "${RED}✗ Failed to build menu bar app${NC}"
    exit 1
  fi
elif [ "$BUILD_MENUBAR" = "true" ]; then
  echo -e "${YELLOW}Skipping menu bar app (only available on macOS)${NC}"
fi

# Build plugins if requested
BUILD_PLUGINS=${BUILD_PLUGINS:-true}
if [ "$BUILD_PLUGINS" = "true" ]; then
  echo -e "${YELLOW}Building plugins...${NC}"
  if [ -f "scripts/build-plugins.sh" ]; then
    ./scripts/build-plugins.sh
  else
    echo -e "${YELLOW}Plugin build script not found, skipping plugins${NC}"
  fi

fi

# Display build results
echo ""
echo -e "${GREEN}🎉 Build completed successfully!${NC}"
echo -e "${BLUE}Output files:${NC}"
ls -la "$OUTPUT_DIR/$BINARY_NAME"

if [ "$BUILD_MENUBAR" = "true" ] && [ "$TARGET_OS" = "darwin" ] && [ -f "$OUTPUT_DIR/$MENUBAR_BINARY_NAME" ]; then
  ls -la "$OUTPUT_DIR/$MENUBAR_BINARY_NAME"
fi

if [ -d "uploaded_plugins" ] && [ "$(ls -A uploaded_plugins 2>/dev/null)" ]; then
  echo -e "${BLUE}Plugin files:${NC}"
  ls -la uploaded_plugins/*.so 2>/dev/null || true
fi

echo ""
echo -e "${CYAN}To run the server (CLI):${NC}"
echo -e "${CYAN}  ./$OUTPUT_DIR/$BINARY_NAME${NC}"

if [ "$BUILD_MENUBAR" = "true" ] && [ "$TARGET_OS" = "darwin" ]; then
  echo ""
  echo -e "${CYAN}To run the menu bar app (macOS):${NC}"
  echo -e "${CYAN}  ./$OUTPUT_DIR/$MENUBAR_BINARY_NAME${NC}"
fi
echo ""
