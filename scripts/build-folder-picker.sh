#!/bin/bash

# Build script for the folder picker Wails app

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
FOLDER_PICKER_DIR="$PROJECT_ROOT/cmd/folder-picker"

# Add Go bin to PATH (where wails gets installed)
export PATH="$HOME/go/bin:$PATH"

echo "Building Ori Folder Picker..."

# Check if wails is installed
if ! command -v wails &> /dev/null; then
    echo "Wails CLI not found. Installing..."
    go install github.com/wailsapp/wails/v2/cmd/wails@latest
fi

cd "$FOLDER_PICKER_DIR"

# Build for current platform
echo "Building for current platform..."
wails build -clean

echo ""
echo "Build complete!"
echo "Binary location: $FOLDER_PICKER_DIR/build/bin/"

# Copy to project bin directory
mkdir -p "$PROJECT_ROOT/bin"

if [[ "$OSTYPE" == "darwin"* ]]; then
    # macOS - copy the .app bundle
    if [ -d "$FOLDER_PICKER_DIR/build/bin/ori-folder-picker.app" ]; then
        cp -r "$FOLDER_PICKER_DIR/build/bin/ori-folder-picker.app" "$PROJECT_ROOT/bin/"
        echo "Copied to: $PROJECT_ROOT/bin/ori-folder-picker.app"
    fi
elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
    # Linux
    if [ -f "$FOLDER_PICKER_DIR/build/bin/ori-folder-picker" ]; then
        cp "$FOLDER_PICKER_DIR/build/bin/ori-folder-picker" "$PROJECT_ROOT/bin/"
        echo "Copied to: $PROJECT_ROOT/bin/ori-folder-picker"
    fi
elif [[ "$OSTYPE" == "msys"* ]] || [[ "$OSTYPE" == "cygwin"* ]]; then
    # Windows
    if [ -f "$FOLDER_PICKER_DIR/build/bin/ori-folder-picker.exe" ]; then
        cp "$FOLDER_PICKER_DIR/build/bin/ori-folder-picker.exe" "$PROJECT_ROOT/bin/"
        echo "Copied to: $PROJECT_ROOT/bin/ori-folder-picker.exe"
    fi
fi

echo ""
echo "Done!"
