# Product Requirements Document: Plugin Store Platform Compatibility & Robustness

## Introduction/Overview

The current plugin store allows users to discover and install plugins from a remote registry. However, users frequently encounter issues where plugins either don't have binaries available for their platform (OS/architecture combination) or downloaded binaries are incompatible with their system. This creates a poor user experience and blocks users from accessing essential plugin functionality.

This PRD outlines improvements to make the plugin store more robust by:
1. Detecting platform compatibility before download attempts
2. Clearly communicating which platforms each plugin supports
3. Providing helpful error messages when plugins aren't available for a user's platform
4. Improving the overall reliability of plugin installation

**Problem Statement:** Users cannot install plugins when their platform isn't supported, and the current system doesn't clearly communicate compatibility information or provide helpful guidance.

**Goal:** Make the plugin store reliable and transparent about platform compatibility, ensuring users understand which plugins will work on their system before attempting installation.

## Goals

1. **Prevent failed installations** - Detect incompatible platforms before download/installation attempts
2. **Improve transparency** - Clearly show which platforms each plugin supports
3. **Better error messaging** - Provide actionable error messages when plugins aren't available
4. **Enhanced user trust** - Build confidence that plugins will work on their system
5. **Reduce support burden** - Decrease user confusion and support requests related to compatibility

## User Stories

### As a user on an unsupported platform (e.g., Linux ARM64)
- I want to know which plugins are compatible with my system **before** I try to install them
- I want to see clear badges/indicators showing platform support
- I want to filter the plugin list to only show compatible plugins
- I want a helpful error message if I try to install an incompatible plugin, explaining why it won't work

### As a user browsing the plugin store
- I want to see at a glance which platforms each plugin supports (macOS, Linux, Windows, ARM, etc.)
- I want confidence that if I click "Install", the plugin will actually work on my system
- I want to understand my current platform (OS and architecture) clearly displayed

### As a user who encounters a missing plugin
- I want a clear error message: "Not available for your platform (linux-arm64)"
- I want to know what platforms ARE supported, so I understand the limitation
- I want suggestions on what to do next (contact maintainer, manual build, alternatives)

### As a plugin developer
- I want the store to automatically detect which platforms my plugin supports based on my GitHub release assets
- I want users to see accurate platform information without manual configuration

## Functional Requirements

### 1. Platform Detection

**FR-1.1:** The system must detect the user's current platform (OS and architecture) on application startup
- Detect OS: darwin (macOS), linux, windows, freebsd
- Detect architecture: amd64 (x86-64), arm64, 386

**FR-1.2:** The system must display the user's current platform in the UI (e.g., "Your platform: macOS ARM64" in settings or plugin store)

**FR-1.3:** Platform detection must happen once per session and be cached for performance

### 2. Plugin Registry Platform Metadata

**FR-2.1:** The plugin registry (`plugin_registry_cache.json`) must store platform compatibility information for each plugin

**FR-2.2:** Platform metadata must include:
- Supported OS list (e.g., ["darwin", "linux", "windows"])
- Supported architecture list (e.g., ["amd64", "arm64"])
- Available platform combinations (e.g., ["darwin-amd64", "darwin-arm64", "linux-amd64"])

**FR-2.3:** Platform metadata must be automatically extracted from GitHub release assets when syncing the registry
- Parse release asset filenames (e.g., `plugin-name-v1.0.0-darwin-arm64` → supports darwin-arm64)
- Extract all platform combinations from available binaries

**FR-2.4:** If platform metadata cannot be determined, the plugin must be marked with `platforms: ["unknown"]`

### 3. Pre-Installation Compatibility Check

**FR-3.1:** Before allowing plugin download/installation, the system must check if the plugin supports the user's platform

**FR-3.2:** If the plugin does NOT support the user's platform:
- Block the installation attempt
- Display error modal: "Plugin Not Available for Your Platform"
- Show user's current platform (e.g., "Your platform: linux-arm64")
- Show supported platforms (e.g., "Supported platforms: macOS (amd64, arm64), Linux (amd64)")
- Provide next steps: "Contact the plugin maintainer to request linux-arm64 support"

**FR-3.3:** If the plugin supports the user's platform, proceed with installation as normal

### 4. UI Platform Indicators

**FR-4.1:** Each plugin card in the plugin store must display platform badges showing supported platforms
- Use icons/badges: 🍎 macOS, 🐧 Linux, 🪟 Windows
- Show architecture variants if relevant (e.g., "macOS: Intel & Apple Silicon")

