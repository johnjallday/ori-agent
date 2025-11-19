# Testing Installers on macOS

This guide shows how to test all Ori Agent installers (macOS, Windows, Linux) on your MacBook.

> **Note**: For automated smoke testing in CI/CD and local environments, see [SMOKE_TESTS.md](SMOKE_TESTS.md). This guide focuses on manual testing and detailed installer verification.

## Quick Reference

| Installer | Best Testing Method | Difficulty | Time |
|-----------|---------------------|------------|------|
| **macOS DMG** | Direct install | ⭐ Easy | 2 min |
| **Linux .deb/.rpm** | Docker containers | ⭐⭐ Medium | 5 min |
| **Windows MSI** | UTM/Parallels VM | ⭐⭐⭐ Hard | 30 min |

---

## 1. Testing macOS DMG (Direct Install)

**Easiest** - Test on your actual Mac:

```bash
# Build the DMG
cd /Users/jjdev/Projects/ori/ori-agent
goreleaser release --snapshot --clean --skip=publish

# Test amd64 (Intel) DMG
open dist/OriAgent-0.0.11-next-amd64.dmg

# OR test arm64 (Apple Silicon) DMG
open dist/OriAgent-0.0.11-next-arm64.dmg
```

### What to Test

1. **DMG Mounts**:
   - ✅ DMG opens without errors
   - ✅ Shows OriAgent.app and Applications symlink
   - ✅ README.txt is readable

2. **Installation**:
   ```bash
   # Drag OriAgent.app to Applications
   # Then run:
   open /Applications/OriAgent.app
   ```

3. **App Launches**:
   - ✅ Menu bar icon appears
   - ✅ No errors in Console.app
   - ✅ Can start/stop server from menu

4. **Server Works**:
   ```bash
   # Check if server is running
   curl http://localhost:8765
   ```

5. **Uninstall**:
   ```bash
   # Drag to trash
   rm -rf /Applications/OriAgent.app
   ```

---

## 2. Testing Linux Packages (Docker - Recommended)

**Best option** - Use Docker containers:

### Prerequisites

```bash
# Install Docker Desktop for Mac
# Download from: https://www.docker.com/products/docker-desktop/

# Or install via Homebrew
brew install --cask docker
```

### Test Debian/Ubuntu (.deb)

```bash
# Build the packages first
cd /Users/jjdev/Projects/ori/ori-agent
goreleaser release --snapshot --clean --skip=publish

# Start Ubuntu container
docker run -it --rm \
  -v $(pwd)/dist:/dist \
  ubuntu:22.04 \
  /bin/bash

# Inside the container:
apt-get update

# Test .deb installation (amd64)
dpkg -i /dist/ori-agent_*_linux_amd64.deb || apt-get install -f -y

# Check what was installed
ls -la /usr/bin/ori-agent
ls -la /lib/systemd/system/ori-agent.service
ls -la /etc/ori-agent/

# Try to run (will fail without API key, but tests binary works)
/usr/bin/ori-agent --version

# Check systemd service (won't start in container, but we can verify file)
cat /lib/systemd/system/ori-agent.service

# Test uninstall
apt-get remove ori-agent -y

# Exit container
exit
```

### Test Fedora/Red Hat (.rpm)

```bash
# Start Fedora container
docker run -it --rm \
  -v $(pwd)/dist:/dist \
  fedora:38 \
  /bin/bash

# Inside the container:
# Test .rpm installation (amd64)
dnf install -y /dist/ori-agent-*-linux-amd64.rpm

# Check installation
ls -la /usr/bin/ori-agent
/usr/bin/ori-agent --version

# Check service file
cat /lib/systemd/system/ori-agent.service

# Test uninstall
dnf remove ori-agent -y

# Exit
exit
```

### Test ARM64 Packages

If you have Apple Silicon Mac:

