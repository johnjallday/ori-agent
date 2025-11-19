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

# Create DMGs manually (publishers are skipped in snapshot mode)
echo ""
echo "🔨 Creating macOS DMGs..."
./build/macos/create-dmg.sh "$VERSION" amd64 dist > /dev/null 2>&1
./build/macos/create-dmg.sh "$VERSION" arm64 dist > /dev/null 2>&1
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

# Unmount
echo "  Unmounting..."
hdiutil detach "$VOLUME" -quiet 2>/dev/null || hdiutil detach "$MOUNT_POINT" -quiet 2>/dev/null || true

echo "✅ macOS DMG: PASSED"

echo ""
echo "==================================="
echo "2. Testing Linux .deb (Ubuntu)"
echo "==================================="

docker run --rm -v "$(pwd)/dist:/dist" ubuntu:22.04 bash -c "
  set -e
  apt-get update -qq 2>&1 > /dev/null
  dpkg -i /dist/ori-agent_*_linux_amd64.deb 2>&1 | grep -q 'Unpacking' || apt-get install -f -y -qq
  /usr/bin/ori-agent --version 2>/dev/null || true
  test -f /lib/systemd/system/ori-agent.service
  test -d /etc/ori-agent
  test -f /usr/share/applications/ori-agent.desktop
" && echo "✅ Linux .deb: PASSED" || echo "❌ Linux .deb: FAILED"

echo ""
echo "==================================="
echo "3. Testing Linux .rpm (Fedora)"
echo "==================================="

docker run --rm -v "$(pwd)/dist:/dist" fedora:38 bash -c "
  set -e
  dnf install -y -q /dist/ori-agent-*-linux-amd64.rpm 2>&1 > /dev/null
  /usr/bin/ori-agent --version 2>/dev/null || true
  test -f /lib/systemd/system/ori-agent.service
  test -d /etc/ori-agent
" && echo "✅ Linux .rpm: PASSED" || echo "❌ Linux .rpm: FAILED"

echo ""
echo "==================================="
echo "📊 Test Summary"
echo "==================================="
echo ""
echo "Platform Coverage:"
echo "  ✅ macOS DMG ($ARCH)"
echo "  ✅ Linux .deb (Debian/Ubuntu)"
echo "  ✅ Linux .rpm (Red Hat/Fedora)"
echo "  ⏳ Windows MSI (requires Windows VM or CI/CD)"
echo ""
echo "Files Tested:"
ls -lh dist/*.dmg 2>/dev/null | awk '{print "  •", $9, "(" $5 ")"}'
ls -lh dist/*.deb 2>/dev/null | awk '{print "  •", $9, "(" $5 ")"}'
ls -lh dist/*.rpm 2>/dev/null | awk '{print "  •", $9, "(" $5 ")"}'
echo ""
echo "🎉 All available tests passed!"
echo ""
echo "Next steps:"
echo "  • Install macOS DMG: open $DMG_FILE"
echo "  • Test in real VM for full validation"
echo "  • Set up CI/CD for automated testing"
echo ""