**FR-4.2:** Plugin cards must have a visual indicator for compatibility status:
- Green checkmark ✅ + "Compatible" badge if plugin supports user's platform
- Gray/disabled appearance + "Incompatible" badge if plugin doesn't support user's platform

**FR-4.3:** Hovering over platform badges must show detailed tooltip:
- "Supports: darwin-amd64, darwin-arm64, linux-amd64, linux-arm64, windows-amd64"

### 5. Plugin Filtering

**FR-5.1:** The plugin store must have a filter toggle: "Show only compatible plugins"
- Default: ON (only show compatible plugins)
- When OFF: Show all plugins with clear compatibility indicators

**FR-5.2:** The filter toggle must persist across sessions (saved in user preferences)

**FR-5.3:** When viewing incompatible plugins (filter OFF), clicking "Install" must show the compatibility error (FR-3.2)

### 6. Install Button Behavior

**FR-6.1:** If a plugin is incompatible with the user's platform, the "Install" button must be:
- Disabled (grayed out) by default
- Labeled "Not Available" instead of "Install"
- Show tooltip on hover: "Not available for linux-arm64"

**FR-6.2:** If a plugin is compatible, the "Install" button must work as currently implemented

### 7. Error Messages & Guidance

**FR-7.1:** When installation is blocked due to incompatibility, the error message must include:
- Clear explanation: "This plugin does not have a binary for your platform"
- User's platform: "Your platform: linux-arm64"
- Supported platforms: List of supported OS/arch combinations
- Next steps:
  - "Contact the plugin maintainer to request support for your platform"
  - Link to plugin repository (if available)
  - "Or build the plugin manually from source" (link to docs)

**FR-7.2:** Error messages must be displayed in a modal dialog (not just console logs)

**FR-7.3:** Error messages must be user-friendly (avoid technical jargon where possible)

### 8. Registry Sync Improvements

**FR-8.1:** When syncing the plugin registry from GitHub, the system must:
- Parse each plugin's GitHub release assets
- Extract platform information from asset filenames
- Store platform metadata in `plugin_registry_cache.json`

**FR-8.2:** If a plugin has no release assets (or unparseable filenames), mark it as `platforms: ["unknown"]` and show warning in UI

**FR-8.3:** Registry sync must validate platform metadata and log warnings for plugins with incomplete data

### 9. Manual Plugin Upload Considerations

**FR-9.1:** When a user manually uploads a plugin binary (via "Upload Plugin" feature), the system must:
- Detect the binary's target platform by inspecting the executable
- OR allow user to manually specify the target platform
- Store this platform information in `local_plugin_registry.json`

**FR-9.2:** Manually uploaded plugins must show platform information in the plugin list (same as remote plugins)

### 10. Settings/Diagnostics Page

**FR-10.1:** The settings page must have a "System Information" section showing:
- Current platform: "darwin-arm64"
- Go version (if applicable): "go1.21.0"
- Plugin directory paths

**FR-10.2:** The plugin store page must show platform detection status:
- "Showing plugins for: macOS ARM64"
- Link to refresh platform detection if needed

## Non-Goals (Out of Scope)

1. **Automatic compilation from source** - Not implementing local compilation (requires Go toolchain)
2. **Cross-compilation service** - Not building a server to compile plugins on-demand for missing platforms
3. **Plugin emulation/virtualization** - Not attempting to run incompatible binaries through emulation
4. **Backward compatibility for old binaries** - Not maintaining compatibility with plugins using old naming schemes
5. **Multi-platform binary bundles** - Not packaging multiple platform binaries into single download (fat binaries)
6. **Plugin dependency resolution across platforms** - Not checking if plugin dependencies support the user's platform
7. **Platform-specific features** - Not implementing different feature sets per platform within a single plugin
8. **Automatic fallback to source compilation** - If binary unavailable, don't automatically attempt compilation

## Design Considerations

### UI Mockup Suggestions

**Plugin Card Design:**
```
┌─────────────────────────────────────────┐
│ 📦 Plugin Name                          │
│ Short description of the plugin         │
│                                         │
│ Platforms: 🍎 macOS  🐧 Linux  🪟 Windows│
│            (amd64, arm64)               │
│                                         │
│ ✅ Compatible with your system          │
│ [Install] [View Details]               │
└─────────────────────────────────────────┘
```

