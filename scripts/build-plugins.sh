#!/bin/bash

# Build all plugins script for dolphin-agent
# This script rebuilds all plugins in the plugins/ directory

set -e # Exit on any error

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}Building all plugins...${NC}"

# Create uploaded_plugins directory if it doesn't exist
mkdir -p uploaded_plugins

# Build each plugin
build_plugin() {
  local plugin_dir="$1"
  local source_file="$2"
  local output_name="$3"

  echo -e "${YELLOW}Building plugin: $plugin_dir${NC}"

  if [ ! -f "plugins/$plugin_dir/$source_file" ]; then
    echo -e "${RED}Error: Source file plugins/$plugin_dir/$source_file not found${NC}"
    return 1
  fi

  cd "plugins/$plugin_dir"

  if go build -buildmode=plugin -o "../../uploaded_plugins/$output_name.so" "$source_file"; then
    echo -e "${GREEN}✓ Successfully built $output_name.so${NC}"
  else
    echo -e "${RED}✗ Failed to build $plugin_dir${NC}"
    cd "$PROJECT_ROOT"
    return 1
  fi

  cd "$PROJECT_ROOT"
}

# Build individual plugins
build_plugin "weather" "weather.go" "weather"
build_plugin "math" "math.go" "math"
build_plugin "result-handler" "main.go" "result-handler"
#build_plugin "music_project_manager" "music_project_manager.go" "music_project_manager"

echo -e "${GREEN}All plugins built successfully!${NC}"
echo -e "${BLUE}Plugin binaries are in: uploaded_plugins/${NC}"
ls -la uploaded_plugins/*.so
