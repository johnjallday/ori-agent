#!/bin/bash

# Clean stale plugin paths from agents.json
# This script fixes issues where agents reference old temporary plugin paths

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

echo -e "${BLUE}🧹 Cleaning stale plugin paths from agents.json...${NC}"

# Backup agents.json
if [ -f "agents.json" ]; then
    cp agents.json agents.json.backup
    echo -e "${YELLOW}📋 Created backup: agents.json.backup${NC}"
fi

# Check for stale paths (temp directories, non-existent files)
AGENTS_FILE="agents.json"
TEMP_FILE=$(mktemp)

if [ -f "$AGENTS_FILE" ]; then
    # Look for stale temporary paths
    STALE_COUNT=$(grep -c "/var/folders.*\.so\|/tmp.*\.so\|/temp.*\.so" "$AGENTS_FILE" 2>/dev/null || echo "0")
    
    if [ "$STALE_COUNT" -gt 0 ]; then
        echo -e "${YELLOW}⚠️ Found $STALE_COUNT stale temporary plugin path(s)${NC}"
        echo -e "${BLUE}Stale paths found:${NC}"
        grep "/var/folders.*\.so\|/tmp.*\.so\|/temp.*\.so" "$AGENTS_FILE" || true
        
        echo -e "${YELLOW}💡 Manual fix required: Update paths in agents.json to point to uploaded_plugins/${NC}"
        echo -e "${YELLOW}Example: Change '/var/folders/.../plugin.so' to '/path/to/project/uploaded_plugins/plugin.so'${NC}"
    else
        echo -e "${GREEN}✅ No stale temporary paths found${NC}"
    fi
    
    # Check for non-existent plugin files
    echo -e "${BLUE}🔍 Checking for non-existent plugin files...${NC}"
    NON_EXISTENT=0
    
    # Extract plugin paths from agents.json and check if they exist
    while read -r plugin_path; do
        if [ -n "$plugin_path" ] && [ ! -f "$plugin_path" ]; then
            echo -e "${RED}❌ Missing: $plugin_path${NC}"
            NON_EXISTENT=$((NON_EXISTENT + 1))
        fi
    done < <(grep '"Path":' "$AGENTS_FILE" | sed 's/.*"Path": *"\([^"]*\)".*/\1/')
    
    if [ "$NON_EXISTENT" -eq 0 ]; then
        echo -e "${GREEN}✅ All plugin files exist${NC}"
    fi
    
else
    echo -e "${YELLOW}⚠️ agents.json not found${NC}"
fi

# Check available plugins
echo -e "${BLUE}📦 Available plugins in uploaded_plugins/:${NC}"
if [ -d "uploaded_plugins" ]; then
    ls -la uploaded_plugins/*.so 2>/dev/null || echo -e "${YELLOW}No .so files found in uploaded_plugins/${NC}"
else
    echo -e "${YELLOW}uploaded_plugins/ directory not found${NC}"
fi

echo -e "${GREEN}🎉 Cleanup check completed!${NC}"
echo -e "${BLUE}💡 Tip: If you found issues, edit agents.json manually or regenerate it by loading plugins through the web interface${NC}"