**Incompatible Plugin Card:**
```
┌─────────────────────────────────────────┐
│ 📦 Plugin Name                    (gray)│
│ Short description of the plugin         │
│                                         │
│ Platforms: 🍎 macOS  🪟 Windows         │
│            (amd64 only)                 │
│                                         │
│ ⚠️  Not available for linux-arm64       │
│ [Not Available] [View Details]         │
└─────────────────────────────────────────┘
```

**Filter Toggle:**
```
[ ] Show incompatible plugins  (toggle switch)
```

**Error Modal:**
```
┌──────────────────────────────────────────────┐
│ ⚠️  Plugin Not Available                     │
│                                              │
│ This plugin does not support your platform. │
│                                              │
│ Your platform: linux-arm64                   │
│                                              │
│ Supported platforms:                         │
│ • macOS (Intel, Apple Silicon)               │
│ • Windows (64-bit)                           │
│                                              │
│ What you can do:                             │
│ • Contact the plugin maintainer to request   │
│   linux-arm64 support                        │
│ • Build the plugin manually from source      │
│   [View Build Instructions]                  │
│                                              │
│ [Close]                                      │
└──────────────────────────────────────────────┘
```

### Component Updates

**Files to Modify:**
- `internal/web/static/js/modules/plugin-store.js` - Add platform filtering, compatibility checks, UI updates
- `internal/registry/registry.go` - Add platform metadata extraction during sync
- `internal/registry/types.go` - Add `Platforms []string` field to plugin metadata
- `internal/pluginhttp/registry.go` - Add platform compatibility check before download
- `internal/server/server.go` - Add platform detection on startup
- `internal/web/templates/pages/marketplace.tmpl` - Update plugin card HTML to show platforms

**New Files:**
- `internal/platform/detector.go` - Platform detection utility (OS, arch, combined string)
- `internal/platform/detector_test.go` - Unit tests for platform detection

## Technical Considerations

### Platform Detection Implementation

**Use Go's `runtime` package:**
```go
import "runtime"

func DetectPlatform() string {
    return runtime.GOOS + "-" + runtime.GOARCH
}
```

