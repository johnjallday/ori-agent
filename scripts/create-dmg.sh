#!/bin/bash

# Script to create a DMG installer for ori-agent
# Usage: ./scripts/create-dmg.sh [version]

set -e

VERSION=${1:-"0.0.6"}
APP_NAME="ori-agent"
DMG_NAME="${APP_NAME}-${VERSION}-darwin-amd64.dmg"
TEMP_DIR="temp-dmg"
SOURCE_DIR="bin"

echo "🔨 Creating DMG for ${APP_NAME} v${VERSION}"

# Clean up any previous temp directory
rm -rf "${TEMP_DIR}"
mkdir -p "${TEMP_DIR}"

# Copy binary to temp directory
echo "📦 Copying binary..."
cp "${SOURCE_DIR}/${APP_NAME}" "${TEMP_DIR}/"

# Make binary executable
chmod +x "${TEMP_DIR}/${APP_NAME}"

# Create a README for installation instructions
cat > "${TEMP_DIR}/INSTALL.txt" << 'EOF'
Installation Instructions
=========================

To install ori-agent:

1. Copy the ori-agent binary to a location in your PATH:

   sudo cp ori-agent /usr/local/bin/

   Or for user-only install:

   cp ori-agent ~/bin/

2. Make sure it's executable:

   chmod +x /usr/local/bin/ori-agent

3. Run ori-agent:

   ori-agent --help

For more information, visit: https://github.com/your-repo/ori-agent
EOF

# Remove old DMG if exists
rm -f "releases/${DMG_NAME}"
mkdir -p releases

echo "🎨 Creating DMG..."

# Create DMG with create-dmg
create-dmg \
  --volname "${APP_NAME} ${VERSION}" \
  --window-pos 200 120 \
  --window-size 600 400 \
  --icon-size 100 \
  --text-size 14 \
  --app-drop-link 400 200 \
  --no-internet-enable \
  "releases/${DMG_NAME}" \
  "${TEMP_DIR}"

# Clean up
rm -rf "${TEMP_DIR}"

echo "✅ DMG created: releases/${DMG_NAME}"
ls -lh "releases/${DMG_NAME}"
