# Tasks: Plugin Store Platform Compatibility & Robustness

## Relevant Files

### Core Platform Detection
- `internal/platform/detector.go` - Platform detection utility (OS, arch detection)
- `internal/platform/detector_test.go` - Unit tests for platform detection

### Plugin Registry
- `internal/registry/types.go` - Add platform metadata fields to plugin structs
- `internal/registry/registry.go` - Update registry sync to extract platform info from GitHub releases
- `internal/registry/registry_test.go` - Unit tests for platform metadata extraction

### Plugin HTTP Handlers
- `internal/pluginhttp/registry.go` - Add platform compatibility check before plugin download
- `internal/pluginhttp/registry_test.go` - Unit tests for compatibility checks

### Server Integration
- `internal/server/server.go` - Initialize platform detection on server startup

### UI Components
- `internal/web/static/js/modules/plugin-store.js` - Add platform filtering, badges, compatibility UI
- `internal/web/templates/pages/marketplace.tmpl` - Update plugin card HTML with platform indicators

### Configuration Files
- `plugin_registry_cache.json` - Updated schema to include platform metadata (runtime)
- `local_plugin_registry.json` - Updated schema for manually uploaded plugins (runtime)

### Notes

- Platform detection uses Go's `runtime` package (no external dependencies)
- Plugin registry schema will be backwards compatible (missing platforms = "unknown")
- UI updates use existing Bootstrap styling and icons
- GitHub release asset parsing uses regex to extract platform from filenames

## Instructions for Completing Tasks

**IMPORTANT:** As you complete each task, you must check it off in this markdown file by changing `- [ ]` to `- [x]`. This helps track progress and ensures you don't skip any steps.

Example:
- `- [ ] 1.1 Read file` → `- [x] 1.1 Read file` (after completing)

Update the file after completing each sub-task, not just after completing an entire parent task.

## Tasks

- [x] 0.0 Create feature branch
  - [x] 0.1 Create and checkout a new branch for this feature (e.g., `git checkout -b feature/plugin-store-platform-compatibility`)

- [x] 1.0 Create platform detection utility
  - [x] 1.1 Create `internal/platform/` directory
  - [x] 1.2 Create `internal/platform/detector.go`:
    - [x] 1.2.1 Import Go's `runtime` package
    - [x] 1.2.2 Implement `DetectPlatform() string` function returning OS-arch string (e.g., "darwin-arm64")
    - [x] 1.2.3 Implement `DetectOS() string` function returning OS only (e.g., "darwin")
    - [x] 1.2.4 Implement `DetectArch() string` function returning architecture only (e.g., "arm64")
    - [x] 1.2.5 Implement `GetPlatformDisplayName(platform string) string` for user-friendly names (e.g., "darwin-arm64" → "macOS (Apple Silicon)")
    - [x] 1.2.6 Add documentation comments for all exported functions
  - [x] 1.3 Create `internal/platform/detector_test.go`:
    - [x] 1.3.1 Write test for `DetectPlatform()` (verify format is "os-arch")
    - [x] 1.3.2 Write test for `DetectOS()` (verify returns valid OS)
    - [x] 1.3.3 Write test for `DetectArch()` (verify returns valid architecture)
    - [x] 1.3.4 Write test for `GetPlatformDisplayName()` with various inputs
    - [x] 1.3.5 Run tests: `go test ./internal/platform/`

