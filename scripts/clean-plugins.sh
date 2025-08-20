#!/bin/bash

# Clean plugin binaries script for dolphin-agent
# Removes all compiled plugin .so files

set -e  # Exit on any error

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}Cleaning plugin binaries...${NC}"

# Clean uploaded_plugins directory
if [ -d "uploaded_plugins" ]; then
    echo -e "${YELLOW}Removing files from uploaded_plugins/...${NC}"
    rm -f uploaded_plugins/*.so
    echo -e "${GREEN}✓ Cleaned uploaded_plugins directory${NC}"
else
    echo -e "${YELLOW}uploaded_plugins directory does not exist${NC}"
fi

# Clean .so files from individual plugin directories
for plugin_dir in plugins/*/; do
    if [ -d "$plugin_dir" ]; then
        plugin_name=$(basename "$plugin_dir")
        echo -e "${YELLOW}Checking $plugin_name for .so files...${NC}"
        
        if find "$plugin_dir" -name "*.so" -delete -print | grep -q .; then
            echo -e "${GREEN}✓ Cleaned .so files from $plugin_name${NC}"
        fi
    fi
done

echo -e "${GREEN}Plugin cleanup complete!${NC}"