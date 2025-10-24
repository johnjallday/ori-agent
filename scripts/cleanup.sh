#!/bin/bash

# Cleanup script for ori-agent
# Removes plugin cache, agents directory, agents.json, plugin registry cache, and uploaded plugins
# Run from scripts/ directory - operates on parent directory

# Get the script directory and parent directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "🧹 Cleaning up ori-agent..."
echo "   Working in: $PROJECT_DIR"

cd "$PROJECT_DIR" || exit 1

# Remove plugin_cache directory
if [ -d "plugin_cache" ]; then
    echo "  ❌ Removing plugin_cache directory..."
    rm -rf plugin_cache
    echo "     ✅ plugin_cache deleted"
else
    echo "  ℹ️  plugin_cache directory not found"
fi

# Remove agents directory  
if [ -d "agents" ]; then
    echo "  ❌ Removing agents directory..."
    rm -rf agents
    echo "     ✅ agents directory deleted"
else
    echo "  ℹ️  agents directory not found"
fi

# Remove agents.json file
if [ -f "agents.json" ]; then
    echo "  ❌ Removing agents.json file..."
    rm -f agents.json
    echo "     ✅ agents.json deleted"
else
    echo "  ℹ️  agents.json file not found"
fi

# Remove plugin_registry_cache.json file
if [ -f "plugin_registry_cache.json" ]; then
    echo "  ❌ Removing plugin_registry_cache.json file..."
    rm -f plugin_registry_cache.json
    echo "     ✅ plugin_registry_cache.json deleted"
else
    echo "  ℹ️  plugin_registry_cache.json file not found"
fi

# Remove uploaded_plugins directory
if [ -d "uploaded_plugins" ]; then
    echo "  ❌ Removing uploaded_plugins directory..."
    rm -rf uploaded_plugins
    echo "     ✅ uploaded_plugins directory deleted"
else
    echo "  ℹ️  uploaded_plugins directory not found"
fi

echo "🎉 Cleanup complete!"