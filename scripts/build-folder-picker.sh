#!/bin/bash

# Build script for the folder picker Wails app

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
FOLDER_PICKER_DIR="$PROJECT_ROOT/cmd/folder-picker"
WAILS_BINDING_DIR="$FOLDER_PICKER_DIR/frontend/wailsjs"

# Wails regenerates its plain-text JS/TS bindings as executable files. Normalize
# every generated binding when this script exits, including after a failed build,
# so worktree creation and local packaging never leave mode-only Git changes.
normalize_wails_binding_modes() {
    local status=$?
    trap - EXIT

    if [[ -d "$WAILS_BINDING_DIR" ]] &&
        ! find "$WAILS_BINDING_DIR" -type f -exec chmod 0644 {} +; then
        echo "Failed to normalize generated Wails binding modes" >&2
        [[ "$status" -ne 0 ]] || status=1
    fi

    exit "$status"
}
trap normalize_wails_binding_modes EXIT

# Add Go bin to PATH (where wails gets installed)
export PATH="$HOME/go/bin:$PATH"

echo "Building Ori Folder Picker..."

# Keep the Wails CLI on the version go.mod pins. Dependabot bumps go.mod, and
# `wails build` warns ("go.mod is using Wails X but the CLI is Y") when the
# installed CLI is older. go.mod is the source of truth rather than @latest, so
# every machine and CI run builds with the same toolchain the code compiles
# against. Installing only when the CLI was missing left it stuck on whatever
# version happened to be latest the day it was first installed.
WAILS_MODULE="github.com/wailsapp/wails/v2"
wanted_wails_version="$(awk -v module="$WAILS_MODULE" \
    '$1 == module { print $2; exit }' "$PROJECT_ROOT/go.mod" 2>/dev/null || true)"
installed_wails_version=""
if command -v wails &> /dev/null; then
    installed_wails_version="$(wails version 2>/dev/null |
        grep -m1 -Eo 'v[0-9]+\.[0-9]+\.[0-9]+[^[:space:]]*' || true)"
fi

if [[ -z "$installed_wails_version" ]] ||
    { [[ -n "$wanted_wails_version" ]] && [[ "$installed_wails_version" != "$wanted_wails_version" ]]; }; then
    install_version="${wanted_wails_version:-latest}"
    echo "Wails CLI ${installed_wails_version:-not found}; installing ${install_version} (go.mod pin)..."
    if ! go install "$WAILS_MODULE/cmd/wails@$install_version"; then
        if [[ -z "$installed_wails_version" ]]; then
            echo "Could not install the Wails CLI" >&2
            exit 1
        fi
        # An older CLI still builds (with the warning), so a failed update such
        # as an offline machine must not block worktree creation.
        echo "Warning: could not update Wails CLI; continuing with ${installed_wails_version}" >&2
    fi
fi

cd "$FOLDER_PICKER_DIR"

# Build for current platform
echo "Building for current platform..."
WAILS_PLATFORM_FLAG=""
WAILS_TAGS_FLAG=""

if [[ "$OSTYPE" == "darwin"* ]]; then
    # Build a universal binary for macOS so DMGs work on Intel + Apple Silicon
    WAILS_PLATFORM_FLAG="--platform darwin/universal"
elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
    # Check if webkit2gtk-4.1 is installed (Ubuntu 22.04+, Fedora 38+)
    # If so, we need to use the webkit2_41 build tag
    if pkg-config --exists webkit2gtk-4.1 2>/dev/null; then
        echo "Detected webkit2gtk-4.1, using webkit2_41 build tag..."
        WAILS_TAGS_FLAG="-tags webkit2_41"
    elif pkg-config --exists webkit2gtk-4.0 2>/dev/null; then
        echo "Detected webkit2gtk-4.0"
    else
        echo "Warning: No webkit2gtk package found, build may fail"
    fi
elif [[ "$OSTYPE" == "msys"* ]] || [[ "$OSTYPE" == "cygwin"* ]]; then
    # Default to amd64 on Windows unless overridden
    WAILS_PLATFORM_FLAG="--platform windows/amd64"
fi

if [ -n "${WAILS_PLATFORM:-}" ]; then
    WAILS_PLATFORM_FLAG="--platform ${WAILS_PLATFORM}"
fi

wails build -clean $WAILS_PLATFORM_FLAG $WAILS_TAGS_FLAG

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
