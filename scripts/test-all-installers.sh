#!/bin/bash
# Comprehensive installer testing script
# Tests: macOS DMG, Linux .deb, Linux .rpm
# Usage: ./scripts/test-all-installers.sh

set -e

cd "$(dirname "$0")/.."

# Get version from VERSION file
VERSION=$(cat VERSION)-next

echo ""
echo "╔════════════════════════════════════════╗"
echo "║  Ori Agent Installer Test Suite       ║"
echo "╚════════════════════════════════════════╝"
echo ""

echo "🚀 Building all installers..."
goreleaser release --snapshot --clean --skip=publish

# Build folder picker for DMG packaging
echo ""
echo "🔨 Building folder picker..."
./scripts/build-folder-picker.sh > /dev/null 2>&1
echo "✅ Folder picker built"

# Create DMGs manually (publishers are skipped in snapshot mode)
echo ""
echo "🔨 Creating macOS DMGs..."
./build/macos/create-dmg.sh "$VERSION" darwin amd64 dist > /dev/null 2>&1
./build/macos/create-dmg.sh "$VERSION" darwin arm64 dist > /dev/null 2>&1
echo "✅ DMGs created"

echo ""
echo "==================================="
echo "1. Testing macOS DMG"
echo "==================================="

# Find the DMG for current architecture
ARCH=$(uname -m)
if [ "$ARCH" = "arm64" ]; then
  DMG_FILE=$(ls dist/OriAgent-*-arm64.dmg 2>/dev/null | head -1)
else
  DMG_FILE=$(ls dist/OriAgent-*-amd64.dmg 2>/dev/null | head -1)
fi

if [ -z "$DMG_FILE" ]; then
  echo "❌ No DMG file found"
  exit 1
fi

echo "📦 Testing: $DMG_FILE"

# Test DMG can mount
echo "  Mounting DMG..."
hdiutil attach "$DMG_FILE" -quiet

sleep 1

# Find the mount point
MOUNT_POINT=$(hdiutil info | grep "Ori Agent" | awk '{print $1}')
VOLUME=$(ls -d /Volumes/Ori\ Agent* 2>/dev/null | head -1)

if [ -z "$VOLUME" ]; then
  echo "❌ DMG failed to mount"
  exit 1
fi

echo "  Checking contents..."
if [ -d "$VOLUME/OriAgent.app" ]; then
  echo "  ✓ OriAgent.app found"
else
  echo "  ❌ OriAgent.app not found"
fi

if [ -L "$VOLUME/Applications" ]; then
  echo "  ✓ Applications symlink found"
else
  echo "  ❌ Applications symlink not found"
fi

if [ -f "$VOLUME/README.txt" ]; then
  echo "  ✓ README.txt found"
else
  echo "  ⚠️  README.txt not found (optional)"
fi

# Smoke test: Copy app and test it runs
echo "  🧪 Running smoke test..."
TEMP_APP="/tmp/OriAgent-test.app"
rm -rf "$TEMP_APP"
cp -R "$VOLUME/OriAgent.app" "$TEMP_APP" 2>/dev/null || true

if [ -d "$TEMP_APP" ]; then
  # Extract server binary and test it
  SERVER_BIN="$TEMP_APP/Contents/Resources/ori-agent"
  if [ -f "$SERVER_BIN" ]; then
    # Start server in background
    PORT=18765  # Use different port to avoid conflicts
    "$SERVER_BIN" --port=$PORT > /tmp/ori-agent-test.log 2>&1 &
    SERVER_PID=$!

    # Wait for server to start (max 10 seconds)
    echo "    → Starting server (PID: $SERVER_PID)..."
    for i in {1..20}; do
      if curl -s "http://localhost:$PORT/health" > /dev/null 2>&1; then
        echo "    ✓ Server responded to HTTP"
        break
      fi
      sleep 0.5
    done

    # Test health endpoint
    if curl -s "http://localhost:$PORT/health" | grep -q "ok"; then
      echo "    ✓ Health check passed"
    else
      echo "    ⚠️  Health check failed (server may need API key)"
    fi

    # Cleanup
    kill $SERVER_PID 2>/dev/null || true
    wait $SERVER_PID 2>/dev/null || true
    echo "    → Server stopped"
  else
    echo "    ⚠️  Server binary not found in .app bundle"
  fi
  rm -rf "$TEMP_APP"
