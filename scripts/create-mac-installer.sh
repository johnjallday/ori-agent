#!/bin/bash

# Complete Mac Installer Creator for Ori Agent
# Creates a .app bundle and packages it in a professional DMG
# Usage: ./scripts/create-mac-installer.sh [version]

set -e

# Configuration
VERSION=${1:-"0.0.7"}
APP_NAME="Ori Agent"
APP_BUNDLE="OriAgent.app"
DMG_NAME="OriAgent-${VERSION}.dmg"
BUNDLE_ID="com.ori.ori-agent"
BUILD_DIR="build-dmg"

echo "🚀 Creating Mac Installer for ${APP_NAME} v${VERSION}"
echo "================================================"

# Clean up previous builds
echo "🧹 Cleaning up previous builds..."
rm -rf "${BUILD_DIR}"
rm -f temp-rw.dmg
mkdir -p "${BUILD_DIR}"
mkdir -p releases

# Step 1: Build the binary
echo ""
echo "🔨 Building ori-agent binary..."
go build -ldflags="-s -w" -o "${BUILD_DIR}/ori-agent" ./cmd/server
chmod +x "${BUILD_DIR}/ori-agent"
echo "✅ Binary built successfully"

# Step 2: Create .app bundle structure
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

# Check if already running
if lsof -Pi :8080 -sTCP:LISTEN -t >/dev/null 2>&1 ; then
    osascript -e 'display notification "Ori Agent is already running" with title "Ori Agent"'
    open http://localhost:8080
    exit 0
fi

# Start server in background
"$RESOURCES_DIR/ori-agent" > ~/Library/Logs/ori-agent.log 2>&1 &
SERVER_PID=$!

# Wait for server to start
for i in {1..10}; do
    if lsof -Pi :8080 -sTCP:LISTEN -t >/dev/null 2>&1; then
        sleep 1
        open http://localhost:8080
        osascript -e 'display notification "Server started on port 8080" with title "Ori Agent"'
        exit 0
    fi
    sleep 0.5
done

# Failed to start
osascript -e 'display alert "Ori Agent" message "Failed to start server. Check logs at ~/Library/Logs/ori-agent.log" as critical'
exit 1
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
    <false/>
</dict>
</plist>
PLIST

# Copy resources
cp "${BUILD_DIR}/ori-agent" "${APP_PATH}/Contents/Resources/"

# Note: Plugins (example_plugins and uploaded_plugins) are excluded from DMG
# Users will add their own plugins after installation

echo "✅ .app bundle created"

# Step 3: Create DMG staging area
echo ""
echo "🎨 Preparing DMG contents..."
DMG_STAGING="${BUILD_DIR}/dmg-staging"
mkdir -p "${DMG_STAGING}"

# Copy app to staging
cp -R "${APP_PATH}" "${DMG_STAGING}/"

# Create Applications symlink
ln -s /Applications "${DMG_STAGING}/Applications"

# Create README
cat >"${DMG_STAGING}/README.txt" <<'README'
Ori Agent Installation
======================

To install:
1. Drag "Ori Agent.app" to the Applications folder
2. Double-click "Ori Agent" in Applications to start
3. Your browser will open to http://localhost:8080

The server runs in the background and can be accessed at:
http://localhost:8080

Logs are stored at:
~/Library/Logs/ori-agent.log

To stop the server:
  lsof -ti:8080 | xargs kill

For more information:
https://github.com/johnjallday/ori-agent
README

echo "✅ DMG staging area ready"

# Step 4: Create DMG
echo ""
echo "💿 Creating compressed DMG image..."

# Create compressed DMG directly (skip customization to avoid mounting issues)
rm -f "releases/${DMG_NAME}"
hdiutil create \
  -srcfolder "${DMG_STAGING}" \
  -volname "${APP_NAME} ${VERSION}" \
  -fs HFS+ \
  -fsargs "-c c=64,a=16,e=16" \
  -format UDZO \
  -imagekey zlib-level=9 \
  "releases/${DMG_NAME}"

# Clean up
rm -rf "${BUILD_DIR}"

echo ""
echo "=========================================="
echo "✅ Mac Installer created successfully!"
echo "=========================================="
echo ""
echo "📍 Location: releases/${DMG_NAME}"
ls -lh "releases/${DMG_NAME}"
echo ""
echo "📊 SHA-256 Checksum:"
shasum -a 256 "releases/${DMG_NAME}"
echo ""
echo "🎉 Ready to distribute!"
echo ""
