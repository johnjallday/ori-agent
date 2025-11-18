# Building Windows MSI Installers

This guide explains how to build the Ori Agent MSI installer for Windows.

## Prerequisites

### Required Software

1. **Windows 10/11** (64-bit)
2. **WiX Toolset** v3.11 or later
3. **PowerShell** 5.1 or later (built into Windows)
4. **Go** 1.25+ (if building from source)

### Installing WiX Toolset

**Option 1: Official Installer**
1. Download from: https://wixtoolset.org/releases/
2. Run the installer
3. Follow the installation wizard

**Option 2: Chocolatey** (recommended)
```powershell
choco install wixtoolset
```

**Verify Installation**:
```powershell
# Should show version number
candle.exe -?
light.exe -?
```

## Building the MSI

### Step 1: Build Binaries

If you haven't already built the binaries:

```powershell
# Clone the repository
git clone https://github.com/johnjallday/ori-agent.git
cd ori-agent

# Build using GoReleaser (recommended)
goreleaser release --snapshot --clean --skip=publish

# OR build manually
go build -o dist/server_windows_amd64_v1/ori-agent.exe ./cmd/server
```

### Step 2: Run MSI Build Script

```powershell
# Navigate to project root
cd ori-agent

# Run the MSI builder script
.\build\windows\create-msi.ps1 -Version "0.0.12" -Arch "amd64" -DistDir "dist"
```

**Parameters**:
- `-Version`: Version number (e.g., "0.0.12")
- `-Arch`: Architecture (currently only "amd64" supported)
- `-DistDir`: Directory containing built binaries (default: "dist")

### Step 3: Verify Output

The MSI file will be created at:
```
dist/ori-agent-0.0.12-amd64.msi
```

Check the output for:
- ✅ WiX Toolset found
- ✅ Binary located
- ✅ Template prepared
- ✅ Compilation successful
- ✅ Linking successful
- ✅ MSI created successfully

## Build Output

### MSI File

**Location**: `dist/ori-agent-{version}-{arch}.msi`

**Size**: Approximately 9-10 MB

**Contents**:
- `ori-agent.exe` (server binary)
- Start menu shortcuts
- Desktop shortcut (optional)
- Uninstaller

### What's Included

The MSI installer includes:

1. **Main Application**:
   - Installs to: `C:\Program Files\OriAgent\bin\ori-agent.exe`

2. **Start Menu Shortcuts**:
   - Launch application
   - Documentation link
   - Uninstaller

3. **Desktop Shortcut** (optional):
   - User can opt-in during installation

4. **Registry Entries**:
   - Tracks installation state
   - Enables Add/Remove Programs functionality

## Customizing the MSI

### Modify WiX Template

Edit `build/windows/ori-agent.wxs` to customize:

**Change Installation Directory**:
```xml
<Directory Id="INSTALLFOLDER" Name="OriAgent">
  <!-- Change "OriAgent" to your preferred name -->
</Directory>
```

**Add Environment Variables**:
```xml
<Environment Id="PATH"
             Name="PATH"
             Value="[BinFolder]"
             Permanent="no"
             Part="last"
             Action="set"
             System="yes" />
```

**Add More Files**:
```xml
<File Id="ReadmeTXT"
      Name="README.txt"
      Source="README.md"
      KeyPath="no" />
```

**Change Product GUID** (for major versions):
```xml
<Product Id="*"
         UpgradeCode="YOUR-NEW-GUID-HERE">
```

Generate a new GUID at: https://www.guidgenerator.com/

### Build Script Options

The PowerShell script `create-msi.ps1` supports:

```powershell
# Basic usage
.\build\windows\create-msi.ps1 -Version "0.0.12" -Arch "amd64"

# Custom output directory
.\build\windows\create-msi.ps1 -Version "0.0.12" -Arch "amd64" -DistDir "custom-dist"

# With verbose output (add at top of script)
$VerbosePreference = "Continue"
```

## Manual Build Process

If you prefer to build manually without the script:

### Step 1: Prepare Template

```powershell
# Copy template to build directory
Copy-Item build\windows\ori-agent.wxs build-msi\ori-agent.wxs

# Replace variables (manually edit the file or use PowerShell)
$content = Get-Content build-msi\ori-agent.wxs -Raw
$content = $content -replace '{{.Version}}', '0.0.12'
$content = $content -replace '{{.Binary}}', 'dist\server_windows_amd64_v1\ori-agent.exe'
Set-Content build-msi\ori-agent.wxs $content
```

### Step 2: Compile with Candle

```powershell
candle.exe -arch x64 `
  -dPlatform=x64 `
  -dWin64=yes `
  -out build-msi\ori-agent.wixobj `
  build-msi\ori-agent.wxs
```

### Step 3: Link with Light

```powershell
light.exe -ext WixUIExtension `
  -out dist\ori-agent-0.0.12-amd64.msi `
  build-msi\ori-agent.wixobj
```

## Testing the MSI

### Test Installation

