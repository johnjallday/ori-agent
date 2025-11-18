# Testing Shared Plugins from ../plugins

This guide covers testing the shared plugins located in the `../plugins` directory.

## Shared Plugins Available

The following plugins are located in `../plugins/`:

1. **ori-music-project-manager** (macOS) - Manage music production projects
2. **ori-reaper** (macOS) - Control Reaper DAW via web interface
3. **ori-mac-os-tools** (macOS) - macOS system utilities and tools
4. **ori-meta-threads-manager** - Multi-threaded conversation management
5. **ori-agent-doc-builder** - Documentation generation

## Building Shared Plugins

### Option 1: Build All External Plugins
```bash
./scripts/build-external-plugins.sh
```

### Option 2: Build Individual Plugin
```bash
cd ../plugins/ori-music-project-manager
go build -o ori-music-project-manager main.go
```

### Option 3: Use Build Script (if exists in plugin dir)
```bash
cd ../plugins/ori-reaper
./build.sh
```

## Testing Approaches

### 1. Automated Tests

Run all shared plugin tests:
```bash
cd ori-agent
make test-user
```

Run specific shared plugin test:
```bash
go test ./tests/user/plugins -run TestMusicProjectManagerPlugin -v
go test ./tests/user/plugins -run TestReaperPlugin -v
go test ./tests/user/plugins -run TestMacOSToolsPlugin -v
```

Run all shared plugin availability tests:
```bash
go test ./tests/user/plugins -run TestSharedPluginsAvailable -v
```

### 2. Manual Scenarios

Launch the scenario runner:
```bash
make test-scenarios
```

Then select from shared plugin scenarios:
- **sc011** - Ori-Music-Project-Manager Plugin (8 min, medium)
- **sc012** - Ori-Mac-OS-Tools Plugin (5 min, easy)
- **sc013** - Ori-Script-Runner Plugin (6 min, medium)
- **sc014** - Ori-Agent-Doc-Builder Plugin (7 min, medium)
- **sc015** - Ori-Meta-Threads-Manager Plugin (10 min, hard)
- **sc016** - All Shared Plugins Load Test (5 min, easy)

### 3. Interactive CLI

```bash
make test-cli
```

Select option 1 to check environment - it will show:
```
✓ Plugins built: 8/8 total (built-in: 2/2, shared: 6/6)
```

## Plugin-Specific Testing

### Music Project Manager (macOS only)

**Prerequisites:**
- macOS system
- Plugin built: `../plugins/ori-music-project-manager/ori-music-project-manager`

**Quick Test:**
```bash
go test ./tests/user/plugins -run TestMusicProjectManagerPlugin -v
```

**Manual Test:**
1. Start server: `make run-dev`
2. Create agent "Music Producer"
3. Enable `ori-music-project-manager` plugin
4. Chat: "Create a new project called Test Song"
5. Chat: "List all my music projects"
6. Verify "Test Song" appears

**What to verify:**
- Project created in file system (check ~/Music/Projects or configured location)
- Project appears in list
- Project details accessible

---

### Ori-Reaper (macOS only)

**Prerequisites:**
- macOS system
- Reaper DAW installed
- Reaper web interface enabled (Preferences → Control/OSC/web)
- Plugin built: `../plugins/ori-reaper/ori-reaper`

**Quick Test:**
```bash
go test ./tests/user/plugins -run TestReaperPlugin -v
```

**Manual Test (Scenario sc007):**
1. Open Reaper DAW
2. Enable web interface (default port: 8080)
3. Start ori-agent server
4. Create agent "Reaper Controller"
5. Enable `ori-reaper` plugin
6. Chat: "Create a new track"
7. Verify track appears in Reaper
8. Chat: "Play the project"
9. Verify playback starts

**What to verify:**
- Reaper responds to commands
- Track creation works
- Transport controls (play/stop) work
- Plugin communicates with Reaper web API

---

### Ori-Mac-OS-Tools (macOS only)

**Prerequisites:**
- macOS system
- Plugin built: `../plugins/ori-mac-os-tools/ori-mac-os-tools`

**Quick Test:**
```bash
go test ./tests/user/plugins -run TestMacOSToolsPlugin -v
```

**Manual Test (Scenario sc012):**
1. Create agent "macOS Assistant"
2. Enable `ori-mac-os-tools` plugin
3. Chat: "What system am I running?"
4. Chat: "What applications are currently running?"
5. Chat: "What's my current directory?"

**What to verify:**
- System information accurate
- Running applications list matches Activity Monitor
- Directory information correct

---

### Ori-Agent-Doc-Builder

**Prerequisites:**
- Plugin built: `../plugins/ori-agent-doc-builder/ori-agent-doc-builder`

**Quick Test:**
```bash
go test ./tests/user/plugins -run TestDocBuilderPlugin -v
```

**Manual Test (Scenario sc014):**
1. Create agent "Doc Builder"
2. Enable `ori-agent-doc-builder` plugin
3. Chat: "Build documentation for the current project"
4. Chat: "What documentation formats are supported?"
5. Chat: "Generate a README template"

**What to verify:**
- Documentation generation starts
- Supported formats listed
- Templates generated correctly

---

### Ori-Meta-Threads-Manager

**Prerequisites:**
- Plugin built: `../plugins/ori-meta-threads-manager/ori-meta-threads-manager`

