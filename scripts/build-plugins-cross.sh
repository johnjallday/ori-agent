#!/bin/bash

# Cross-platform build script for dolphin-agent plugins
# Builds RPC plugin executables for Windows, Linux, and macOS

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

echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
echo -e "${BLUE}   Cross-Platform Plugin Build${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
echo ""

# Output directory for cross-compiled plugins
OUTPUT_DIR="bin/plugins"
mkdir -p "$OUTPUT_DIR"

# Define platforms
PLATFORMS=(
  "darwin/amd64"
  "darwin/arm64"
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
)

# Plugins to build
PLUGINS=(
  "weather:weather.go"
  "math:math.go"
  "result-handler:main.go"
)

# Build for each platform
for platform in "${PLATFORMS[@]}"; do
  IFS='/' read -r -a platform_parts <<< "$platform"
  GOOS="${platform_parts[0]}"
  GOARCH="${platform_parts[1]}"

  echo -e "${CYAN}Building for $GOOS/$GOARCH...${NC}"

  PLATFORM_DIR="$OUTPUT_DIR/$GOOS-$GOARCH"
  mkdir -p "$PLATFORM_DIR"

  # Build each plugin
  for plugin_spec in "${PLUGINS[@]}"; do
    IFS=':' read -r -a plugin_parts <<< "$plugin_spec"
    PLUGIN_NAME="${plugin_parts[0]}"
    SOURCE_FILE="${plugin_parts[1]}"

    OUTPUT_NAME="$PLUGIN_NAME"
    if [ "$GOOS" = "windows" ]; then
      OUTPUT_NAME="${PLUGIN_NAME}.exe"
    fi

    echo -e "${YELLOW}  - Building $PLUGIN_NAME...${NC}"

    if GOOS="$GOOS" GOARCH="$GOARCH" go build \
       -o "$PLATFORM_DIR/$OUTPUT_NAME" \
       "plugins/$PLUGIN_NAME/$SOURCE_FILE"; then

      SIZE=$(ls -lh "$PLATFORM_DIR/$OUTPUT_NAME" | awk '{print $5}')
      echo -e "${GREEN}    ✓ $OUTPUT_NAME ($SIZE)${NC}"
    else
      echo -e "${RED}    ✗ Failed to build $PLUGIN_NAME for $GOOS/$GOARCH${NC}"
    fi
  done

  echo ""
done

echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
echo -e "${GREEN}   Cross-platform build complete!${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
echo ""
echo -e "${BLUE}Plugin binaries are in: $OUTPUT_DIR/${NC}"
echo ""

# Show directory structure
tree "$OUTPUT_DIR" 2>/dev/null || find "$OUTPUT_DIR" -type f | sort
