#!/bin/bash
# Automated DMG creator for GoReleaser
# This script is called by GoReleaser to create macOS DMG installers
# Usage: ./build/macos/create-dmg.sh <version> <architecture> <dist-dir>

set -e

VERSION=$1
ARCH=$2
DIST_DIR=${3:-"dist"}

if [ -z "$VERSION" ] || [ -z "$ARCH" ]; then
    echo "Usage: $0 <version> <architecture> <dist-dir>"
    echo "Example: $0 0.0.11 amd64 dist"
    exit 1
fi

# Check if this is a macOS build by looking for the menubar binary
# GoReleaser publishers run for all archives, so we need to skip non-macOS ones
MENUBAR_CHECK=$(find "${DIST_DIR}" -path "*/menubar_darwin_${ARCH}*/ori-menubar" -type f 2>/dev/null | head -1)
if [ -z "$MENUBAR_CHECK" ]; then
    echo "ℹ️  Skipping DMG creation - not a macOS build (no menubar binary found)"
    exit 0
fi

APP_NAME="Ori Agent"
APP_BUNDLE="OriAgent.app"
DMG_NAME="OriAgent-${VERSION}-${ARCH}.dmg"
BUNDLE_ID="com.ori.ori-agent"
BUILD_DIR="build-dmg-${ARCH}"

echo "🚀 Creating macOS DMG for ${APP_NAME} v${VERSION} (${ARCH})"
echo "=========================================================="

# Clean up previous builds
echo "🧹 Cleaning up..."
rm -rf "${BUILD_DIR}"
rm -f "${DIST_DIR}/${DMG_NAME}"  # Remove any existing DMG
mkdir -p "${BUILD_DIR}"
mkdir -p "${DIST_DIR}"

# Step 1: Create .app bundle structure
echo ""
echo "📦 Creating .app bundle..."
APP_PATH="${BUILD_DIR}/${APP_BUNDLE}"
mkdir -p "${APP_PATH}/Contents/MacOS"
mkdir -p "${APP_PATH}/Contents/Resources"

# Create the launcher script
cat >"${APP_PATH}/Contents/MacOS/OriAgent" <<'LAUNCHER'
#!/bin/bash

# Get the directory where the app is located
APP_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
RESOURCES_DIR="$APP_DIR/Contents/Resources"

# Use proper macOS data directory
DATA_DIR="$HOME/Library/Application Support/OriAgent"
mkdir -p "$DATA_DIR"
mkdir -p "$HOME/Library/Logs"

cd "$DATA_DIR"

# Launch the menu bar app
# The menu bar app provides a UI to start/stop the server
# Note: We don't redirect stdout/stderr here because systray apps need direct terminal access
exec "$RESOURCES_DIR/ori-menubar"
LAUNCHER

chmod +x "${APP_PATH}/Contents/MacOS/OriAgent"

# Create Info.plist
cat >"${APP_PATH}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>OriAgent</string>
    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>
    <key>CFBundleName</key>
    <string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key>
    <string>${APP_NAME}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>LSMinimumSystemVersion</key>
    <string>10.15</string>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>LSUIElement</key>
    <true/>
    <key>CFBundleIconFile</key>
    <string>AppIcon</string>
</dict>
</plist>
PLIST

# Copy app icon
echo "🎨 Copying app icon..."
ICON_PATH="assets/AppIcon.icns"

# Ensure Resources directory exists
mkdir -p "${APP_PATH}/Contents/Resources"

if [ -f "$ICON_PATH" ]; then
    if cp "$ICON_PATH" "${APP_PATH}/Contents/Resources/"; then
        echo "  ✓ Copied icon from: $ICON_PATH"
    else
        echo "  ❌ Failed to copy icon (non-fatal, continuing without icon)"
    fi
else
    echo "  ⚠️  Warning: AppIcon.icns not found at $ICON_PATH (continuing without icon)"
fi

# Copy binaries from dist directory
echo "📦 Copying binaries..."

# Ensure Resources directory exists before copying binaries
mkdir -p "${APP_PATH}/Contents/Resources"