```powershell
# Install (as Administrator)
msiexec /i dist\ori-agent-0.0.12-amd64.msi

# Silent install
msiexec /i dist\ori-agent-0.0.12-amd64.msi /qn

# Install with logging
msiexec /i dist\ori-agent-0.0.12-amd64.msi /l*v install.log
```

### Test Uninstallation

```powershell
# Uninstall (find ProductCode in the log)
msiexec /x {PRODUCT-CODE} /qn

# Or use GUI
# Control Panel → Programs and Features → Ori Agent → Uninstall
```

### Verify Installation

```powershell
# Check if installed
Test-Path "C:\Program Files\OriAgent\bin\ori-agent.exe"

# Check Start Menu
Test-Path "$env:ProgramData\Microsoft\Windows\Start Menu\Programs\Ori Agent"

# Run the application
& "C:\Program Files\OriAgent\bin\ori-agent.exe" --version
```

## Troubleshooting

### "Candle not found"

**Cause**: WiX Toolset not in PATH

**Solution**:
```powershell
# Add WiX to PATH temporarily
$env:PATH += ";C:\Program Files (x86)\WiX Toolset v3.14\bin"

# Or install via Chocolatey (auto-adds to PATH)
choco install wixtoolset
```

### "The system cannot find the file specified"

**Cause**: Binary path incorrect

**Solution**: Verify binary location:
```powershell
Get-ChildItem -Path dist -Recurse -Filter "ori-agent.exe"
```

### Candle error: "Unresolved reference to symbol"

**Cause**: Missing WiX extension or incorrect variable

**Solutions**:
1. Check that all `{{.Variable}}` placeholders are replaced
2. Ensure WiX syntax is correct
3. Add missing extensions to Light command

### Light error: ICE validation warnings

**Cause**: MSI validation warnings (often cosmetic)

**Solution**: Suppress specific ICE checks:
```powershell
light.exe -ext WixUIExtension `
  -sice:ICE61 `  # Suppress ICE61
  -out dist\ori-agent.msi `
  build-msi\ori-agent.wixobj
```

## Code Signing (Optional)

To avoid SmartScreen warnings, sign the MSI:

### Get a Code Signing Certificate

1. Purchase from: Digicert, GlobalSign, or Sectigo
2. Cost: ~$200-500/year

### Sign the MSI

```powershell
# Using signtool from Windows SDK
signtool sign /f MyCert.pfx /p CertPassword /t http://timestamp.digicert.com dist\ori-agent-0.0.12-amd64.msi

# Verify signature
signtool verify /pa dist\ori-agent-0.0.12-amd64.msi
```

## CI/CD Integration

### GitHub Actions (Future Enhancement)

To build MSI in GitHub Actions, add to `.github/workflows/release.yml`:

```yaml
jobs:
  build-windows-msi:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v5

      - uses: actions/setup-go@v6
        with:
          go-version: '1.25'

      - name: Install WiX
        run: choco install wixtoolset -y

      - name: Build binaries
        run: goreleaser release --snapshot --clean --skip=publish

      - name: Create MSI
        run: .\build\windows\create-msi.ps1 -Version "${{ github.ref_name }}" -Arch "amd64"

      - name: Upload MSI
        uses: actions/upload-artifact@v5
        with:
          name: windows-msi
          path: dist/*.msi
```

**Note**: This is planned for Phase 4 (full CI/CD automation).

## Advanced Topics

### Multi-Architecture Support

Currently, only `amd64` (64-bit) is supported. To add ARM64:

1. Update WiX template with ARM64 conditions
2. Build ARM64 binary
3. Run script with `-Arch "arm64"`

### Adding Services

To install Ori Agent as a Windows Service:

1. Add to WiX template:
```xml
<ServiceInstall Id="OriAgentService"
                Type="ownProcess"
                Name="OriAgent"
                DisplayName="Ori Agent"
                Description="Modular AI Agent Framework"
                Start="auto"
                Account="LocalSystem"
                ErrorControl="normal" />

<ServiceControl Id="StartService"
                Name="OriAgent"
                Start="install"
                Stop="both"
                Remove="uninstall" />
```

2. Rebuild MSI

### Bundle Multiple Products

To create a bundle (EXE that installs multiple MSIs):

1. Create a Burn bundle WXS file
2. Reference MSI products
3. Build with burn.exe

See: https://wixtoolset.org/docs/v3/bundle/

## Resources

- [WiX Toolset Documentation](https://wixtoolset.org/docs/)
- [WiX Tutorial](https://www.firegiant.com/wix/tutorial/)
- [MSI Best Practices](https://learn.microsoft.com/en-us/windows/win32/msi/windows-installer-best-practices)
- [GoReleaser MSI Docs](https://goreleaser.com/customization/msi/)

## Getting Help

If you encounter issues:

1. Check the [Troubleshooting](#troubleshooting) section
2. Review WiX build logs
3. Post in [GitHub Discussions](https://github.com/johnjallday/ori-agent/discussions)
4. Open an [Issue](https://github.com/johnjallday/ori-agent/issues)

---

**Last Updated**: November 18, 2025
**WiX Version**: v3.14+ (v3 schema)