**Supported platforms** (from Go's runtime package):
- darwin-amd64, darwin-arm64
- linux-amd64, linux-arm64, linux-386, linux-arm
- windows-amd64, windows-386, windows-arm64
- freebsd-amd64, freebsd-386, freebsd-arm

### Plugin Registry Schema Update

**Current schema:**
```json
{
  "name": "example-plugin",
  "version": "1.0.0",
  "download_url": "https://github.com/...",
  ...
}
```

**Updated schema:**
```json
{
  "name": "example-plugin",
  "version": "1.0.0",
  "download_url": "https://github.com/.../releases/download/v1.0.0/plugin-{platform}",
  "platforms": ["darwin-amd64", "darwin-arm64", "linux-amd64", "windows-amd64"],
  "os_support": ["darwin", "linux", "windows"],
  "arch_support": ["amd64", "arm64"],
  ...
}
```

**Note:** `download_url` may use `{platform}` placeholder to construct platform-specific URLs

### GitHub Release Asset Parsing

**Asset naming conventions to support:**
- `plugin-name-v1.0.0-darwin-arm64`
- `plugin-name-v1.0.0-darwin-arm64.tar.gz`
- `plugin-name_v1.0.0_darwin_arm64`
- `plugin-name-darwin-arm64` (version in tag, not filename)

**Parsing logic:**
1. Extract all asset names from GitHub release
2. For each asset, use regex to extract platform: `(darwin|linux|windows|freebsd)[-_](amd64|arm64|386|arm)`
3. Deduplicate platform list
4. Store in registry metadata

### Binary Verification (Post-Download)

**Optional enhancement:** After downloading a plugin binary, attempt to execute `plugin --version` to verify:
1. Binary is executable
2. Binary isn't corrupted
3. Binary is for the correct platform (will fail if wrong platform)

If verification fails, delete the binary and show error to user.

### Caching Platform Detection

**Store detected platform in:**
- `app_state.json` or `settings.json`
- OR: Runtime-only cache (re-detect on each app start)

**Recommendation:** Runtime-only cache is simpler and more reliable (system changes are rare)

### Backward Compatibility

**Handling old plugins without platform metadata:**
- If `platforms` field is missing: assume `["unknown"]`
- Show warning in UI: "Platform compatibility unknown"
- Allow user to attempt installation at their own risk
- If installation fails, show helpful error about missing platform metadata

## Success Metrics

### Primary Metrics

1. **Failed installation rate** - Reduce failed plugin installations due to platform incompatibility by 90%
2. **User error clarity** - 100% of incompatible plugin install attempts show clear error messages
3. **Platform metadata coverage** - 95%+ of plugins in registry have accurate platform metadata

### Secondary Metrics

4. **Support ticket reduction** - Reduce support tickets related to "plugin won't install" by 70%
5. **User confidence** - User survey: "I know which plugins will work on my system" - target 90% agree
6. **Installation success rate** - Increase overall plugin installation success rate by 30%

### Measurement Methods

- Track installation attempts vs. successes in usage analytics
- Monitor error logs for platform-related errors
- User survey after 2 weeks of feature rollout
- Support ticket categorization and trend analysis

## Open Questions

1. **Plugin naming conventions:** Should we enforce/recommend a standard naming convention for plugin binaries in GitHub releases? (e.g., `plugin-name-{version}-{platform}`)

2. **Registry sync frequency:** Should platform metadata be updated every time the registry syncs, or only when a new version is released?

3. **Manual verification:** Should we add a feature for plugin maintainers to manually specify supported platforms in plugin metadata (e.g., via a config file in the plugin repo)?

4. **Platform aliases:** Should we support platform aliases? (e.g., "macOS" → "darwin", "Intel" → "amd64", "Apple Silicon" → "arm64")

5. **Partial platform support:** How should we handle plugins that support only some architectures on a given OS? (e.g., Linux amd64 but not Linux arm64)
   - Current approach: Show "Linux (amd64 only)" in UI
   - Alternative: Separate badges for each OS/arch combination

6. **Rosetta compatibility:** Should we consider macOS Intel binaries (darwin-amd64) as "compatible" on Apple Silicon (darwin-arm64) via Rosetta 2 emulation?
   - Recommendation: NO - prefer native arm64 binaries, but allow user to manually install darwin-amd64 if desired

7. **Future: On-demand compilation service:** Should we consider building a cloud service that compiles plugins on-demand for missing platforms?
   - Out of scope for this PRD, but worth considering for future enhancement

8. **Plugin updates:** When a plugin updates to support new platforms, how quickly should the UI reflect this change?
   - Depends on registry sync frequency (currently manual or periodic)

## Priority & Timeline

**Priority:** URGENT - Blocking users from using plugins

**Estimated Effort:**
- Small: Platform detection utility (1-2 hours)
- Medium: Registry schema update and sync logic (4-6 hours)
- Medium: UI updates (plugin cards, badges, filters) (6-8 hours)
- Small: Error modal and messaging (2-3 hours)
- Medium: Testing and edge cases (4-6 hours)

**Total Estimate:** 17-25 hours (2-3 days of development)

**Recommended Phases:**
1. **Phase 1 (MVP):** Platform detection + Pre-install compatibility check + Basic error messages (8-10 hours)
2. **Phase 2:** UI badges, filters, improved error messages (6-8 hours)
3. **Phase 3:** Registry sync improvements, binary verification (optional) (3-7 hours)

## Appendix: Example Platforms

### Common Platform Combinations

| OS | Architecture | Platform String | Common Use Cases |
|---|---|---|---|
| macOS | Intel (x86-64) | darwin-amd64 | Intel Macs (2005-2020) |
| macOS | Apple Silicon (ARM64) | darwin-arm64 | M1/M2/M3 Macs (2020+) |
| Linux | x86-64 | linux-amd64 | Most Linux servers, desktops |
| Linux | ARM64 | linux-arm64 | Raspberry Pi 4+, ARM servers |
| Linux | ARM (32-bit) | linux-arm | Older Raspberry Pi models |
| Windows | x86-64 | windows-amd64 | Most Windows PCs |
| Windows | ARM64 | windows-arm64 | Windows on ARM devices |
| FreeBSD | x86-64 | freebsd-amd64 | BSD servers |

### Platform Detection Examples

```go
// Example platform detection output
DetectPlatform() // Returns: "darwin-arm64"

// Example registry metadata
{
  "platforms": ["darwin-amd64", "darwin-arm64", "linux-amd64"],
  "is_compatible": true  // For current user's platform
}

// Example compatibility check
IsCompatible("darwin-arm64", []string{"darwin-amd64", "darwin-arm64"}) // true
IsCompatible("linux-arm64", []string{"darwin-amd64", "darwin-arm64"}) // false
```

---

**Document Version:** 1.0
**Last Updated:** 2025-11-11
**Author:** Generated via Claude Code
**Status:** Draft - Awaiting Review