# GoReleaser creates directories with version suffixes like _v1 or _v8.0
# Find the menubar binary
MENUBAR_PATH=$(find "${DIST_DIR}" -path "*/menubar_darwin_${ARCH}*/ori-menubar" -type f | head -1)
if [ -f "$MENUBAR_PATH" ]; then
    if cp "$MENUBAR_PATH" "${APP_PATH}/Contents/Resources/"; then
        echo "  ✓ Copied menubar from: $MENUBAR_PATH"
    else
        echo "❌ Error: Failed to copy ori-menubar"
        exit 1
    fi
else
    echo "❌ Error: ori-menubar binary not found for architecture ${ARCH}"
    echo "  Searched in: ${DIST_DIR}/menubar_darwin_${ARCH}*/"
    exit 1
fi

# Find the server binary
SERVER_PATH=$(find "${DIST_DIR}" -path "*/server_darwin_${ARCH}*/ori-agent" -type f | head -1)
if [ -f "$SERVER_PATH" ]; then
    if cp "$SERVER_PATH" "${APP_PATH}/Contents/Resources/"; then
        echo "  ✓ Copied server from: $SERVER_PATH"
    else
        echo "❌ Error: Failed to copy ori-agent"
        ls -la "${APP_PATH}/Contents/Resources/" || true
        exit 1
    fi
else
    echo "❌ Error: ori-agent binary not found for architecture ${ARCH}"
    echo "  Searched in: ${DIST_DIR}/server_darwin_${ARCH}*/"
    exit 1
fi

echo "✅ .app bundle created"

# Verify binaries exist in .app bundle before proceeding
echo ""
echo "🔍 Verifying .app bundle contents..."
if [ ! -f "${APP_PATH}/Contents/Resources/ori-menubar" ]; then
    echo "❌ Error: ori-menubar not found in .app bundle"
    ls -la "${APP_PATH}/Contents/Resources/" || true
    exit 1
fi
if [ ! -f "${APP_PATH}/Contents/Resources/ori-agent" ]; then
    echo "❌ Error: ori-agent not found in .app bundle"
    ls -la "${APP_PATH}/Contents/Resources/" || true
    exit 1
fi
echo "✓ All binaries present in .app bundle"

# Step 2: Create DMG staging area
echo ""
echo "🎨 Preparing DMG contents..."
DMG_STAGING="${BUILD_DIR}/dmg-staging"
mkdir -p "${DMG_STAGING}"

# Copy app to staging (use ditto for better macOS compatibility)
echo "📋 Copying .app to staging area..."
ditto "${APP_PATH}" "${DMG_STAGING}/OriAgent.app"

# Create Applications symlink (remove if exists)
rm -f "${DMG_STAGING}/Applications"
ln -s /Applications "${DMG_STAGING}/Applications"

# Create README
cat >"${DMG_STAGING}/README.txt" <<'README'
Ori Agent Installation
======================

To install:
1. Drag "OriAgent.app" to the Applications folder
2. Double-click "Ori Agent" in Applications to start
3. A menu bar icon will appear - click it to start the server

Features:
• Menu bar app with Start/Stop server controls
• Auto-start on login option
• Quick access to open browser
• Visual server status indicators

The server can be accessed at:
http://localhost:8765

Logs are stored at:
~/Library/Logs/ori-menubar.log
~/Library/Logs/ori-agent.log

Command Line (Advanced):
For advanced users, a CLI version is also available:
/Applications/OriAgent.app/Contents/Resources/ori-agent

For more information:
https://github.com/johnjallday/ori-agent
README

echo "✅ DMG staging area ready"

# Step 3: Create DMG
echo ""
echo "💿 Creating compressed DMG image..."

# Create compressed DMG
rm -f "${DIST_DIR}/${DMG_NAME}"
hdiutil create \
  -srcfolder "${DMG_STAGING}" \
  -volname "${APP_NAME} ${VERSION}" \
  -fs HFS+ \
  -fsargs "-c c=64,a=16,e=16" \
  -format UDZO \
  -imagekey zlib-level=9 \
  "${DIST_DIR}/${DMG_NAME}"

# Clean up
rm -rf "${BUILD_DIR}"

echo ""
echo "=========================================="
echo "✅ DMG created successfully!"
echo "=========================================="
echo ""
echo "📍 Location: ${DIST_DIR}/${DMG_NAME}"
ls -lh "${DIST_DIR}/${DMG_NAME}"
echo ""
echo "📊 SHA-256 Checksum:"
shasum -a 256 "${DIST_DIR}/${DMG_NAME}"
echo ""
