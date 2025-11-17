# Installing Ori Agent on macOS

## Quick Start

### Option 1: Download Release (Recommended)

1. **Download** the latest DMG from [Releases](https://github.com/johnjallday/ori-agent/releases)
   - Intel Macs: `OriAgent-X.X.X-amd64.dmg`
   - Apple Silicon: `OriAgent-X.X.X-arm64.dmg`

2. **Open** the downloaded DMG file

3. **Bypass Gatekeeper Warning** (first time only):

   Since this app is not signed with an Apple Developer certificate, macOS will show a warning:

   > "OriAgent" Not Opened
   > Apple could not verify "OriAgent" is free of malware...

   **To open the app:**
   - **Right-click** (or Control+click) on OriAgent.app
   - Select **"Open"** from the menu
   - Click **"Open"** in the confirmation dialog

   macOS will remember this choice and the app will open normally in the future.

4. **Drag to Applications** folder

5. **Launch** from Applications or Spotlight

### Option 2: Build from Source

If you prefer to build yourself (no Gatekeeper warning):

```bash
# Install prerequisites
brew install go@1.25  # or download from golang.org

# Clone repository
git clone https://github.com/johnjallday/ori-agent.git
cd ori-agent

# Build
make menubar

# Run
./bin/ori-menubar
```

## Alternative Methods to Bypass Gatekeeper

If you downloaded the DMG and see the warning, you can also:

### Method 2: System Settings
1. Try to open OriAgent.app (it will be blocked)
2. Go to **System Settings → Privacy & Security**
3. Scroll down to find "OriAgent was blocked..."
4. Click **"Open Anyway"**
5. Click **"Open"** in the confirmation dialog

### Method 3: Terminal Command
```bash
# Remove quarantine attribute
xattr -d com.apple.quarantine /Applications/OriAgent.app

# Now open normally
open /Applications/OriAgent.app
```

## Setup

After installation:

1. The menu bar icon will appear in the top-right
2. Click the icon to:
   - Start/Stop the server
   - Open the web interface
   - Configure auto-start
3. Visit http://localhost:8765
4. Complete the onboarding wizard

## Auto-Start on Login

To have Ori Agent start automatically when you log in:

1. Click the menu bar icon
2. Select **"Start on Login"**
3. The checkmark indicates it's enabled

## Uninstallation

To completely remove Ori Agent:

```bash
# Stop the app
pkill -f ori-menubar

# Remove app
rm -rf /Applications/OriAgent.app

# Remove data (optional)
rm -rf ~/Library/Application\ Support/OriAgent
rm -rf ~/.ori-agent

# Remove auto-start (if enabled)
rm ~/Library/LaunchAgents/com.ori.ori-agent.plist
```

## Troubleshooting

### "App is damaged and can't be opened"

This can happen if the download was corrupted. Try:
```bash
xattr -cr /Applications/OriAgent.app
```

### Menu bar icon doesn't appear

The app may still be loading. Check:
```bash
# See if it's running
ps aux | grep ori-menubar

# Check logs
tail -f ~/Library/Logs/ori-menubar.log
```

### Server won't start

1. Check if port 8765 is already in use:
   ```bash
   lsof -i :8765
   ```

2. View server logs:
   ```bash
   tail -f ~/Library/Logs/ori-menubar.log
   ```

3. Try running the server directly:
   ```bash
   ~/Applications/OriAgent.app/Contents/Resources/ori-menubar
   ```

## Why the Gatekeeper Warning?

macOS shows this warning because:
- The app is not signed with an Apple Developer certificate ($99/year)
- This is common for free/open-source macOS apps
- The app is safe - you can verify the source code on GitHub

**Building from source** avoids this warning entirely, as macOS trusts apps you build yourself.

## Need Help?

- 📖 [Documentation](https://github.com/johnjallday/ori-agent/docs)
- 🐛 [Report an Issue](https://github.com/johnjallday/ori-agent/issues)
- 💬 [Discussions](https://github.com/johnjallday/ori-agent/discussions)
