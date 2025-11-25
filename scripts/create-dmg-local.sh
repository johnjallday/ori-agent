#!/bin/bash
# Create DMG from locally built binaries (for development/testing)
# This is simpler than the full GoReleaser flow

set -e

# Configuration
VERSION="0.0.13-native"
ARCH=$(uname -m)  # arm64 or x86_64
if [ "$ARCH" = "x86_64" ]; then
    ARCH="amd64"
fi

APP_NAME="Ori Agent"
APP_BUNDLE="OriAgent.app"
DMG_NAME="OriAgent-${VERSION}-${ARCH}.dmg"
BUNDLE_ID="com.ori.ori-agent"
BUILD_DIR="build-dmg-local"
DIST_DIR="dist"

echo "🚀 Creating macOS DMG for ${APP_NAME} v${VERSION} (${ARCH})"
echo "=========================================================="

# Check if binaries exist
if [ ! -f "bin/ori-menubar" ]; then
    echo "❌ Error: bin/ori-menubar not found. Run './scripts/build.sh' first"
    exit 1
fi

if [ ! -f "bin/ori-agent" ]; then
    echo "❌ Error: bin/ori-agent not found. Run './scripts/build.sh' first"
    exit 1
fi

# Clean up previous builds
echo "🧹 Cleaning up..."
rm -rf "${BUILD_DIR}"
mkdir -p "${BUILD_DIR}"
mkdir -p "${DIST_DIR}"

# Step 1: Create .app bundle structure
echo ""
echo "📦 Creating .app bundle..."
APP_PATH="${BUILD_DIR}/${APP_BUNDLE}"
mkdir -p "${APP_PATH}/Contents/MacOS"
mkdir -p "${APP_PATH}/Contents/Resources"

# Copy the menubar binary directly as the main executable
# No wrapper script - this is important for macOS security and XPC permissions
cp "bin/ori-menubar" "${APP_PATH}/Contents/MacOS/OriAgent"
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
</dict>
</plist>
PLIST

# Copy ori-agent binary to Resources
echo "📦 Copying ori-agent binary..."
cp "bin/ori-agent" "${APP_PATH}/Contents/Resources/"
echo "  ✓ Copied ori-agent"

echo "✅ .app bundle created"

# Sign the app bundle
echo ""
echo "🔐 Signing app bundle..."
CERT_NAME="OriAgent Self-Signed"
if security find-identity -v -p codesigning 2>/dev/null | grep -q "${CERT_NAME}"; then
    echo "  Using certificate: ${CERT_NAME}"
    codesign --force --deep --options runtime --sign "${CERT_NAME}" "${APP_PATH}"
else
    echo "  Using ad-hoc signature (FREE, no Apple account needed)"
    codesign --force --deep --sign - "${APP_PATH}"
fi
echo "✅ App signed successfully"

# Step 2: Create DMG staging area
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
Ori Agent Installation (Native menubar build)
==============================================

To install:
1. Drag "OriAgent.app" to the Applications folder
2. Double-click "Ori Agent" in Applications to start
3. A menu bar icon will appear - click it to start the server

⚠️  macOS Security Note:
On first launch, macOS may show a security warning because this app
is not signed. To allow it to run:

1. Go to System Settings > Privacy & Security
2. Scroll down to "Security" section
3. Click "Open Anyway" next to the Ori Agent message
4. Confirm by clicking "Open"

Features:
• Native macOS menu bar app (no third-party dependencies!)
• Start/Stop server controls
• Auto-start on login option
• Quick access to open browser
• Visual server status indicators

The server can be accessed at:
http://localhost:8765

Configuration files are stored at:
~/Library/Application Support/OriAgent/

Logs are stored at:
~/Library/Logs/

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
echo "🧪 To test the DMG:"
echo "   open ${DIST_DIR}/${DMG_NAME}"
echo ""
