# Plugin Registry Configuration

This document describes how to configure plugins in the registry for cross-platform support.

## Registry File Locations

- **GitHub Registry**: `https://raw.githubusercontent.com/user/repo/main/plugin_registry.json`
- **Local Registry**: `local_plugin_registry.json`
- **Embedded Registry**: `internal/web/static/plugin_registry.json`

## Plugin Entry Formats

### 1. Local Plugin (RPC Executable)

```json
{
  "name": "weather",
  "description": "Get weather for a given location",
  "path": "plugins/weather/weather",
  "version": "1.0.0"
}
```

**Notes:**
- `path` points to the executable (no extension needed - system will detect `.exe` on Windows)
- Best for bundled plugins included with the application

### 2. Remote Plugin with Platform-Specific URLs

```json
{
  "name": "my-plugin",
  "description": "My awesome plugin",
  "version": "1.2.3",
  "download_url": "https://github.com/user/repo/releases/download/v{version}/my-plugin-{os}-{arch}",
  "checksum": "sha256_hash_here",
  "auto_update": true,
  "github_repo": "user/repo"
}
```

**Placeholders:**
- `{version}` - Replaced with the version field
- `{os}` - Replaced with `darwin`, `linux`, or `windows`
- `{arch}` - Replaced with `amd64` or `arm64`

**Example URLs Generated:**
- macOS Intel: `https://github.com/user/repo/releases/download/v1.2.3/my-plugin-darwin-amd64`
- macOS ARM: `https://github.com/user/repo/releases/download/v1.2.3/my-plugin-darwin-arm64`
- Linux: `https://github.com/user/repo/releases/download/v1.2.3/my-plugin-linux-amd64`
- Windows: `https://github.com/user/repo/releases/download/v1.2.3/my-plugin-windows-amd64.exe`

## Complete Example Registry

```json
{
  "plugins": [
    {
      "name": "math",
      "description": "Perform basic math operations: add, subtract, multiply, divide",
      "path": "plugins/math/math",
      "version": "1.0.0"
    },
    {
      "name": "weather",
      "description": "Get weather for a given location",
      "path": "plugins/weather/weather",
      "version": "1.0.0"
    },
    {
      "name": "external-tool",
      "description": "Example external plugin with auto-update",
      "version": "2.1.0",
      "download_url": "https://github.com/user/plugin-repo/releases/download/v{version}/external-tool-{os}-{arch}",
      "checksum": "abcdef1234567890...",
      "auto_update": true,
      "github_repo": "user/plugin-repo"
    }
  ]
}
```

## File Extension Handling

The system automatically handles file extensions based on platform:

| Platform | Extension | Example |
|----------|-----------|---------|
| Linux | (none) | `my-plugin` |
| macOS | (none) | `my-plugin` |
| Windows | `.exe` | `my-plugin.exe` |

## Caching

Downloaded plugins are cached in `plugin_cache/` with platform-specific names:

- `my-plugin-1.2.3-darwin-amd64`
- `my-plugin-1.2.3-linux-amd64`
- `my-plugin-1.2.3-windows-amd64.exe`

## Auto-Update Behavior

When `auto_update: true`:
- Plugin is re-downloaded if cached version is older than 1 hour
- Checksum is verified on every download
- Updates are checked when loading the plugin

## GitHub Release Integration

For plugins hosted on GitHub:

1. Build for all platforms using `make plugins-cross` or `./scripts/build-plugins-cross.sh`
2. Create a GitHub release with version tag (e.g., `v1.2.3`)
3. Upload all platform binaries to the release
4. Set `download_url` with placeholders in the registry
5. Set `github_repo` to enable GitHub API integration

Example release assets:
```
my-plugin-darwin-amd64
my-plugin-darwin-arm64
my-plugin-linux-amd64
my-plugin-linux-arm64
my-plugin-windows-amd64.exe
```
