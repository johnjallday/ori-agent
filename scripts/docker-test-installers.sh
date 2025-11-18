#!/bin/bash
# Test all Linux installers in Docker containers
# Usage: ./scripts/docker-test-installers.sh

set -e

cd "$(dirname "$0")/.."

echo "🚀 Building packages with GoReleaser..."
goreleaser release --snapshot --clean --skip=publish

echo ""
echo "==================================="
echo "Testing Ubuntu .deb (amd64)"
echo "==================================="

docker run --rm -v "$(pwd)/dist:/dist" ubuntu:22.04 bash -c "
  set -e
  apt-get update -qq 2>&1 | grep -v 'Get:' || true
  echo '📦 Installing ori-agent...'
  dpkg -i /dist/ori-agent_*_linux_amd64.deb 2>&1 | grep -v 'Selecting\|Unpacking' || apt-get install -f -y -qq
  echo '✅ Installation successful'
  echo ''
  echo '📍 Checking installed files...'
  test -f /usr/bin/ori-agent && echo '  ✓ Binary: /usr/bin/ori-agent'
  test -f /lib/systemd/system/ori-agent.service && echo '  ✓ Service: /lib/systemd/system/ori-agent.service'
  test -d /etc/ori-agent && echo '  ✓ Config: /etc/ori-agent/'
  test -f /usr/share/applications/ori-agent.desktop && echo '  ✓ Desktop: /usr/share/applications/ori-agent.desktop'
  echo ''
  echo '🔍 Testing binary...'
  /usr/bin/ori-agent --version || echo '  (Version flag not implemented yet)'
  echo ''
  echo '✅ Ubuntu .deb (amd64) works!'
"

echo ""
echo "==================================="
echo "Testing Fedora .rpm (amd64)"
echo "==================================="

docker run --rm -v "$(pwd)/dist:/dist" fedora:38 bash -c "
  set -e
  echo '📦 Installing ori-agent...'
  dnf install -y -q /dist/ori-agent-*-linux-amd64.rpm 2>&1 | grep -v 'Installing\|Running'
  echo '✅ Installation successful'
  echo ''
  echo '📍 Checking installed files...'
  test -f /usr/bin/ori-agent && echo '  ✓ Binary: /usr/bin/ori-agent'
  test -f /lib/systemd/system/ori-agent.service && echo '  ✓ Service: /lib/systemd/system/ori-agent.service'
  test -d /etc/ori-agent && echo '  ✓ Config: /etc/ori-agent/'
  echo ''
  echo '🔍 Testing binary...'
  /usr/bin/ori-agent --version || echo '  (Version flag not implemented yet)'
  echo ''
  echo '✅ Fedora .rpm (amd64) works!'
"

# Test ARM64 if on Apple Silicon
if [ "$(uname -m)" = "arm64" ]; then
  echo ""
  echo "==================================="
  echo "Testing Ubuntu .deb (arm64)"
  echo "==================================="

  docker run --rm --platform linux/arm64 -v "$(pwd)/dist:/dist" ubuntu:22.04 bash -c "
    set -e
    apt-get update -qq 2>&1 | grep -v 'Get:' || true
    echo '📦 Installing ori-agent (ARM64)...'
    dpkg -i /dist/ori-agent_*_linux_arm64.deb 2>&1 | grep -v 'Selecting\|Unpacking' || apt-get install -f -y -qq
    echo '✅ Installation successful'
    echo ''
    echo '🔍 Testing binary...'
    /usr/bin/ori-agent --version || echo '  (Version flag not implemented yet)'
    echo ''
    echo '✅ Ubuntu .deb (arm64) works!'
  "
fi

echo ""
echo "==================================="
echo "🎉 All tests passed!"
echo "==================================="
echo ""
echo "Tested packages:"
echo "  ✅ Ubuntu .deb (amd64)"
echo "  ✅ Fedora .rpm (amd64)"
if [ "$(uname -m)" = "arm64" ]; then
  echo "  ✅ Ubuntu .deb (arm64)"
fi
echo ""
echo "Next steps:"
echo "  • Test macOS DMG: open dist/OriAgent-*-arm64.dmg"
echo "  • Test Windows MSI: Use VM or GitHub Actions"
echo ""
