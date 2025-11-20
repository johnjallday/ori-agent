# Changelog

All notable changes to Ori Agent will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

#### Plugin Optimization APIs 🚀

Three powerful new APIs that dramatically simplify plugin development:

**1. YAML-Based Tool Definitions (70% code reduction)**
- Define tool parameters in `plugin.yaml` instead of code
- Automatic JSON Schema generation from YAML configuration
- Support for all parameter types: string, integer, number, boolean, enum, array, object
- Built-in validation rules: min, max, required, default, enum values
- New `GetToolDefinition()` method on `BasePlugin` reads from plugin.yaml
- Fallback mechanism if YAML tool definition is not provided

**2. Settings API**
- Simple key-value storage for plugin configuration
- Thread-safe with `sync.RWMutex` for concurrent access
- Type-safe getters: `GetString()`, `GetInt()`, `GetBool()`, `GetFloat()`
- Atomic file writes using temp file + rename pattern for data safety
- In-memory caching for performance optimization
- Per-agent isolation - each agent has separate plugin settings
- Settings stored in `agents/<agent>/plugins/<plugin>/settings.json`
- New `Settings()` method on `BasePlugin` provides lazy-initialized settings manager

**3. Template Rendering API**
- Serve web pages using Go's `html/template` package
- `RenderTemplate(templateFS, templateName, data)` function for easy template rendering
- Template caching for performance - parse once, render many times
- Automatic XSS protection with HTML escaping
- Support for `embed.FS` for embedding templates in plugin binaries
- Clean separation of HTML from Go code

#### Documentation & Examples

- Added `PLUGIN_OPTIMIZATION_GUIDE.md` - Complete migration guide with:
  - Step-by-step migration instructions
  - Full YAML parameter schema reference
  - Settings API usage examples
  - Template Rendering API guide
  - Before/After code comparisons showing 70-90% reduction
  - Troubleshooting section for common issues
  - No breaking changes - all APIs are opt-in

- Added `example_plugins/minimal/` - Minimal plugin demonstrating:
  - YAML-only configuration
  - Settings API usage
  - Simplified tool definition (~200 lines total)

- Added `example_plugins/webapp/` - Web application plugin demonstrating:
  - Template rendering with embedded templates
  - WebPageProvider interface implementation
  - Data persistence using Settings API
  - Beautiful responsive dashboard UI (~250 lines total)

- Updated `CLAUDE.md` with new "Plugin Optimization APIs" section
- Updated `README.md` with plugin development overview and new APIs

### Changed

#### Migrated Plugins

- **ori-reaper**: Migrated to use new optimization APIs
  - Tool definition moved from code to `plugin.yaml` (70% code reduction)
  - HTML extracted to `templates/marketplace.html`
  - Uses `RenderTemplate()` for template rendering
  - Simplified `Definition()` method from ~30 lines to ~10 lines

- **ori-music-project-manager**: Migrated to use new optimization APIs
  - Tool definition moved from code to `plugin.yaml` (70% code reduction)
  - Simplified `Definition()` method from ~40 lines to ~12 lines
  - BPM constraints defined in YAML (min: 30, max: 300)

### Fixed

- Fixed enum support for string type parameters in `tool_definition.go`
  - String parameters with enum field now correctly include enum constraint in generated schema
  - Previously only explicit "enum" type had enum values populated

### Technical Details

#### New Files

- `pluginapi/integration_test.go` - Comprehensive end-to-end tests for all three APIs
- `example_plugins/minimal/` - Complete working example plugin
- `example_plugins/webapp/` - Complete web application example plugin
- `PLUGIN_OPTIMIZATION_GUIDE.md` - Complete migration guide
- `CHANGELOG.md` - This file

#### Modified Files

- `pluginapi/tool_definition.go` - Added enum support for string parameters
- `CLAUDE.md` - Added Plugin Optimization APIs documentation
- `README.md` - Added Plugin Development section

#### Test Results

- All unit tests pass (settings, tool definitions, templates)
- All integration tests pass (end-to-end workflows, concurrent access)
- Race detector shows no race conditions (`go test -race ./pluginapi/...`)
- All migrated plugins build successfully
- Pre-commit hooks pass on all repositories

#### Compatibility

- **No breaking changes** - All new APIs are opt-in
- Existing plugins continue to work without modification
- Plugins can gradually migrate to new APIs at their own pace
- Fallback mechanisms ensure graceful degradation if YAML config missing

---

## [0.0.10] - Previous Release

(Previous changelog entries would go here)