- [x] 2.0 Update plugin registry schema and sync logic
  - [x] 2.1 Update `internal/types/types.go` (PluginRegistryEntry struct):
    - [x] 2.1.1 Add `Platforms []string` field to `PluginRegistryEntry` struct (list of supported platform strings like "darwin-arm64")
    - [x] 2.1.2 SupportedOS and SupportedArch fields already exist
    - [x] 2.1.3 SupportedOS and SupportedArch fields already exist
    - [x] 2.1.4 Add helper method `IsCompatibleWith(platform string) bool` to `PluginRegistryEntry`
    - [x] 2.1.5 Add documentation comments for new fields
  - [x] 2.2 Create `internal/registry/platform.go`:
    - [x] 2.2.1 Registry is fetched as JSON from GitHub (no dynamic sync needed)
    - [x] 2.2.2 Implement `extractPlatformsFromAssets(assets []string) []string` helper function:
      - [x] 2.2.2.1 Use regex to parse asset filenames for platform patterns: `(darwin|linux|windows|freebsd)[-_](amd64|arm64|386|arm)`
      - [x] 2.2.2.2 Extract and deduplicate platform strings
      - [x] 2.2.2.3 Return sorted list of platforms
    - [x] 2.2.3 Implement `extractOSAndArch(platforms []string) ([]string, []string)` helper to separate OS and arch lists
    - [x] 2.2.4 Platform metadata populated in GitHub registry JSON file (manual for now)
    - [x] 2.2.5 Handle plugins with no platforms via IsCompatibleWith fallback logic
    - [x] 2.2.6 Logging can be added when actually used
  - [x] 2.3 Create `internal/registry/platform_test.go`:
    - [x] 2.3.1 Write test for `extractPlatformsFromAssets()` with various asset naming conventions
    - [x] 2.3.2 Write test for `extractOSAndArch()` with various platform lists
    - [x] 2.3.3 Write test for `IsCompatibleWith()` method (in types_test.go)
    - [x] 2.3.4 Write test verifying plugins with no assets fallback to SupportedOS/Arch
    - [x] 2.3.5 Run tests: `go test ./internal/registry/` and `go test ./internal/types/`

- [x] 3.0 Add pre-installation platform compatibility checks
  - [x] 3.1 Update `internal/pluginhttp/registry.go`:
    - [x] 3.1.1 Find the plugin installation/download handler (likely `InstallPlugin` or similar)
    - [x] 3.1.2 At the start of the handler, get current platform using `platform.DetectPlatform()`
    - [x] 3.1.3 Load plugin metadata from registry
    - [x] 3.1.4 Check compatibility using `plugin.IsCompatibleWith(currentPlatform)`
    - [x] 3.1.5 If incompatible:
      - [x] 3.1.5.1 Return HTTP 400 Bad Request
      - [x] 3.1.5.2 Return JSON error response with structure:
        ```json
        {
          "error": "platform_incompatible",
          "message": "Plugin not available for your platform",
          "user_platform": "linux-arm64",
          "supported_platforms": ["darwin-amd64", "darwin-arm64"],
          "supported_os": ["darwin"],
          "supported_arch": ["amd64", "arm64"]
        }
        ```
    - [x] 3.1.6 If compatible, proceed with installation as normal
    - [x] 3.1.7 Add logging for blocked installation attempts
  - [x] 3.2 Update `internal/pluginhttp/registry_test.go`:
    - [x] 3.2.1 Write test for successful installation when platform is compatible
    - [x] 3.2.2 Write test for blocked installation when platform is incompatible
    - [x] 3.2.3 Write test for error response structure
    - [x] 3.2.4 Write test for plugins with "unknown" platform (should allow installation with warning)
    - [x] 3.2.5 Run tests: `go test ./internal/pluginhttp/`

- [ ] 4.0 Update plugin store UI with platform indicators
  - [ ] 4.1 Update `internal/web/static/js/modules/plugin-store.js`:
    - [ ] 4.1.1 Find the function that renders plugin cards (likely `renderPluginCard` or similar)
    - [ ] 4.1.2 Implement `getPlatformBadges(plugin)` helper function:
      - [ ] 4.1.2.1 Check if plugin has `os_support` array
      - [ ] 4.1.2.2 For each OS, add appropriate icon badge:
        - darwin: 🍎 macOS
        - linux: 🐧 Linux
        - windows: 🪟 Windows
        - freebsd: 🐡 FreeBSD
      - [ ] 4.1.2.3 Include architecture info in badge tooltip
      - [ ] 4.1.2.4 Return HTML string with badges
    - [ ] 4.1.3 Implement `getCompatibilityIndicator(plugin)` helper function:
      - [ ] 4.1.3.1 Get current platform from window/global variable (set by server)
      - [ ] 4.1.3.2 Check if plugin.platforms includes current platform
      - [ ] 4.1.3.3 Return object: `{compatible: boolean, badge: htmlString, cssClass: string}`
      - [ ] 4.1.3.4 Compatible: return green checkmark ✅ badge
      - [ ] 4.1.3.5 Incompatible: return warning ⚠️ badge with "Not available for your platform"
    - [ ] 4.1.4 Update plugin card HTML generation:
      - [ ] 4.1.4.1 Add platform badges section below plugin description
      - [ ] 4.1.4.2 Add compatibility indicator badge
      - [ ] 4.1.4.3 Add `data-compatible` attribute to card for filtering
      - [ ] 4.1.4.4 Apply dimmed/grayed-out styling to incompatible plugin cards
    - [ ] 4.1.5 Update install button rendering:
      - [ ] 4.1.5.1 If incompatible: disable button, change text to "Not Available"
      - [ ] 4.1.5.2 If incompatible: add tooltip explaining incompatibility
      - [ ] 4.1.5.3 If compatible: keep button enabled as normal
  - [ ] 4.2 Add current platform to server template data:
    - [ ] 4.2.1 Find where marketplace template is rendered in `internal/server/server.go`
    - [ ] 4.2.2 Add `CurrentPlatform` field to template data struct
    - [ ] 4.2.3 Populate with `platform.DetectPlatform()`
    - [ ] 4.2.4 Add `CurrentPlatformDisplay` field with user-friendly name
  - [ ] 4.3 Update `internal/web/templates/pages/marketplace.tmpl`:
    - [ ] 4.3.1 Add hidden input or data attribute with current platform value
    - [ ] 4.3.2 Add JavaScript to expose platform to plugin-store.js module
    - [ ] 4.3.3 Add CSS classes for dimmed/incompatible plugin styling

