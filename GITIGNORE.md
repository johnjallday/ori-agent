# Git Ignore Configuration

This document describes the .gitignore setup for the dolphin-agent project.

## Files Created

### Main Project
- **`.gitignore`** - Main gitignore for dolphin-agent project
- **`../dolphin/.gitignore`** - Updated parent directory gitignore

### Plugins
- **`plugins/math/.gitignore`** - Math plugin specific ignores
- **`plugins/weather/.gitignore`** - Weather plugin specific ignores  
- **`plugins/result-handler/.gitignore`** - Result handler plugin specific ignores

### External Projects
- **`../dolphin-reaper/.gitignore`** - External REAPER plugin project

## Key Patterns Ignored

### Build Artifacts
- `bin/` - Compiled binaries
- `*.so` - Plugin shared libraries
- `dist/` - Distribution builds
- `build/` - Build artifacts

### Development Files
- `.claude/settings.local.json` - Local Claude settings
- `.DS_Store` - macOS system files
- `.vscode/` - VS Code settings
- `.idea/` - JetBrains IDE settings

### Runtime Files
- `plugin_cache/` - Plugin cache directory
- `uploaded_plugins/*.so` - Uploaded plugin binaries
- `*.log` - Log files
- `tmp/` - Temporary files

### Configuration
- `.env*` - Environment files
- `agents.json` - Runtime agent configuration
- Local development configs

## Files Removed from Tracking

The following files were removed from git tracking:
- `.DS_Store`
- `.claude/settings.local.json`
- `bin/server`
- `bin/agents.json`
- `plugins/*/*.so` (all plugin binaries)

## Best Practices

1. **Plugin Development**: Each plugin has its own .gitignore to ensure binaries aren't tracked
2. **Build Artifacts**: All compiled binaries and build outputs are ignored
3. **Local Settings**: Personal/local configuration files are not tracked
4. **Runtime Files**: Cache and temporary files are ignored
5. **IDE Files**: Editor-specific files are ignored for better collaboration

## Usage

- Plugin binaries are built locally and not committed
- Local settings remain private to each developer
- Build artifacts can be regenerated from source
- Clean repository with only source code tracked