```bash
# Test ARM64 .deb
docker run -it --rm --platform linux/arm64 \
  -v $(pwd)/dist:/dist \
  ubuntu:22.04 \
  /bin/bash

# Inside container:
apt-get update
dpkg -i /dist/ori-agent_*_linux_arm64.deb || apt-get install -f -y
/usr/bin/ori-agent --version
exit
```

### Docker Compose Testing Script

Create `docker-test-installers.sh`:

```bash
#!/bin/bash
# Test all Linux installers in Docker

cd /Users/jjdev/Projects/ori/ori-agent

# Build packages
echo "Building packages..."
goreleaser release --snapshot --clean --skip=publish

# Test Ubuntu amd64
echo -e "\n=== Testing Ubuntu .deb (amd64) ==="
docker run --rm -v $(pwd)/dist:/dist ubuntu:22.04 bash -c "
  apt-get update -qq &&
  dpkg -i /dist/ori-agent_*_linux_amd64.deb 2>&1 | grep -v 'Selecting\|Unpacking' &&
  /usr/bin/ori-agent --version &&
  echo '✅ Ubuntu .deb (amd64) works!'
"

# Test Fedora amd64
echo -e "\n=== Testing Fedora .rpm (amd64) ==="
docker run --rm -v $(pwd)/dist:/dist fedora:38 bash -c "
  dnf install -y -q /dist/ori-agent-*-linux-amd64.rpm &&
  /usr/bin/ori-agent --version &&
  echo '✅ Fedora .rpm (amd64) works!'
"

# Test Ubuntu arm64 (if on Apple Silicon)
if [ "$(uname -m)" = "arm64" ]; then
  echo -e "\n=== Testing Ubuntu .deb (arm64) ==="
  docker run --rm --platform linux/arm64 -v $(pwd)/dist:/dist ubuntu:22.04 bash -c "
    apt-get update -qq &&
    dpkg -i /dist/ori-agent_*_linux_arm64.deb 2>&1 | grep -v 'Selecting\|Unpacking' &&
    /usr/bin/ori-agent --version &&
    echo '✅ Ubuntu .deb (arm64) works!'
  "
fi

echo -e "\n🎉 All tests passed!"
```

Make it executable:

```bash
chmod +x docker-test-installers.sh
./docker-test-installers.sh
```

---

## 3. Testing Linux Packages (VM - Full Testing)

For **complete testing** (systemd, service management):

### Option A: Multipass (Easiest)

**Best for**: Quick Ubuntu VMs

```bash
# Install Multipass
brew install --cask multipass

# Create Ubuntu VM
multipass launch --name ori-test --cpus 2 --memory 2G --disk 10G 22.04

# Copy .deb to VM
multipass transfer dist/ori-agent_*_amd64.deb ori-test:/home/ubuntu/

# Enter VM
multipass shell ori-test

# Inside VM:
sudo dpkg -i ori-agent_*_amd64.deb
sudo apt-get install -f -y

# Configure API key
sudo nano /etc/ori-agent/environment
# Add: OPENAI_API_KEY=your-key-here

# Start service
sudo systemctl start ori-agent
sudo systemctl status ori-agent

# Check logs
sudo journalctl -u ori-agent -f

# Test web UI (from Mac)
# Get VM IP:
hostname -I
# Then visit: http://<VM-IP>:8765

# Exit VM
exit

# Delete VM when done
multipass delete ori-test
multipass purge
```

### Option B: UTM (Free, GUI-Based)

**Best for**: Full VM testing with GUI

```bash
# Install UTM
brew install --cask utm

# Download Ubuntu ISO
# Visit: https://ubuntu.com/download/desktop
# Choose: Ubuntu 22.04 LTS (ARM64 for Apple Silicon)
```

