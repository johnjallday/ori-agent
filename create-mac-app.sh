#!/bin/bash

# Create a proper macOS .app bundle for Ori Agent

APP_NAME="OriAgent"
APP_DIR="${APP_NAME}.app"

echo "🔨 Creating ${APP_NAME}.app..."

# Clean up old app
rm -rf "$APP_DIR"

# Create app bundle structure
mkdir -p "$APP_DIR/Contents/MacOS"
mkdir -p "$APP_DIR/Contents/Resources"

# Create the launcher script
cat >"$APP_DIR/Contents/MacOS/${APP_NAME}" <<'LAUNCHER'
#!/bin/bash

# Get the directory where the app is located
APP_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
RESOURCES_DIR="$APP_DIR/Resources"

cd "$RESOURCES_DIR"

# Start the server
if [ ! -f "./ori-agent" ]; then
    osascript -e 'display dialog "Ori Agent server not found. Please build it first." buttons {"OK"} default button 1 with icon stop'
    exit 1
fi

# Check if already running
if lsof -Pi :8080 -sTCP:LISTEN -t >/dev/null 2>&1 ; then
    osascript -e 'display dialog "Ori Agent is already running on port 8080" buttons {"OK"} default button 1 with icon note'
    open http://localhost:8080
    exit 0
fi

# Start server in background
./ori-agent > /tmp/ori-agent.log 2>&1 &
SERVER_PID=$!

# Wait for server to start
sleep 2

# Check if server started
if ! kill -0 $SERVER_PID 2>/dev/null; then
    osascript -e 'display dialog "Failed to start Ori Agent. Check logs at /tmp/ori-agent.log" buttons {"OK"} default button 1 with icon stop'
    exit 1
fi

# Open browser
sleep 1
open http://localhost:8080

# Show success message
osascript -e 'display notification "Server running on port 8080" with title "Ori Agent Started"'

LAUNCHER

# Make launcher executable
chmod +x "$APP_DIR/Contents/MacOS/${APP_NAME}"

# Create Info.plist
cat >"$APP_DIR/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>OriAgent</string>
    <key>CFBundleIdentifier</key>
    <string>com.ori.ori-agent</string>
    <key>CFBundleName</key>
    <string>Ori Agent</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>1.0.0</string>
    <key>CFBundleVersion</key>
    <string>1</string>
    <key>LSMinimumSystemVersion</key>
    <string>10.13</string>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
PLIST

echo "✅ Created app bundle structure"

# Copy necessary files to Resources
echo "📦 Copying files to app bundle..."

# Build server if needed
if [ ! -f "./ori-agent" ]; then
  echo "🔨 Building server..."
  go build -o ori-agent ./cmd/server
fi

cp ori-agent "$APP_DIR/Contents/Resources/"
cp -r uploaded_plugins "$APP_DIR/Contents/Resources/" 2>/dev/null || true
cp local_plugin_registry.json "$APP_DIR/Contents/Resources/plugin_registry_local.json" 2>/dev/null || true
cp settings.json "$APP_DIR/Contents/Resources/" 2>/dev/null || true

# Create settings.json if it doesn't exist
if [ ! -f "$APP_DIR/Contents/Resources/settings.json" ]; then
  if [ -n "$OPENAI_API_KEY" ]; then
    cat >"$APP_DIR/Contents/Resources/settings.json" <<SETTINGS
{
  "openai_api_key": "$OPENAI_API_KEY",
  "anthropic_api_key": "${ANTHROPIC_API_KEY:-}"
}
SETTINGS
    echo "✅ Created settings.json with your API keys"
  else
    echo "⚠️  No API keys found. You'll need to configure them in the app."
  fi
fi

echo ""
echo "✅ ${APP_NAME}.app created successfully!"
echo ""
echo "📍 Location: $PWD/${APP_DIR}"
echo ""
echo "To use:"
echo "  1. Double-click ${APP_NAME}.app"
echo "  2. Your browser will open to http://localhost:8080"
echo ""
echo "To distribute:"
echo "  - Drag ${APP_NAME}.app to Applications folder"
echo "  - Or zip it: zip -r OriAgent.zip ${APP_NAME}.app"
echo ""