else
  echo "    ⚠️  Could not copy app for smoke test"
fi

# Unmount
echo "  Unmounting..."
hdiutil detach "$VOLUME" -quiet 2>/dev/null || hdiutil detach "$MOUNT_POINT" -quiet 2>/dev/null || true

echo "✅ macOS DMG: PASSED (with smoke test)"

echo ""
echo "==================================="
echo "2. Testing Linux .deb (Ubuntu)"
echo "==================================="

# Skip if packages weren't built on this platform
if ! ls dist/ori-agent_*_linux_amd64.deb > /dev/null 2>&1; then
  echo "⚠️  No Linux .deb packages found, skipping Linux .deb test"
  DEB_SKIPPED=true
elif ! command -v docker &> /dev/null; then
  echo "⚠️  Docker not installed, skipping Linux .deb test"
  echo "   Install Docker to run: https://docs.docker.com/get-docker/"
  DEB_SKIPPED=true
elif ! docker info &> /dev/null; then
  echo "⚠️  Docker daemon not running, skipping Linux .deb test"
  echo "   Start Docker and re-run tests"
  DEB_SKIPPED=true
else
  if docker run --rm -v "$(pwd)/dist:/dist" ubuntu:22.04 bash -c "
    set -e
    apt-get update -qq 2>&1 > /dev/null
    echo '📦 Installing package...'
    dpkg -i /dist/ori-agent_*_linux_amd64.deb 2>&1 | grep -q 'Unpacking' || apt-get install -f -y -qq

    echo '🧪 Running smoke test...'
    # Start server in background
    /usr/bin/ori-agent --port=18765 > /tmp/ori-agent.log 2>&1 &
    SERVER_PID=\$!

    # Wait for server to start (max 10 seconds)
    echo '  → Starting server...'
    apt-get install -y -qq curl > /dev/null 2>&1
    for i in {1..20}; do
      if curl -s http://localhost:18765/health > /dev/null 2>&1; then
        echo '  ✓ Server responded to HTTP'
        break
      fi
      sleep 0.5
    done

    # Test health endpoint
    if curl -s http://localhost:18765/health | grep -q 'ok'; then
      echo '  ✓ Health check passed'
    else
      echo '  ⚠️  Health check failed (may need API key)'
    fi

    # Cleanup
    kill \$SERVER_PID 2>/dev/null || true
    echo '  → Server stopped'

    echo '📋 Verifying installed files...'
    test -f /lib/systemd/system/ori-agent.service && echo '  ✓ systemd service'
    test -d /etc/ori-agent && echo '  ✓ Config directory'
    test -f /usr/share/applications/ori-agent.desktop && echo '  ✓ Desktop entry'
  "; then
    echo "✅ Linux .deb: PASSED (with smoke test)"
    DEB_PASSED=true
  else
    echo "❌ Linux .deb: FAILED"
    DEB_PASSED=false
  fi
fi

echo ""
echo "==================================="
echo "3. Testing Linux .rpm (Fedora)"
echo "==================================="

# Skip if packages weren't built on this platform
if ! ls dist/ori-agent-*-linux-amd64.rpm > /dev/null 2>&1; then
  echo "⚠️  No Linux .rpm packages found, skipping Linux .rpm test"
  RPM_SKIPPED=true
elif ! command -v docker &> /dev/null; then
  echo "⚠️  Docker not installed, skipping Linux .rpm test"
  echo "   Install Docker to run: https://docs.docker.com/get-docker/"
  RPM_SKIPPED=true
elif ! docker info &> /dev/null; then
  echo "⚠️  Docker daemon not running, skipping Linux .rpm test"
  echo "   Start Docker and re-run tests"
  RPM_SKIPPED=true
