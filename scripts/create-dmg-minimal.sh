#!/bin/bash

# Minimal DMG creator - quick and simple
# Usage: ./scripts/create-dmg-minimal.sh [version]

set -e

VERSION=${1:-"0.0.6"}
APP_NAME="ori-agent"
DMG_NAME="${APP_NAME}-${VERSION}-darwin-amd64.dmg"

echo "🔨 Creating minimal DMG for ${APP_NAME} v${VERSION}"

# Create releases directory
mkdir -p releases

# Remove old DMG if exists
rm -f "releases/${DMG_NAME}"

# Create DMG directly from bin directory
hdiutil create \
  -srcfolder bin \
  -volname "${APP_NAME} ${VERSION}" \
  -format UDZO \
  -imagekey zlib-level=9 \
  "releases/${DMG_NAME}"

echo "✅ DMG created: releases/${DMG_NAME}"
ls -lh "releases/${DMG_NAME}"
