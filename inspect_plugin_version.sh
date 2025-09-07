#!/bin/bash

# Plugin Version Inspector
# Extracts embedded version information from .so plugin files

if [ $# -eq 0 ]; then
    echo "Usage: $0 <plugin.so> [plugin2.so ...]"
    echo "Example: $0 uploaded_plugins/music_project_manager.so"
    exit 1
fi

for plugin in "$@"; do
    if [ ! -f "$plugin" ]; then
        echo "❌ File not found: $plugin"
        continue
    fi
    
    echo "🔍 Inspecting: $plugin"
    echo "----------------------------------------"
    
    # Extract version info using strings command
    version=$(strings "$plugin" | grep -E "^[0-9]+\.[0-9]+\.[0-9]+" | head -1)
    build_time=$(strings "$plugin" | grep -E "^[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{2}:[0-9]{2}:[0-9]{2}_UTC$" | head -1)
    git_commit=$(strings "$plugin" | grep -E "^[a-f0-9]{7}$" | head -1)
    
    # Display results
    if [ -n "$version" ]; then
        echo "📦 Version: $version"
    else
        echo "📦 Version: Not found"
    fi
    
    if [ -n "$build_time" ]; then
        echo "⏰ Build Time: $build_time"
    else
        echo "⏰ Build Time: Not found"
    fi
    
    if [ -n "$git_commit" ]; then
        echo "🔗 Git Commit: $git_commit"
    else
        echo "🔗 Git Commit: Not found"
    fi
    
    # Show file info
    file_size=$(ls -lh "$plugin" | awk '{print $5}')
    file_date=$(ls -l "$plugin" | awk '{print $6, $7, $8}')
    echo "📂 File Size: $file_size"
    echo "📅 File Date: $file_date"
    
    echo ""
done

echo "✅ Inspection completed!"