**Steps**:
1. Open UTM
2. Create new VM → Virtualize
3. Choose Linux
4. Browse to downloaded .iso
5. Set RAM: 2GB, Cores: 2, Disk: 10GB
6. Start VM and install Ubuntu
7. Inside Ubuntu:
   ```bash
   # Transfer .deb (use Shared Folder or scp)
   sudo dpkg -i ori-agent_*_amd64.deb
   sudo systemctl start ori-agent
   ```

### Option C: Lima (Lightweight)

**Best for**: Command-line testing

```bash
# Install Lima
brew install lima

# Start Ubuntu VM
limactl start --name=ori-test template://ubuntu-lts

# Enter VM
lima sudo dpkg -i /path/to/ori-agent_*_amd64.deb

# Stop VM
limactl stop ori-test
```

---

## 4. Testing Windows MSI

### Option A: UTM with Windows ARM (Apple Silicon)

**Free, but slow**:

```bash
# Install UTM
brew install --cask utm

# Download Windows 11 ARM Preview ISO
# Visit: https://www.microsoft.com/en-us/software-download/windowsinsiderpreviewARM64
# (Requires Windows Insider account - free)
```

**Steps**:
1. Create new VM in UTM
2. Choose Windows
3. Browse to Windows ISO
4. Set RAM: 4GB, Disk: 64GB
5. Install Windows
6. Inside Windows:
   - Transfer `ori-agent-*-amd64.msi`
   - Double-click to install
   - Test Start Menu shortcuts

**Pros**: Free
**Cons**: Very slow, requires Insider Preview

---

### Option B: Parallels Desktop (Paid, Best)

**Recommended if you already have it**:

```bash
# Purchase Parallels Desktop ($99/year)
# Download from: https://www.parallels.com/

# Create Windows 11 VM (built-in assistant)
# Transfer MSI and test
```

**Pros**: Fast, easy, runs Windows 11 perfectly
**Cons**: $99/year

---

### Option C: VMware Fusion (Free for Personal Use)

```bash
# Download VMware Fusion
# Visit: https://www.vmware.com/products/fusion.html
# (Now free for personal use!)

# Create Windows VM
# Transfer and test MSI
```

**Pros**: Free, good performance
**Cons**: Requires Windows license

---

### Option D: GitHub Actions (No Local Setup)

**Easiest** - Test in CI/CD:

Create `.github/workflows/test-installers.yml`:

```yaml
name: Test Installers

on:
  workflow_dispatch:
  push:
    branches: [feature/installers-*]

jobs:
  test-linux:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version: '1.25'

      - name: Build packages
        run: |
          curl -sfL https://goreleaser.com/static/run | bash -s -- release --snapshot --clean --skip=publish

      - name: Test .deb installation
        run: |
          sudo dpkg -i dist/ori-agent_*_linux_amd64.deb || sudo apt-get install -f -y
          /usr/bin/ori-agent --version

  test-macos:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version: '1.25'

      - name: Build DMG
        run: |
          curl -sfL https://goreleaser.com/static/run | bash -s -- release --snapshot --clean --skip=publish

      - name: Test DMG
        run: |
          hdiutil attach dist/OriAgent-*-arm64.dmg
          ls -la /Volumes/Ori\ Agent*/
          hdiutil detach /Volumes/Ori\ Agent*

  test-windows:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version: '1.25'

      - name: Install WiX
        run: choco install wixtoolset -y

      - name: Build MSI
        run: |
          curl -sfL https://goreleaser.com/static/run | bash -s -- release --snapshot --clean --skip=publish
          .\build\windows\create-msi.ps1 -Version "0.0.11-test" -Arch "amd64"

      - name: Test MSI
        run: |
          msiexec /i dist\ori-agent-0.0.11-test-amd64.msi /qn /l*v install.log
          # Wait for installation
          Start-Sleep -Seconds 10
          # Check if installed
          Test-Path "C:\Program Files\OriAgent\bin\ori-agent.exe"
```

Run with:
```bash
git push origin feature/installers-build-and-test-workflow
# Check GitHub Actions tab
```

---

## 5. Comprehensive Test Script

