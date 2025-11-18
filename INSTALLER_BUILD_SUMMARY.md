# Ori Agent Installer Build System - Implementation Summary

## ✅ Phase 1 Complete: Installer-Only Distribution

**Goal**: Distribute **installers only**, not raw binaries or archives.

### What Users Get

When you create a release (e.g., `v0.0.12`), users will download:

**macOS:**
- `OriAgent-0.0.12-amd64.dmg` (Intel)
- `OriAgent-0.0.12-arm64.dmg` (Apple Silicon)

**Windows:** _(Coming in Phase 2)_
- `ori-agent-0.0.12-amd64.msi`

**Linux:** _(Coming in Phase 3)_
- `ori-agent_0.0.12_amd64.deb` (Debian/Ubuntu)
- `ori-agent-0.0.12-1.x86_64.rpm` (Red Hat/Fedora)

### Build Workflow

```
Tag Push (v0.0.12)
  ↓
GitHub Actions (macos-latest)
  ↓
GoReleaser
  ├─→ Build binaries (all platforms)
  ├─→ Skip archive creation ✓
  ├─→ Create macOS DMGs (via custom script)
  ├─→ Create Windows MSI (coming soon)
  ├─→ Create Linux packages (coming soon)
  └─→ Upload installers to GitHub Release
```

### What Changed

**Before:**
- ❌ Manual DMG creation with `./scripts/create-mac-installer.sh`
- ❌ Archives (.tar.gz, .zip) for all platforms
- ❌ Users had to extract and manually install binaries

**After:**
- ✅ Automated DMG creation on every tag push
- ✅ **No archives** - only installers
- ✅ Users get one-click installers for their platform
- ✅ Cleaner GitHub Releases page

### Directory Structure After Build

```
dist/
├── OriAgent-0.0.12-amd64.dmg          ← User downloads this (macOS Intel)
├── OriAgent-0.0.12-arm64.dmg          ← User downloads this (macOS Apple Silicon)
├── checksums.txt                       ← SHA-256 checksums
├── artifacts.json                      ← GoReleaser metadata
├── config.yaml                         ← GoReleaser config snapshot
├── metadata.json                       ← Release metadata
└── [build directories]/                ← Internal build artifacts
    ├── menubar_darwin_amd64_v1/
    ├── menubar_darwin_arm64_v8.0/
    ├── server_darwin_amd64_v1/
    ├── server_darwin_arm64_v8.0/
    ├── server_linux_amd64_v1/
    ├── server_linux_arm64_v8.0/
    └── server_windows_amd64_v1/
```

**Note**: Only the DMG files are uploaded to GitHub Releases. The build directories are temporary artifacts.

### Configuration Files

**`.goreleaser.yaml`** - Main configuration:
```yaml
archives:
  - format: binary  # No archives - just installers
    files:
      - none*       # Don't include extra files

publishers:
  - name: macos-dmg-amd64  # Calls build/macos/create-dmg.sh
  - name: macos-dmg-arm64  # Calls build/macos/create-dmg.sh
```

**`build/macos/create-dmg.sh`** - DMG creation script:
- Finds binaries in GoReleaser's build directories
- Creates .app bundle with launcher script
- Packages as DMG with Applications symlink
- Outputs to `dist/OriAgent-{version}-{arch}.dmg`

**`.github/workflows/release.yml`** - Automation:
```yaml
jobs:
  release:
    runs-on: macos-latest  # Required for DMG creation
    steps:
      - uses: goreleaser/goreleaser-action@v6
```

### Testing Locally

```bash
# 1. Build everything (no publishing)
goreleaser release --snapshot --clean --skip=publish

# 2. Check build output
ls -lh dist/*.dmg

# 3. Test a DMG
open dist/OriAgent-0.0.11-next-amd64.dmg
```

### Creating a Release

```bash
# 1. Update version and create tag
./scripts/create-release.sh v0.0.12

# 2. Push tag to trigger GitHub Actions
git push origin v0.0.12

# 3. GitHub Actions will:
#    - Run tests
#    - Build all binaries
#    - Create DMG installers
#    - Upload to GitHub Release
#    - Generate release notes
```

