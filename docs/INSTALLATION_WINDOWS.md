# Ori Agent - Windows Installation Guide

This guide covers installing Ori Agent on Windows using the MSI installer.

## Quick Installation

### Download the Installer

1. Go to the [Releases page](https://github.com/johnjallday/ori-agent/releases)
2. Download the latest `ori-agent-{version}-amd64.msi` file
3. Double-click the MSI file to start installation

### Installation Steps

1. **Launch Installer**: Double-click the downloaded MSI file
2. **Windows SmartScreen Warning** (if you see it):
   - Click "More info"
   - Click "Run anyway"
   - *(This warning appears because the installer isn't code-signed yet - coming in a future release)*
3. **Welcome Screen**: Click "Next"
4. **Installation Location**: Choose install directory (default: `C:\Program Files\OriAgent`) or click "Next"
5. **Features**:
   - ✅ Ori Agent (required)
   - ✅ Desktop Shortcut (optional - uncheck if you don't want a desktop icon)
   - Click "Next"
6. **Install**: Click "Install"
7. **User Account Control**: Click "Yes" to allow installation
8. **Complete**: Click "Finish"

## What Gets Installed

### Installation Directory

Default location: `C:\Program Files\OriAgent\`

```
C:\Program Files\OriAgent\
└── bin\
    └── ori-agent.exe    # Main executable
```

### Start Menu Shortcuts

The installer creates shortcuts in:
**Start Menu → Ori Agent**

- **Ori Agent** - Launches the application
- **Ori Agent Documentation** - Opens GitHub repository
- **Uninstall Ori Agent** - Removes the application

### Desktop Shortcut (Optional)

If you selected the Desktop Shortcut feature, you'll find an **Ori Agent** icon on your desktop.

## Running Ori Agent

### Method 1: Start Menu

1. Press **Windows key**
2. Type "Ori Agent"
3. Click **Ori Agent** to launch

### Method 2: Desktop Shortcut

Double-click the **Ori Agent** icon on your desktop (if you installed it).

### Method 3: Command Line

1. Open **Command Prompt** or **PowerShell**
2. Run:
   ```cmd
   "C:\Program Files\OriAgent\bin\ori-agent.exe"
   ```

### Method 4: Add to PATH (Advanced)

To run `ori-agent` from anywhere:

1. Open **System Properties**:
   - Right-click **This PC** → **Properties**
   - Click **Advanced system settings**
   - Click **Environment Variables**

2. Edit **PATH**:
   - Under "System variables", find and select **Path**
   - Click **Edit**
   - Click **New**
   - Add: `C:\Program Files\OriAgent\bin`
   - Click **OK** on all dialogs

3. Open a **new** Command Prompt and run:
   ```cmd
   ori-agent --version
   ```

## First Run

1. When you first launch Ori Agent, you'll need to configure your API key
2. Open your web browser and go to: `http://localhost:8765`
3. Follow the onboarding wizard

## Configuration

### Settings Location

Ori Agent stores its configuration in:
```
C:\Users\{YourUsername}\AppData\Local\OriAgent\
```

### API Keys

Set your API key in one of these ways:

**Option 1: Environment Variable**
```cmd
setx OPENAI_API_KEY "your-api-key-here"
```

**Option 2: Settings File**

Edit `C:\Users\{YourUsername}\AppData\Local\OriAgent\settings.json`:
```json
{
  "openai_api_key": "your-api-key-here"
}
```

**Option 3: Web UI**

1. Open `http://localhost:8765`
2. Go to **Settings**
3. Enter your API key
4. Click **Save**

## Uninstalling

### Method 1: Start Menu

1. Press **Windows key**
2. Type "Ori Agent"
3. Click **Uninstall Ori Agent**

### Method 2: Settings App

1. Open **Settings** (Windows + I)
2. Go to **Apps** → **Installed apps**
3. Find **Ori Agent**
4. Click the three dots (**...**) → **Uninstall**

### Method 3: Control Panel

1. Open **Control Panel**
2. Go to **Programs** → **Programs and Features**
3. Find **Ori Agent**
4. Click **Uninstall**

## Troubleshooting

### "Windows protected your PC" (SmartScreen)

**Why this happens**: The installer isn't code-signed yet (coming in a future release).

**Solution**:
1. Click "More info"
2. Click "Run anyway"

### "This app can't run on your PC"

**Cause**: You're trying to run a 64-bit installer on a 32-bit Windows.

**Solution**: Download the 32-bit version (if available) or upgrade to 64-bit Windows.

### Installation fails with "Error 1603"

**Common causes**:
- Insufficient permissions
- Previous installation not fully removed
- Antivirus blocking

**Solutions**:
1. Run installer as Administrator (right-click → "Run as administrator")
2. Uninstall any previous versions completely
3. Temporarily disable antivirus during installation

### Port 8765 already in use

**Cause**: Another application is using port 8765.

**Solution**: Set a custom port via environment variable:
```cmd
set PORT=9000
ori-agent
```

### Can't access http://localhost:8765

**Possible causes**:
- Ori Agent isn't running
- Firewall blocking the port
- Wrong port number

**Solutions**:
1. Check if Ori Agent is running (Task Manager → Details → ori-agent.exe)
2. Check Windows Firewall settings
3. Try: `http://127.0.0.1:8765`

## Firewall Configuration

If Windows Firewall blocks Ori Agent:

1. Open **Windows Defender Firewall**
2. Click **Allow an app through firewall**
3. Click **Change settings**
4. Click **Allow another app**
5. Browse to: `C:\Program Files\OriAgent\bin\ori-agent.exe`
6. Click **Add**
7. Check both **Private** and **Public** networks
8. Click **OK**

## Running as a Service (Advanced)

To run Ori Agent as a Windows service (auto-start on boot):

```powershell
# Using NSSM (Non-Sucking Service Manager)
# Download from: https://nssm.cc/download

nssm install OriAgent "C:\Program Files\OriAgent\bin\ori-agent.exe"
nssm set OriAgent AppDirectory "C:\Program Files\OriAgent\bin"
nssm start OriAgent
```

## System Requirements

- **OS**: Windows 10 (64-bit) or later
- **RAM**: 512 MB minimum, 1 GB recommended
- **Disk**: 50 MB for application, additional space for data
- **Network**: Internet connection for LLM API access

## Upgrading

To upgrade to a new version:

1. Download the latest MSI installer
2. Run the installer
3. The installer will automatically remove the old version and install the new one
4. Your settings and data are preserved

## Security Notes

### SmartScreen Warning

Currently, installers show a SmartScreen warning because they aren't code-signed. Code signing is planned for a future release.

### Antivirus

Some antivirus software may flag Ori Agent. This is a false positive. You can:
- Add an exception for `C:\Program Files\OriAgent\`
- Submit the file to your antivirus vendor for analysis

### Permissions

Ori Agent requires:
- **Installation**: Administrator (to write to Program Files)
- **Runtime**: Standard user (runs with your user permissions)

## Getting Help

- **Documentation**: https://github.com/johnjallday/ori-agent
- **Issues**: https://github.com/johnjallday/ori-agent/issues
- **Discussions**: https://github.com/johnjallday/ori-agent/discussions

## Building the MSI Yourself

If you want to build the MSI installer yourself, see [BUILD_MSI.md](BUILD_MSI.md).

---

**Last Updated**: November 18, 2025
**Version**: Covers Ori Agent 0.0.11+