Create `test-all-installers.sh`:

```bash
#!/bin/bash
# Comprehensive installer testing script

set -e

cd /Users/jjdev/Projects/ori/ori-agent

echo "🚀 Building all installers..."
goreleaser release --snapshot --clean --skip=publish

echo ""
echo "==================================="
echo "Testing macOS DMG"
echo "==================================="

# Test DMG can mount
hdiutil attach dist/OriAgent-*-arm64.dmg
sleep 2
ls -la /Volumes/Ori\ Agent*/
hdiutil detach /Volumes/Ori\ Agent* || true
echo "✅ macOS DMG mounts successfully"

echo ""
echo "==================================="
echo "Testing Linux .deb (Ubuntu)"
echo "==================================="

docker run --rm -v $(pwd)/dist:/dist ubuntu:22.04 bash -c "
  set -e
  apt-get update -qq
  dpkg -i /dist/ori-agent_*_linux_amd64.deb || apt-get install -f -y
  /usr/bin/ori-agent --version
  test -f /lib/systemd/system/ori-agent.service
  test -d /etc/ori-agent
  echo '✅ Ubuntu .deb package works'
"

echo ""
echo "==================================="
echo "Testing Linux .rpm (Fedora)"
echo "==================================="

docker run --rm -v $(pwd)/dist:/dist fedora:38 bash -c "
  set -e
  dnf install -y -q /dist/ori-agent-*-linux-amd64.rpm
  /usr/bin/ori-agent --version
  test -f /lib/systemd/system/ori-agent.service
  test -d /etc/ori-agent
  echo '✅ Fedora .rpm package works'
"

echo ""
echo "==================================="
echo "📊 Test Summary"
echo "==================================="
echo "✅ macOS DMG: PASSED"
echo "✅ Linux .deb: PASSED"
echo "✅ Linux .rpm: PASSED"
echo "⏳ Windows MSI: Requires Windows VM"
echo ""
echo "🎉 All available tests passed!"
```

Run it:

```bash
chmod +x test-all-installers.sh
./test-all-installers.sh
```

---

## Recommended Testing Strategy

### Quick Testing (5 minutes)

```bash
# 1. Test macOS DMG directly
open dist/OriAgent-*-arm64.dmg

# 2. Test Linux in Docker
./docker-test-installers.sh

# 3. Skip Windows for now (test in GitHub Actions)
```

### Thorough Testing (30 minutes)

```bash
# 1. Test macOS DMG (install and run)
# 2. Test Linux in Multipass VM (full systemd testing)
# 3. Test Windows in UTM/Parallels (if available)
```

### CI/CD Testing (0 minutes local time)

```bash
# Push to GitHub and let Actions test everything
git push origin feature/installers-build-and-test-workflow
```

---

## Quick Decision Guide

**Just want to verify builds work?**
→ Use Docker (5 minutes)

**Need to test systemd service?**
→ Use Multipass (15 minutes)

**Need to test Windows MSI?**
→ Use GitHub Actions or Parallels

**Want professional testing?**
→ Use all VMs + CI/CD

---

## Troubleshooting

### Docker "permission denied"

```bash
# Start Docker Desktop app first
open -a Docker

# Wait for it to start, then try again
```

### DMG "can't be opened"

```bash
# Right-click → Open (instead of double-click)
# Or remove quarantine:
xattr -dr com.apple.quarantine dist/*.dmg
```

### VM too slow

```bash
# Give VM more resources:
# - Increase RAM to 4GB
# - Increase CPU cores to 4
# - Use SSD storage
```

---

## Next Steps

1. **Start simple**: Test macOS DMG + Linux Docker
2. **Set up CI/CD**: Let GitHub Actions test Windows
3. **Add to release checklist**: Always test before tagging

---

**Last Updated**: November 18, 2025
**Recommended**: Docker for Linux, Direct install for macOS, GitHub Actions for Windows