**Quick Test:**
```bash
go test ./tests/user/plugins -run TestMetaThreadsManagerPlugin -v
```

**Manual Test (Scenario sc015):**
1. Create agent "Threads Manager"
2. Enable `ori-meta-threads-manager` plugin
3. Chat: "Create a new conversation thread about project planning"
4. Chat: "List all active threads"
5. Chat: "Switch to the project planning thread"
6. Chat: "What thread am I in?"

**What to verify:**
- Thread creation works
- Thread listing accurate
- Thread switching functional
- Context maintained per thread

---

## Multi-Plugin Testing

### Test All Plugins Simultaneously (Scenario sc016)

**Purpose:** Verify plugin compatibility and resource usage

**Steps:**
1. Create agent "Multi-Plugin Agent"
2. Enable ALL shared plugins:
   - ori-music-project-manager
   - ori-reaper
   - ori-mac-os-tools
   - ori-meta-threads-manager
   - ori-agent-doc-builder
3. Verify no errors during enabling
4. Send test message
5. Check server logs

**What to verify:**
- All plugins load without conflicts
- No memory/resource issues
- Agent remains functional
- No error messages in logs

---

## Troubleshooting

### Plugin Not Found

**Check build status:**
```bash
# From ori-agent directory
ls -la ../plugins/*/

# Look for plugin binaries
ls -la ../plugins/ori-music-project-manager/ori-music-project-manager
ls -la ../plugins/ori-reaper/ori-reaper
```

**Build missing plugins:**
```bash
./scripts/build-external-plugins.sh
```

### Plugin Load Errors

**Check server logs:**
```bash
# If using make test-cli, view logs via menu option 11
# OR
tail -f test-server.log
```

**Common issues:**
- Plugin binary not executable: `chmod +x ../plugins/plugin-name/plugin-name`
- Plugin not in registry: Check `local_plugin_registry.json`
- Dependencies missing: Check plugin's own `go.mod`

### macOS-Specific Issues

**Permission errors:**
```bash
# Grant necessary permissions via System Preferences > Security & Privacy
```

**Reaper not responding:**
- Verify Reaper web interface enabled
- Check Reaper listening on port 8080
- Test manually: `curl http://localhost:8080`

### Test Failures

**Skip tests requiring plugins:**
```bash
# Tests auto-skip if plugin not built
go test ./tests/user/plugins -v
# Look for "SKIP" messages
```

**Run with verbose output:**
```bash
export TEST_VERBOSE=true
go test ./tests/user/plugins -v -run TestMusicProjectManagerPlugin
```

**Keep test artifacts:**
```bash
export TEST_CLEANUP=false
go test ./tests/user/plugins -v
# Check tests/user/reports/ for details
```

---

## Plugin Development Workflow

When developing a new shared plugin:

1. **Build the plugin:**
   ```bash
   cd ../plugins/my-new-plugin
   go build -o my-new-plugin main.go
   ```

2. **Add to test fixtures:**
   Edit `tests/user/helpers/fixtures.go`:
   ```go
   func SharedPluginNames() []string {
       return []string{
           // ... existing plugins
           "my-new-plugin",
       }
   }
   ```

3. **Write automated test:**
   Add to `tests/user/plugins/shared_plugins_test.go`:
   ```go
   func TestMyNewPlugin(t *testing.T) {
       ctx := helpers.NewTestContext(t)
       defer ctx.Cleanup()

       agent := ctx.CreateAgent("test-agent", "gpt-4o-mini")
       ctx.EnablePlugin(agent, "my-new-plugin")
       // ... test plugin functionality
   }
   ```

4. **Add manual scenario:**
   Edit `tests/user/scenarios/scenarios.json`:
   ```json
   {
     "id": "sc017",
     "name": "My New Plugin Test",
     "category": "shared-plugin",
     // ... scenario details
   }
   ```

5. **Test:**
   ```bash
   make test-user
   make test-scenarios
   ```

---

## Environment Variables

```bash
# Required for testing
export OPENAI_API_KEY="sk-..."
# OR
export ANTHROPIC_API_KEY="sk-ant-..."

# Optional
export TEST_VERBOSE="true"      # Detailed logs
export TEST_CLEANUP="false"     # Keep artifacts
export TEST_PLUGIN_DIR="../plugins"  # Plugin location
```

---

## Quick Reference

```bash
# Build all shared plugins
./scripts/build-external-plugins.sh

# Check plugin status (via CLI)
make test-cli
# Select: 1. Check environment

# Run all shared plugin tests
make test-user

# Run manual scenarios
make test-scenarios
# Select: 2. Run scenarios by category
# Choose: shared-plugin

# Test specific plugin
go test ./tests/user/plugins -run TestMusicProjectManagerPlugin -v
```

---

## Integration with CI/CD

Add to `.github/workflows/test.yml`:

```yaml
- name: Build Shared Plugins
  run: ./scripts/build-external-plugins.sh

- name: Test Shared Plugins
  run: make test-user
  env:
    OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
```

---

## Summary

- **16 scenarios total** (10 core + 6 shared plugin)
- **17 automated tests** (11 core + 6 shared plugin)
- **6 shared plugins** supported
- **macOS-specific testing** for music/audio plugins
- **Multi-plugin compatibility** testing included

All shared plugins are now fully integrated into the user-testing framework!
