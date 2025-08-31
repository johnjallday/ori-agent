#!/bin/bash

# Build single plugin script for dolphin-agent
# Usage: ./build-plugin.sh <plugin-name>
# Example: ./build-plugin.sh weather

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

if [ $# -eq 0 ]; then
  echo -e "${RED}Error: Plugin name required${NC}"
  echo "Usage: $0 <plugin-name>"
  echo "Available plugins:"
  find plugins -maxdepth 1 -type d ! -path plugins | sed 's|plugins/||' | sort
  exit 1
fi

PLUGIN_NAME="$1"
PLUGIN_DIR="plugins/$PLUGIN_NAME"

if [ ! -d "$PLUGIN_DIR" ]; then
  echo -e "${RED}Error: Plugin directory '$PLUGIN_DIR' not found${NC}"
  echo "Available plugins:"
  find plugins -maxdepth 1 -type d ! -path plugins | sed 's|plugins/||' | sort
  exit 1
fi

echo -e "${BLUE}Building plugin: $PLUGIN_NAME${NC}"

# Create uploaded_plugins directory if it doesn't exist
mkdir -p uploaded_plugins

# Determine source file
SOURCE_FILE=""
if [ -f "$PLUGIN_DIR/main.go" ]; then
  SOURCE_FILE="main.go"
elif [ -f "$PLUGIN_DIR/$PLUGIN_NAME.go" ]; then
  SOURCE_FILE="$PLUGIN_NAME.go"
else
  echo -e "${RED}Error: Could not find Go source file in $PLUGIN_DIR${NC}"
  echo "Looking for main.go or $PLUGIN_NAME.go"
  exit 1
fi

echo -e "${YELLOW}Source file: $SOURCE_FILE${NC}"

cd "$PLUGIN_DIR"

if go build -buildmode=plugin -o "../../uploaded_plugins/$PLUGIN_NAME.so" "$SOURCE_FILE"; then
  echo -e "${GREEN}✓ Successfully built $PLUGIN_NAME.so${NC}"
  echo -e "${BLUE}Plugin binary: uploaded_plugins/$PLUGIN_NAME.so${NC}"
  ls -la "../../uploaded_plugins/$PLUGIN_NAME.so"
else
  echo -e "${RED}✗ Failed to build $PLUGIN_NAME${NC}"
  exit 1
fi

