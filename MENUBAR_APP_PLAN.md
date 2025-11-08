# macOS Menu Bar App Implementation Plan

## Overview
Create a hybrid menu bar app that controls the ori-agent server while keeping the existing web UI and CLI compatibility.

## User Requirements
- **Approach**: Hybrid (menu bar for server control + web UI for main interface)
- **Auto-start**: Manual start (server doesn't start automatically)
- **Features**: Start/Stop Server, Open Browser, Auto-start on Login
- **Compatibility**: Keep both cmd/server (CLI) and cmd/menubar (GUI)

## Architecture

### High-Level Design
```
┌─────────────────────────────────────┐
│   macOS Menu Bar App (systray)      │
│  ┌──────────────────────────────┐   │
│  │  Menu Bar Icon & Menu        │   │
│  └────────────┬─────────────────┘   │
│               │                     │
│  ┌────────────▼─────────────────┐   │
│  │  Server Controller           │   │
│  │  - Start/Stop lifecycle      │   │
│  │  - State management          │   │
│  │  - Process control           │   │
│  └────────────┬─────────────────┘   │
│               │                     │
│  ┌────────────▼─────────────────┐   │
│  │  HTTP Server (port 8765)     │   │
│  │  (existing ori-agent server) │   │
│  └──────────────────────────────┘   │
└─────────────────────────────────────┘
                │
                ▼
        ┌───────────────┐
        │  Web Browser  │
        │  (Web UI)     │
        └───────────────┘
```

### Components

#### 1. Menu Bar Application (`cmd/menubar/`)
- **Entry Point**: `main.go`
- **Purpose**: Initialize systray and server controller
- **Dependencies**: `fyne.io/systray` (already available)

#### 2. Server Controller (`internal/menubar/controller.go`)
- **Manages**:
  - Server lifecycle (start, stop, restart)
  - Server state tracking (stopped, starting, running, stopping, error)
  - Port availability checking
  - Process management
- **Key Functions**:
  - `StartServer(ctx context.Context) error`
  - `StopServer(ctx context.Context) error`
  - `GetStatus() ServerStatus`
  - `WatchStatus(callback func(ServerStatus))`

#### 3. Menu Manager (`internal/menubar/menu.go`)
- **Builds systray menu**:
  ```
  [🟢 Ori Icon]  (changes color based on status)
  ├─ 📊 Server Status: Stopped/Running
  ├─ ─────────────
  ├─ ▶️  Start Server
  ├─ ⏹  Stop Server (disabled when stopped)
  ├─ 🌐 Open Browser (disabled when stopped)
  ├─ ─────────────
  ├─ ⚙️  Auto-start on Login [✓/  ]
  ├─ ─────────────
  ├─ ℹ️  About Ori Agent
  └─ 🚪 Quit
  ```
- **Dynamic updates**: Menu items enable/disable based on state
- **Icon changes**: Visual feedback for server status

#### 4. Launch Agent Manager (`internal/menubar/launchagent.go`)
- **macOS Auto-start**:
  - Creates/removes `~/Library/LaunchAgents/com.ori.menubar.plist`
  - Manages launchctl load/unload
  - Persists setting in app state

## Implementation Steps

### Phase 1: Core Infrastructure
**Files to create/modify:**

1. **Create `internal/menubar/controller.go`**
   - Server lifecycle management
   - State machine (stopped → starting → running → stopping → stopped)
   - Goroutine for running server
   - Context-based cancellation
   - Error handling and recovery

2. **Modify `internal/server/server.go`**
   - Add `ShutdownHTTP(ctx context.Context) error` method
   - Implement graceful HTTP server shutdown
   - Ensure existing `Shutdown()` method is called
   - Add context support to `HTTPServer()` method

3. **Create `cmd/menubar/main.go`**
   - Initialize systray
   - Create server controller
   - Set up signal handling (SIGINT, SIGTERM)
   - Main event loop

### Phase 2: Menu Bar UI

4. **Create `internal/menubar/menu.go`**
   - Build systray menu structure
   - Handle menu item clicks
   - Dynamic menu updates
   - Icon management (embedded PNG icons)

5. **Create `internal/menubar/icons.go`**
   - Embed icon assets
   - Status icons (stopped, running, error)
   - Use `//go:embed` for bundling

### Phase 3: Auto-start Feature

6. **Create `internal/menubar/launchagent.go`**
   - Generate LaunchAgent plist XML
   - Install to `~/Library/LaunchAgents/`
   - `launchctl load/unload` commands
   - Check if already installed
   - Persist preference in `settings.json`

### Phase 4: Build System

7. **Modify `Makefile`**
   - Add `menubar` target
   - Add `menubar-app` alias
   - Build to `bin/ori-menubar`

8. **Modify `scripts/build.sh`**
   - Build both `bin/ori-agent` and `bin/ori-menubar`
   - Conditional building

9. **Modify `scripts/create-mac-installer.sh`**
   - Package `ori-menubar` as primary app
   - Keep `ori-agent` for CLI users
   - Update launcher script to use menubar version
   - Create app bundle with proper Info.plist

### Phase 5: Testing & Polish

10. **Testing checklist**:
    - [ ] Start server from menu bar
    - [ ] Stop server gracefully
    - [ ] Open browser while running
    - [ ] Handle port conflicts
    - [ ] Auto-start toggle (install/uninstall)
    - [ ] Auto-start actually works after login
    - [ ] Quit app stops server
    - [ ] Multiple menu bar apps don't conflict
    - [ ] Server state persists correctly
    - [ ] Error states display properly
    - [ ] CLI `cmd/server` still works independently

11. **Documentation**:
    - Update `README.md` with menu bar app instructions
    - Add section to `CLAUDE.md`
    - Document keyboard shortcuts (if any)

## Technical Details

### Server State Machine
```
    ┌─────────┐
    │ Stopped │ ◄───────────┐
    └────┬────┘             │
         │                  │
    [Start]            [Stop/Error]
         │                  │
         ▼                  │
    ┌─────────┐             │
    │Starting │             │
    └────┬────┘             │
         │                  │
    [Success]               │
         │                  │
         ▼                  │
    ┌─────────┐             │
    │ Running │ ────────────┘
    └─────────┘
```

### Configuration Storage
- **Menu bar preferences**: `settings.json`
  ```json
  {
    "menubar": {
      "autoStartOnLogin": false,
      "lastServerPort": 8080,
      "openBrowserOnStart": false
    }
  }
  ```

### Launch Agent Plist Template
```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.ori.menubar</string>
    <key>ProgramArguments</key>
    <array>
        <string>/Applications/Ori Agent.app/Contents/MacOS/ori-menubar</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>StandardOutPath</key>
    <string>/Users/USER/Library/Logs/ori-menubar.log</string>
    <key>StandardErrorPath</key>
    <string>/Users/USER/Library/Logs/ori-menubar.error.log</string>
</dict>
</plist>
```

### Error Handling

**Port Already in Use**:
- Check if port 8080 is occupied before starting
- Display error in menu: "Server Status: Port 8080 in use"
- Offer to open Activity Monitor

**Server Crash**:
- Detect unexpected server exit
- Update menu: "Server Status: Stopped (Error)"
- Offer to view logs

**Permission Issues**:
- Handle ~/Library/LaunchAgents permission errors
- Graceful fallback if auto-start can't be configured

### Performance Considerations
- Menu bar app should use minimal CPU when idle
- Server controller polls every 1 second when starting
- Use event-driven updates (channels) not polling
- Graceful shutdown timeout: 10 seconds max

## File Structure After Implementation

```
ori-agent/
├── cmd/
│   ├── menubar/              # NEW
│   │   └── main.go           # NEW - Menu bar entry point
│   └── server/
│       └── main.go           # KEEP - CLI server (unchanged)
├── internal/
│   ├── menubar/              # NEW
│   │   ├── controller.go     # NEW - Server lifecycle
│   │   ├── menu.go           # NEW - Systray menu
│   │   ├── launchagent.go    # NEW - macOS auto-start
│   │   └── icons.go          # NEW - Embedded icons
│   └── server/
│       └── server.go         # MODIFIED - Add graceful shutdown
├── assets/                   # NEW
│   └── menubar/              # NEW
│       ├── icon.png          # NEW - Menu bar icon
│       ├── icon-running.png  # NEW - Server running icon
│       └── icon-error.png    # NEW - Error state icon
├── bin/
│   ├── ori-agent             # CLI version
│   └── ori-menubar           # NEW - Menu bar version
├── scripts/
│   ├── build.sh              # MODIFIED
│   └── create-mac-installer.sh # MODIFIED
├── Makefile                  # MODIFIED
└── MENUBAR_APP_PLAN.md       # THIS FILE
```

## Dependencies

**Already Available**:
- `fyne.io/systray v1.11.0` (transitive dependency via Fyne)

**No New Dependencies Required**!

## Backward Compatibility

### Existing Functionality Preserved
- ✅ `cmd/server` continues to work as CLI tool
- ✅ Web UI unchanged (HTML/CSS/JS in `internal/ui/`)
- ✅ All API endpoints remain the same
- ✅ Plugin system unaffected
- ✅ Configuration files compatible
- ✅ Existing installers work (can be updated optionally)

### Migration Path
1. Users can try menu bar app without removing CLI version
2. Both can coexist (though only one instance runs at a time)
3. Installer can default to menu bar, but CLI still available

## Future Enhancements (Out of Scope)

- Native settings window (Fyne UI for advanced config)
- Notification center integration
- Keyboard shortcuts (global hotkeys)
- Preferences window for port configuration
- Log viewer window
- Plugin manager UI (native)
- Multiple server instances (different ports)

## Success Criteria

✅ Menu bar icon appears and is clickable
✅ Start/Stop server works reliably
✅ Server state accurately reflected in menu
✅ Open browser navigates to http://localhost:8765
✅ Auto-start on login can be toggled
✅ Graceful shutdown on quit
✅ No breaking changes to existing CLI
✅ Installer packages menu bar app correctly

## Timeline Estimate

- **Phase 1** (Core Infrastructure): 2-3 hours
- **Phase 2** (Menu Bar UI): 1-2 hours
- **Phase 3** (Auto-start): 1-2 hours
- **Phase 4** (Build System): 1 hour
- **Phase 5** (Testing & Polish): 2-3 hours

**Total**: ~8-12 hours of development time

---

**Document Version**: 1.0
**Created**: 2025-11-07
**Author**: Claude Code
**Status**: Ready for Implementation
