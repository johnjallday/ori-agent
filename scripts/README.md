# Build Scripts

This directory contains shell scripts for building the ori-agent project and its plugins.

## Main Build Scripts

### `build.sh` - Complete Project Build
```bash
./scripts/build.sh
```
Builds the main server binary and all plugins. This is the primary build script for development.

**Features:**
- Builds server binary with version information
- Builds all plugins automatically
- Embeds version, git commit, and build date
- Cross-platform compatible
- Colored output for easy reading

### `build-server.sh` - Server Only Build
```bash
./scripts/build-server.sh
```
Builds just the main server binary without plugins for faster iterations.

**Features:**
- Faster builds when plugins haven't changed
- Same version embedding as full build
- Outputs to `bin/` directory

### `build-release.sh` - Cross-Platform Release Build
```bash
./scripts/build-release.sh
```
Builds release binaries for multiple platforms (Linux, macOS, Windows) with both amd64 and arm64 architectures.

**Features:**
- Cross-compilation for multiple platforms
- Creates compressed archives for each platform
- Includes plugins in each platform build
- Outputs to `dist/` directory

## Plugin Build Scripts

### `build-plugins.sh` - Build All Plugins
```bash
./scripts/build-plugins.sh
```
Builds all plugins in the `plugins/` directory and outputs them to `uploaded_plugins/`.

**Features:**
- Builds all plugins automatically
- Creates output directory if needed
- Colored output for easy reading
- Exits on any build error
- Lists all built plugins at completion

### `build-plugin.sh` - Build Single Plugin
```bash
./scripts/build-plugin.sh <plugin-name>

# Examples:
./scripts/build-plugin.sh weather
./scripts/build-plugin.sh math
./scripts/build-plugin.sh result-handler
```
Builds a specific plugin by name.

**Features:**
- Auto-detects source file (`main.go` or `<plugin-name>.go`)
- Shows available plugins if invalid name provided
- Detailed build output and error reporting

### `clean-plugins.sh` - Clean Plugin Binaries
```bash
./scripts/clean-plugins.sh
```
Removes all compiled `.so` files from the project.

**Features:**
- Cleans `uploaded_plugins/` directory
- Removes `.so` files from individual plugin directories
- Safe operation - only removes plugin binaries

### `clean-agents.sh` - Clean Stale Agent Configuration
```bash
./scripts/clean-agents.sh
```
Checks and reports stale plugin paths in `agents.json`.

**Features:**
- Detects temporary/stale plugin paths
- Checks for non-existent plugin files
- Creates backup before any changes
- Provides guidance for manual fixes

### `build-external-plugins.sh` - Build External Plugins
```bash
./scripts/build-external-plugins.sh
```
Builds plugins that are in separate repositories or directories outside the main project.

**Features:**
- Builds plugins from external directories (like `../dolphin-reaper`)
- Uses plugin's own build script if available
- Falls back to direct Go build if `main.go` exists
- Copies built plugins to `uploaded_plugins/` directory

## Usage Examples

```bash
# Complete project build (server + plugins)
./scripts/build.sh

# Server only (faster for development)
./scripts/build-server.sh

# Cross-platform release build
./scripts/build-release.sh

# Plugin management
./scripts/build-plugins.sh    # Build all plugins
./scripts/build-plugin.sh weather  # Build specific plugin
./scripts/clean-plugins.sh    # Clean plugin binaries

# Typical development workflow
./scripts/clean-plugins.sh    # Clean old builds
./scripts/build.sh            # Build everything
./bin/ori-agent          # Run the server

# Release workflow
./scripts/clean-plugins.sh    # Clean old builds
./scripts/build-release.sh    # Build for all platforms
```

## Environment Variables

### Build Configuration
- `BUILD_PLUGINS=false` - Skip plugin builds in main build script
- `BUILD_EXTERNAL_PLUGINS=false` - Skip external plugin builds
- `GOOS=linux` - Target operating system for build
- `GOARCH=amd64` - Target architecture for build

### Examples
```bash
# Build without plugins
BUILD_PLUGINS=false ./scripts/build.sh

# Build plugins but skip external plugins
BUILD_EXTERNAL_PLUGINS=false ./scripts/build.sh

# Cross-compile for Linux
GOOS=linux GOARCH=amd64 ./scripts/build-server.sh

# Build for Windows
GOOS=windows GOARCH=amd64 ./scripts/build-server.sh
```

## Requirements

- Go compiler installed
- Run from project root directory
- Plugins must be in `plugins/` subdirectories
- Each plugin needs either `main.go` or `<plugin-name>.go`

## Notes

- All scripts use colored output for better visibility
- Scripts are designed to be run from the project root
- Build failures will stop execution and show clear error messages
- All scripts are executable and ready to use