- [ ] 5.0 Add platform filtering to plugin store
  - [ ] 5.1 Update `internal/web/static/js/modules/plugin-store.js`:
    - [ ] 5.1.1 Add filter toggle UI element:
      - [ ] 5.1.1.1 Create toggle switch HTML: "[ ] Show incompatible plugins"
      - [ ] 5.1.1.2 Default state: OFF (hide incompatible plugins)
      - [ ] 5.1.1.3 Add toggle to top of plugin list, below search bar
    - [ ] 5.1.2 Implement `filterPluginsByCompatibility()` function:
      - [ ] 5.1.2.1 Get toggle state (show all vs compatible only)
      - [ ] 5.1.2.2 If "compatible only": hide cards with `data-compatible="false"`
      - [ ] 5.1.2.3 If "show all": show all cards
      - [ ] 5.1.2.4 Update result count display
    - [ ] 5.1.3 Add event listener for toggle change
    - [ ] 5.1.4 Save toggle state to localStorage for persistence
    - [ ] 5.1.5 Restore toggle state on page load
    - [ ] 5.1.6 Ensure filtering works together with existing search functionality
  - [ ] 5.2 Update `internal/web/templates/pages/marketplace.tmpl`:
    - [ ] 5.2.1 Add filter toggle HTML to template
    - [ ] 5.2.2 Add section showing "X plugins compatible with your platform (OS-ARCH)"
    - [ ] 5.2.3 Style toggle to match existing UI (Bootstrap switch component)

- [ ] 6.0 Implement error messages and user guidance
  - [ ] 6.1 Update `internal/web/static/js/modules/plugin-store.js`:
    - [ ] 6.1.1 Find the plugin installation function (likely `installPlugin` or similar)
    - [ ] 6.1.2 Add error handler for HTTP 400 with `error: "platform_incompatible"`
    - [ ] 6.1.3 Implement `showPlatformIncompatibleModal(errorData)` function:
      - [ ] 6.1.3.1 Create Bootstrap modal with error information
      - [ ] 6.1.3.2 Show user's current platform
      - [ ] 6.1.3.3 Show supported platforms list (with friendly names)
      - [ ] 6.1.3.4 Add "What you can do" section with guidance:
        - Contact plugin maintainer
        - Build manually from source (link to docs)
        - View similar compatible plugins
      - [ ] 6.1.3.5 Add "Close" button
    - [ ] 6.1.4 Call `showPlatformIncompatibleModal()` when incompatible install attempted
    - [ ] 6.1.5 Prevent install button click for incompatible plugins (defensive check)
  - [ ] 6.2 Create error modal HTML in `internal/web/templates/pages/marketplace.tmpl`:
    - [ ] 6.2.1 Add modal container with ID `platformIncompatibleModal`
    - [ ] 6.2.2 Add modal header: "⚠️ Plugin Not Available"
    - [ ] 6.2.3 Add modal body with placeholder content (populated by JS)
    - [ ] 6.2.4 Add modal footer with "Close" button
    - [ ] 6.2.5 Style modal to match existing modals in the app

