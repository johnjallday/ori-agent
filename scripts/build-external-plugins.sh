#!/bin/bash

# Build external plugins script for dolphin-agent
# Builds plugins that are in separate repositories/directories

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

echo -e "${BLUE}Building external plugins...${NC}"

# Create uploaded_plugins directory if it doesn't exist
mkdir -p uploaded_plugins

# Build external plugins
build_external_plugin() {
    local plugin_path="$1"
    local plugin_name="$2"
    
    if [ ! -d "$plugin_path" ]; then
        echo -e "${YELLOW}⚠ Skipping $plugin_name: directory not found at $plugin_path${NC}"
        return 0
    fi
    
    echo -e "${YELLOW}Building external plugin: $plugin_name${NC}"
    
    if [ -f "$plugin_path/build.sh" ]; then
        # Use the plugin's own build script
        cd "$plugin_path"
        if ./build.sh; then
            echo -e "${GREEN}✓ Successfully built $plugin_name using build.sh${NC}"
        else
            echo -e "${RED}✗ Failed to build $plugin_name${NC}"
            cd "$PROJECT_ROOT"
            return 1
        fi
        cd "$PROJECT_ROOT"
    elif [ -f "$plugin_path/main.go" ]; then
        # Build directly if main.go exists
        cd "$plugin_path"
        local output_name="${plugin_name}.so"
        if go build -buildmode=plugin -o "$output_name" main.go; then
            echo -e "${GREEN}✓ Successfully built $plugin_name${NC}"
            # Copy to uploaded_plugins
            cp "$output_name" "$PROJECT_ROOT/uploaded_plugins/"
        else
            echo -e "${RED}✗ Failed to build $plugin_name${NC}"
            cd "$PROJECT_ROOT"
            return 1
        fi
        cd "$PROJECT_ROOT"
    else
        echo -e "${YELLOW}⚠ Skipping $plugin_name: no build.sh or main.go found${NC}"
        return 0
    fi
}

# List of external plugins to build
# Format: "relative_path:plugin_name"
EXTERNAL_PLUGINS=(
    "../dolphin-reaper:dolphin-reaper"
)

# Build each external plugin
built_count=0
for plugin_spec in "${EXTERNAL_PLUGINS[@]}"; do
    IFS=':' read -r plugin_path plugin_name <<< "$plugin_spec"
    if build_external_plugin "$plugin_path" "$plugin_name"; then
        built_count=$((built_count + 1))
    fi
done

echo ""
if [ $built_count -gt 0 ]; then
    echo -e "${GREEN}✓ Built $built_count external plugin(s) successfully!${NC}"
    echo -e "${BLUE}External plugin binaries are in: uploaded_plugins/${NC}"
    ls -la uploaded_plugins/*.so 2>/dev/null | grep -E "(reascript_launcher|dolphin-)" || true
else
    echo -e "${YELLOW}No external plugins were built${NC}"
fi