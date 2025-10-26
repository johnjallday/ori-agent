#!/bin/bash

# Simple DMG creator using macOS built-in hdiutil
# Usage: ./scripts/create-dmg-simple.sh [version]

set -e

VERSION=${1:-"0.0.6"}
APP_NAME="ori-agent"
DMG_NAME="${APP_NAME}-${VERSION}-darwin-amd64.dmg"
TEMP_DIR="temp-dmg"
SOURCE_DIR="bin"
VOLUME_NAME="${APP_NAME} ${VERSION}"

echo "🔨 Creating DMG for ${APP_NAME} v${VERSION}"

# Clean up
rm -rf "${TEMP_DIR}"
mkdir -p "${TEMP_DIR}"
mkdir -p releases

# Copy binary
echo "📦 Copying binary..."
cp "${SOURCE_DIR}/${APP_NAME}" "${TEMP_DIR}/"
chmod +x "${TEMP_DIR}/${APP_NAME}"

# Create installation instructions
cat > "${TEMP_DIR}/README.txt" << 'EOF'
ori-agent Installation
======================

Option 1: System-wide installation (requires sudo):
  sudo cp ori-agent /usr/local/bin/
  ori-agent --help

Option 2: User installation:
  mkdir -p ~/bin
  cp ori-agent ~/bin/
  echo 'export PATH="$HOME/bin:$PATH"' >> ~/.zshrc
  source ~/.zshrc
  ori-agent --help

Usage:
  ori-agent           # Start on port 8080
  ori-agent --port 3000
  ori-agent --verbose
  ori-agent --help

More info: https://github.com/your-repo/ori-agent
EOF

# Calculate size needed (add 10MB buffer)
SIZE=$(du -sm "${TEMP_DIR}" | awk '{print $1+10}')

echo "🎨 Creating ${SIZE}MB DMG image..."

# Create temporary read-write DMG
hdiutil create \
  -srcfolder "${TEMP_DIR}" \
  -volname "${VOLUME_NAME}" \
  -fs HFS+ \
  -fsargs "-c c=64,a=16,e=16" \
  -format UDRW \
  -size ${SIZE}m \
  temp-rw.dmg

# Mount the DMG
echo "📂 Mounting DMG..."
MOUNT_DIR=$(hdiutil attach -readwrite -noverify -noautoopen temp-rw.dmg | grep "/Volumes/${VOLUME_NAME}" | awk '{print $3}')

if [ -z "$MOUNT_DIR" ]; then
  echo "❌ Failed to mount DMG"
  exit 1
fi

echo "✨ Customizing DMG appearance..."

# Set custom icon positions and window settings using AppleScript
osascript <<EOD
tell application "Finder"
  tell disk "${VOLUME_NAME}"
    open
    set current view of container window to icon view
    set toolbar visible of container window to false
    set statusbar visible of container window to false
    set the bounds of container window to {400, 100, 1000, 500}
    set viewOptions to the icon view options of container window
    set arrangement of viewOptions to not arranged
    set icon size of viewOptions to 72
    set background picture of viewOptions to file ".background:background.png"

    -- Position items
    set position of item "ori-agent" of container window to {150, 150}
    set position of item "README.txt" of container window to {450, 150}

    close
    open
    update without registering applications
    delay 2
  end tell
end tell
EOD

# Unmount
echo "💿 Finalizing DMG..."
hdiutil detach "${MOUNT_DIR}"

# Convert to compressed read-only
rm -f "releases/${DMG_NAME}"
hdiutil convert temp-rw.dmg \
  -format UDZO \
  -imagekey zlib-level=9 \
  -o "releases/${DMG_NAME}"

# Clean up temporary files
rm -rf "${TEMP_DIR}" temp-rw.dmg

echo "✅ DMG created successfully!"
echo "📍 Location: releases/${DMG_NAME}"
ls -lh "releases/${DMG_NAME}"

# Show checksums
echo ""
echo "📊 Checksums:"
shasum -a 256 "releases/${DMG_NAME}"
