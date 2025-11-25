#!/bin/bash

# Create a local .app bundle for testing
# This is faster than running the full GoReleaser build

set -e

VERSION="dev"
APP_NAME="Ori Agent"
APP_BUNDLE="OriAgent.app"
BUNDLE_ID="com.ori.ori-agent"

echo "🚀 Creating local macOS .app bundle for testing"
echo "================================================"

# Clean up previous build
echo "🧹 Cleaning up..."
rm -rf "$APP_BUNDLE"

# Create .app bundle structure
echo "📦 Creating .app bundle..."
mkdir -p "${APP_BUNDLE}/Contents/MacOS"
mkdir -p "${APP_BUNDLE}/Contents/Resources"

# Create the launcher script
cat >"${APP_BUNDLE}/Contents/MacOS/OriAgent" <<'LAUNCHER'
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
exec "$RESOURCES_DIR/ori-menubar"
LAUNCHER

chmod +x "${APP_BUNDLE}/Contents/MacOS/OriAgent"

# Create Info.plist
cat >"${APP_BUNDLE}/Contents/Info.plist" <<PLIST
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
if [ -f "assets/AppIcon.icns" ]; then
    cp "assets/AppIcon.icns" "${APP_BUNDLE}/Contents/Resources/"
    echo "  ✓ Icon copied"
else
    echo "  ⚠️  Warning: AppIcon.icns not found"
fi

# Build and copy binaries
echo "📦 Building and copying binaries..."
echo "  Building ori-menubar..."
go build -o "${APP_BUNDLE}/Contents/Resources/ori-menubar" ./cmd/menubar

echo "  Building ori-agent..."
go build -o "${APP_BUNDLE}/Contents/Resources/ori-agent" ./cmd/server

echo ""
echo "✅ App bundle created successfully!"
echo "📍 Location: $(pwd)/${APP_BUNDLE}"
echo ""
echo "To test:"
echo "  open ${APP_BUNDLE}"
echo ""
echo "To install:"
echo "  cp -R ${APP_BUNDLE} /Applications/"
echo ""
