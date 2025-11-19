#!/bin/bash

# Generate menubar icons from SVG
# Creates different colored versions for different states

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ASSETS_DIR="$PROJECT_ROOT/assets"
ICONS_DIR="$PROJECT_ROOT/internal/menubar/icons"
# Use cropped version for menubar (better visibility)
SVG_FILE="$ASSETS_DIR/logo-menubar.svg"

# Colors for different states (hex)
COLOR_STOPPED="#AAAAAA"     # Light Gray
COLOR_STARTING="#FFA500"    # Orange
COLOR_RUNNING="#00FF00"     # Green
COLOR_STOPPING="#FFD700"    # Gold
COLOR_ERROR="#FF0000"       # Red

echo "🎨 Generating menubar icons from SVG..."
echo "Source: $SVG_FILE"
echo "Output: $ICONS_DIR"
echo ""

# Create icons directory if it doesn't exist
mkdir -p "$ICONS_DIR"

# Function to create colored SVG and convert to PNG
generate_icon() {
    local state=$1
    local color=$2
    local output_name=$3

    echo "  Creating $output_name (color: $color)..."

    # Create temporary colored SVG
    local temp_svg="/tmp/ori-icon-$state.svg"
    sed "s/fill=\"#000000\"/fill=\"$color\"/" "$SVG_FILE" > "$temp_svg"

    # Convert SVG to PNG using qlmanage (QuickLook) or rsvg-convert
    # Generate at 2x size (44x44) for better quality and visibility, macOS will scale appropriately
    if command -v rsvg-convert >/dev/null 2>&1; then
        # Use rsvg-convert if available (brew install librsvg)
        rsvg-convert -w 44 -h 44 "$temp_svg" -o "$ICONS_DIR/$output_name"
    elif command -v convert >/dev/null 2>&1; then
        # Use ImageMagick if available
        convert -background none -resize 22x22 "$temp_svg" "$ICONS_DIR/$output_name"
    else
        echo "  ⚠️  Warning: No SVG converter found (rsvg-convert or ImageMagick)"
        echo "  Installing librsvg..."
        if command -v brew >/dev/null 2>&1; then
            brew install librsvg
            rsvg-convert -w 44 -h 44 "$temp_svg" -o "$ICONS_DIR/$output_name"
        else
            echo "  ❌ Error: Homebrew not found. Please install librsvg manually:"
            echo "     brew install librsvg"
            exit 1
        fi
    fi

    # Clean up temp file
    rm "$temp_svg"

    # Verify the output
    if [ -f "$ICONS_DIR/$output_name" ]; then
        echo "  ✓ Generated $output_name"
    else
        echo "  ✗ Failed to generate $output_name"
        return 1
    fi
}

# Generate all state icons
generate_icon "stopped" "$COLOR_STOPPED" "icon.png"
generate_icon "starting" "$COLOR_STARTING" "icon-starting.png"
generate_icon "running" "$COLOR_RUNNING" "icon-running.png"
generate_icon "stopping" "$COLOR_STOPPING" "icon-stopping.png"
generate_icon "error" "$COLOR_ERROR" "icon-error.png"

echo ""
echo "✅ All menubar icons generated successfully!"
echo ""
echo "Generated icons:"
ls -lh "$ICONS_DIR"/*.png