### Release Page Example

```
Ori Agent v0.0.12
=================

Installation
------------

Choose the installer for your platform:

macOS:
  • Apple Silicon (M1/M2/M3): OriAgent-0.0.12-arm64.dmg
  • Intel: OriAgent-0.0.12-amd64.dmg

Windows: ori-agent-0.0.12-amd64.msi (coming soon)
Linux:
  • Debian/Ubuntu: ori-agent_0.0.12_amd64.deb (coming soon)
  • Red Hat/Fedora: ori-agent-0.0.12-1.x86_64.rpm (coming soon)

Assets:
  - OriAgent-0.0.12-amd64.dmg (18 MB)
  - OriAgent-0.0.12-arm64.dmg (17 MB)
  - checksums.txt
```

### Verification

Users can verify downloads:

```bash
# macOS/Linux
shasum -a 256 OriAgent-0.0.12-arm64.dmg

# Windows
certutil -hashfile ori-agent-0.0.12-amd64.msi SHA256
```

### Benefits of Installer-Only Distribution

**For Users:**
- ✅ Simpler download experience (one file per platform)
- ✅ Native installers with familiar UX
- ✅ Automatic app placement (macOS: /Applications)
- ✅ Uninstaller support (drag to trash on macOS, Add/Remove Programs on Windows)
- ✅ Package manager integration (Linux apt/yum)

**For Developers:**
- ✅ Cleaner release artifacts
- ✅ Fewer files to maintain/sign
- ✅ Better professional appearance
- ✅ Easier to add features (auto-updates, signing, etc.)

### What's Next

**Phase 2: Windows MSI Installer** (Week 2)
- [ ] Create WiX template
- [ ] Add MSI configuration to GoReleaser
- [ ] Implement Start menu shortcuts
- [ ] Test on Windows 10/11

**Phase 3: Linux Packages** (Weeks 2-3)
- [ ] Add nfpm configuration
- [ ] Create systemd service
- [ ] Build .deb and .rpm packages
- [ ] Test on Ubuntu/Fedora

**Phase 4: Testing & Polish** (Weeks 3-4)
- [ ] Installer smoke tests
- [ ] Upgrade/downgrade testing
- [ ] Documentation updates
- [ ] Code signing (optional)

### Technical Details

**Why format: binary?**
- GoReleaser's `format: binary` mode creates individual binaries instead of archives
- Binaries are placed in `dist/{build-id}_{os}_{arch}_v{goamd64}/`
- Our DMG script finds these binaries using `find` with glob patterns
- This keeps the release clean while still allowing installer creation

**Why publishers instead of native DMG support?**
- GoReleaser Pro has native DMG support, but it's a paid feature
- Our custom publisher script gives us full control
- We can add custom logic (signing, notarization, etc.) easily
- Works with GoReleaser's free version

**Why macos-latest runner?**
- DMG creation requires macOS tools (`hdiutil`)
- GitHub provides macOS runners with all necessary tools
- Both Intel and ARM64 DMGs can be built on the same runner

### Troubleshooting

**"DMG not created"**
- Check GitHub Actions logs for errors
- Ensure publisher scripts have execute permissions
- Verify binaries exist in expected paths

**"Binary not found" in DMG script**
- GoReleaser directory naming changed
- Update the `find` commands in `build/macos/create-dmg.sh`
- Check actual paths in `dist/` directory

**"No artifacts uploaded to release"**
- Publishers may have failed silently
- Check that DMG files exist in `dist/` before upload
- Review GitHub Actions artifact upload step

### Resources

- [GoReleaser Documentation](https://goreleaser.com/)
- [DMG Creation Guide](build/README.md)
- [Release Workflow Documentation](docs/RELEASE_WORKFLOW.md)
- [macOS Installation Guide](docs/INSTALLATION_MACOS.md)

---

**Status**: Phase 1 Complete ✅
**Last Updated**: November 18, 2025
**Next**: Phase 2 - Windows MSI Installer
