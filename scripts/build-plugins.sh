#!/bin/bash

# Build all plugins script for ori-agent
# This script rebuilds all plugins in the example_plugins/ directory as RPC executables

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

echo -e "${BLUE}Building all plugins as RPC executables...${NC}"

# Check if building for specific OS/ARCH
TARGET_OS=${GOOS:-$(go env GOOS)}
TARGET_ARCH=${GOARCH:-$(go env GOARCH)}
echo -e "${CYAN}Target platform: $TARGET_OS/$TARGET_ARCH${NC}"

# Build each plugin as RPC executable
build_plugin() {
  local plugin_dir="$1"
  local source_file="$2"
  local output_name="$3"

  echo -e "${YELLOW}Building RPC plugin: $plugin_dir${NC}"

  if [ ! -f "example_plugins/$plugin_dir/$source_file" ]; then
    echo -e "${RED}Error: Source file example_plugins/$plugin_dir/$source_file not found${NC}"
    return 1
  fi

  # Create uploaded_plugins directory if it doesn't exist
  mkdir -p uploaded_plugins

  local output_path="uploaded_plugins/$output_name"

  # Add .exe extension for Windows
  if [ "$TARGET_OS" = "windows" ]; then
    output_path="${output_path}.exe"
  fi

  # Build as regular executable (not -buildmode=plugin)
  if GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" go build -o "$output_path" "example_plugins/$plugin_dir/$source_file"; then
    echo -e "${GREEN}✓ Successfully built $output_name -> $output_path${NC}"
  else
    echo -e "${RED}✗ Failed to build $plugin_dir${NC}"
    return 1
  fi
}

# Build individual plugins
build_plugin "weather" "weather.go" "weather"
build_plugin "math" "math.go" "math"
build_plugin "result-handler" "main.go" "result-handler"

echo ""
echo -e "${GREEN}All plugins built successfully!${NC}"
echo -e "${BLUE}Plugin executables in uploaded_plugins/:${NC}"
ls -lh uploaded_plugins/weather uploaded_plugins/math uploaded_plugins/result-handler 2>/dev/null || true
echo ""