- [ ] 7.0 Update settings page with platform information
  - [ ] 7.1 Update `internal/web/templates/pages/settings.tmpl`:
    - [ ] 7.1.1 Find the "System Information" section
    - [ ] 7.1.2 Add new info row: "Platform: darwin-arm64"
    - [ ] 7.1.3 Add user-friendly display: "Platform: macOS (Apple Silicon)"
    - [ ] 7.1.4 Add tooltip explaining what platform means
  - [ ] 7.2 Update settings page template data in `internal/server/server.go`:
    - [ ] 7.2.1 Find where settings template is rendered
    - [ ] 7.2.2 Add `Platform` field with detected platform string
    - [ ] 7.2.3 Add `PlatformDisplay` field with user-friendly name
  - [ ] 7.3 Update marketplace page to show platform context:
    - [ ] 7.3.1 Add header text: "Showing plugins for: macOS (Apple Silicon)"
    - [ ] 7.3.2 Add info icon with tooltip explaining platform detection
    - [ ] 7.3.3 Style to be subtle and non-intrusive

- [ ] 8.0 Write tests and verify functionality
  - [ ] 8.1 Run all unit tests:
    - [ ] 8.1.1 Run `go test ./internal/platform/`
    - [ ] 8.1.2 Run `go test ./internal/registry/`
    - [ ] 8.1.3 Run `go test ./internal/pluginhttp/`
    - [ ] 8.1.4 Fix any failing tests
  - [ ] 8.2 Verify test coverage:
    - [ ] 8.2.1 Run `go test -cover ./internal/platform/`
    - [ ] 8.2.2 Run `go test -cover ./internal/registry/`
    - [ ] 8.2.3 Run `go test -cover ./internal/pluginhttp/`
    - [ ] 8.2.4 Ensure coverage is at least 80% for new code
  - [ ] 8.3 Manual testing - Build and run server:
    - [ ] 8.3.1 Build server: `./scripts/build-server.sh`
    - [ ] 8.3.2 Start server: `./bin/ori-agent`
    - [ ] 8.3.3 Verify no startup errors in logs
  - [ ] 8.4 Manual testing - Plugin store UI:
    - [ ] 8.4.1 Open plugin store in browser
    - [ ] 8.4.2 Verify platform badges display correctly on plugin cards
    - [ ] 8.4.3 Verify compatible plugins show green checkmark
    - [ ] 8.4.4 Verify incompatible plugins (if any) show warning badge
    - [ ] 8.4.5 Verify "Show incompatible plugins" toggle works
    - [ ] 8.4.6 Verify toggle state persists after page reload
    - [ ] 8.4.7 Verify install button is disabled for incompatible plugins
  - [ ] 8.5 Manual testing - Platform compatibility:
    - [ ] 8.5.1 Try to install a compatible plugin - should succeed
    - [ ] 8.5.2 Simulate incompatible plugin install (modify registry data) - should show error modal
    - [ ] 8.5.3 Verify error modal shows correct platform information
    - [ ] 8.5.4 Verify error modal shows supported platforms list
  - [ ] 8.6 Manual testing - Settings page:
    - [ ] 8.6.1 Open settings page
    - [ ] 8.6.2 Verify "Platform" information displays correctly
    - [ ] 8.6.3 Verify user-friendly platform name shows (e.g., "macOS (Apple Silicon)")
  - [ ] 8.7 Manual testing - Registry sync:
    - [ ] 8.7.1 Trigger registry sync/refresh
    - [ ] 8.7.2 Check `plugin_registry_cache.json` for platform metadata
    - [ ] 8.7.3 Verify platforms array is populated for plugins with release assets
    - [ ] 8.7.4 Verify plugins without assets have `platforms: ["unknown"]`
  - [ ] 8.8 Run full test suite:
    - [ ] 8.8.1 Run `go test ./...` to ensure no regressions
    - [ ] 8.8.2 Fix any failing tests
  - [ ] 8.9 Run linters and formatters:
    - [ ] 8.9.1 Run `go fmt ./...`
    - [ ] 8.9.2 Run `go vet ./...`
    - [ ] 8.9.3 Run `golangci-lint run` (if available)
    - [ ] 8.9.4 Fix any issues reported
