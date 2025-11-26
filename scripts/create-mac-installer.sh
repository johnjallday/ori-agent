#!/bin/bash

# Complete Mac Installer Creator for Ori Agent
# Creates a .app bundle and packages it in a professional DMG
# Usage: ./scripts/create-mac-installer.sh [version]

set -e

# Configuration
VERSION=${1:-"0.0.14"}
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

# Step 1: Build the binaries
echo ""
echo "🔨 Building binaries..."

# Build the menu bar app (primary)
echo "  Building menu bar app..."
go build -ldflags="-s -w" -o "${BUILD_DIR}/ori-menubar" ./cmd/menubar
chmod +x "${BUILD_DIR}/ori-menubar"
echo "  ✅ Menu bar app built"

# Build the CLI server (optional, for advanced users)
echo "  Building CLI server..."
go build -ldflags="-s -w" -o "${BUILD_DIR}/ori-agent" ./cmd/server
chmod +x "${BUILD_DIR}/ori-agent"
echo "  ✅ CLI server built"

echo "✅ All binaries built successfully"

# Step 2: Create .app bundle structure
echo ""
echo "📦 Creating .app bundle..."
APP_PATH="${BUILD_DIR}/${APP_BUNDLE}"
mkdir -p "${APP_PATH}/Contents/MacOS"
mkdir -p "${APP_PATH}/Contents/Resources"

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
    <key>CFBundleIconFile</key>
    <string>AppIcon</string>
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
# Menubar binary must live in Contents/MacOS so macOS sees it as the app executable
cp "${BUILD_DIR}/ori-menubar" "${APP_PATH}/Contents/MacOS/OriAgent"
cp "${BUILD_DIR}/ori-agent" "${APP_PATH}/Contents/Resources/"

# Copy app icon
if [ -f "${PROJECT_ROOT}/assets/AppIcon.icns" ]; then
  cp "${PROJECT_ROOT}/assets/AppIcon.icns" "${APP_PATH}/Contents/Resources/"
  echo "  ✓ App icon copied"
else
  echo "  ⚠️  Warning: AppIcon.icns not found, generating..."
  if [ -f "${PROJECT_ROOT}/scripts/generate-app-icon.sh" ]; then
    "${PROJECT_ROOT}/scripts/generate-app-icon.sh"
    cp "${PROJECT_ROOT}/assets/AppIcon.icns" "${APP_PATH}/Contents/Resources/"
  fi
fi

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
/Applications/Ori Agent.app/Contents/Resources/ori-agent

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