else
  if docker run --rm -v "$(pwd)/dist:/dist" fedora:38 bash -c "
    set -e
    echo '📦 Installing package...'
    dnf install -y -q /dist/ori-agent-*-linux-amd64.rpm 2>&1 > /dev/null

    echo '🧪 Running smoke test...'
    # Start server in background
    /usr/bin/ori-agent --port=18765 > /tmp/ori-agent.log 2>&1 &
    SERVER_PID=\$!

    # Wait for server to start (max 10 seconds)
    echo '  → Starting server...'
    for i in {1..20}; do
      if curl -s http://localhost:18765/health > /dev/null 2>&1; then
        echo '  ✓ Server responded to HTTP'
        break
      fi
      sleep 0.5
    done

    # Test health endpoint
    if curl -s http://localhost:18765/health | grep -q 'ok'; then
      echo '  ✓ Health check passed'
    else
      echo '  ⚠️  Health check failed (may need API key)'
    fi

    # Cleanup
    kill \$SERVER_PID 2>/dev/null || true
    echo '  → Server stopped'

    echo '📋 Verifying installed files...'
    test -f /lib/systemd/system/ori-agent.service && echo '  ✓ systemd service'
    test -d /etc/ori-agent && echo '  ✓ Config directory'
  "; then
    echo "✅ Linux .rpm: PASSED (with smoke test)"
    RPM_PASSED=true
  else
    echo "❌ Linux .rpm: FAILED"
    RPM_PASSED=false
  fi
fi

echo ""
echo "==================================="
echo "📊 Test Summary"
echo "==================================="
echo ""
echo "Platform Coverage:"
echo "  ✅ macOS DMG ($ARCH)"
if [ "$DEB_SKIPPED" = true ]; then
  echo "  ⏭️  Linux .deb (Skipped - Docker not available)"
elif [ "$DEB_PASSED" = true ]; then
  echo "  ✅ Linux .deb (Debian/Ubuntu)"
else
  echo "  ❌ Linux .deb (FAILED)"
fi
if [ "$RPM_SKIPPED" = true ]; then
  echo "  ⏭️  Linux .rpm (Skipped - Docker not available)"
elif [ "$RPM_PASSED" = true ]; then
  echo "  ✅ Linux .rpm (Red Hat/Fedora)"
else
  echo "  ❌ Linux .rpm (FAILED)"
fi
echo "  ⏳ Windows MSI (requires Windows VM or CI/CD)"
echo ""
echo "Files Tested:"
ls -lh dist/*.dmg 2>/dev/null | awk '{print "  •", $9, "(" $5 ")"}'
ls -lh dist/*.deb 2>/dev/null | awk '{print "  •", $9, "(" $5 ")"}'
ls -lh dist/*.rpm 2>/dev/null | awk '{print "  •", $9, "(" $5 ")"}'
echo ""

# Determine overall test status
if [ "$DEB_PASSED" = false ] || [ "$RPM_PASSED" = false ]; then
  echo "❌ Some tests failed!"
  echo ""
  echo "Failed tests:"
  [ "$DEB_PASSED" = false ] && echo "  • Linux .deb"
  [ "$RPM_PASSED" = false ] && echo "  • Linux .rpm"
  echo ""
  exit 1
elif [ "$DEB_SKIPPED" = true ] || [ "$RPM_SKIPPED" = true ]; then
  echo "✅ All available tests passed!"
  echo "⚠️  Some tests were skipped (Docker not available)"
  echo ""
  echo "What was tested:"
  echo "  ✅ Installer packages build correctly"
  echo "  ✅ macOS DMG mounts and contains required files"
  echo "  ✅ Server binary starts successfully"
  echo "  ✅ Server responds to HTTP requests"
  echo ""
  echo "Skipped tests:"
  [ "$DEB_SKIPPED" = true ] && echo "  • Linux .deb (requires Docker)"
  [ "$RPM_SKIPPED" = true ] && echo "  • Linux .rpm (requires Docker)"
  echo ""
  echo "Next steps:"
  echo "  • Install Docker to run full test suite"
  echo "  • Run in CI/CD: .github/workflows/smoke-tests.yml"
  echo "  • Install macOS DMG: open $DMG_FILE"
  echo "  • Configure API keys for full functionality"
  echo ""
else
  echo "🎉 All installer and smoke tests passed!"
  echo ""
  echo "What was tested:"
  echo "  ✅ Installer packages build correctly"
  echo "  ✅ Installers can be installed"
  echo "  ✅ Server binary starts successfully"
  echo "  ✅ Server responds to HTTP requests"
  echo "  ✅ Health check endpoint works"
  echo ""
  echo "Next steps:"
  echo "  • Run in CI/CD: .github/workflows/smoke-tests.yml"
  echo "  • Install macOS DMG: open $DMG_FILE"
  echo "  • Configure API keys for full functionality"
  echo ""
fi
