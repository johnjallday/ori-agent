#!/bin/bash

# Generate macOS app icon (.icns) from SVG
# Creates all required sizes for macOS app bundle

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ASSETS_DIR="$PROJECT_ROOT/assets"
SVG_FILE="$ASSETS_DIR/logo.svg"
ICON_NAME="AppIcon"

echo "🎨 Generating macOS app icon from SVG..."
echo "Source: $SVG_FILE"
echo ""

# Check for required tools
if ! command -v rsvg-convert >/dev/null 2>&1; then
    echo "❌ Error: rsvg-convert not found"
    echo "Install with: brew install librsvg"
    exit 1
fi

if ! command -v iconutil >/dev/null 2>&1; then
    echo "❌ Error: iconutil not found (macOS only)"
    exit 1
fi

# Create temporary iconset directory
ICONSET_DIR="/tmp/${ICON_NAME}.iconset"
rm -rf "$ICONSET_DIR"
mkdir -p "$ICONSET_DIR"

echo "📐 Generating icon sizes..."

# macOS app icon sizes (standard and retina)
# Format: icon_SIZExSIZE.png and icon_SIZExSIZE@2x.png
SIZES=(16 32 128 256 512)

for SIZE in "${SIZES[@]}"; do
    # Standard resolution
    echo "  Creating ${SIZE}x${SIZE}..."
    rsvg-convert -w $SIZE -h $SIZE "$SVG_FILE" -o "$ICONSET_DIR/icon_${SIZE}x${SIZE}.png"

    # Retina resolution (@2x)
    RETINA_SIZE=$((SIZE * 2))
    echo "  Creating ${SIZE}x${SIZE}@2x (${RETINA_SIZE}x${RETINA_SIZE})..."
    rsvg-convert -w $RETINA_SIZE -h $RETINA_SIZE "$SVG_FILE" -o "$ICONSET_DIR/icon_${SIZE}x${SIZE}@2x.png"
done

echo ""
echo "🔨 Converting iconset to .icns..."

# Convert iconset to .icns
iconutil -c icns "$ICONSET_DIR" -o "$ASSETS_DIR/${ICON_NAME}.icns"

# Clean up
rm -rf "$ICONSET_DIR"

echo ""
echo "✅ App icon generated successfully!"
echo "📦 Output: $ASSETS_DIR/${ICON_NAME}.icns"
echo ""

# Verify the output
if [ -f "$ASSETS_DIR/${ICON_NAME}.icns" ]; then
    ls -lh "$ASSETS_DIR/${ICON_NAME}.icns"
else
    echo "❌ Error: Failed to generate .icns file"
    exit 1
fi
