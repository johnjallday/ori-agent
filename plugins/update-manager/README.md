# 🔄 Update Manager Plugin

A comprehensive update management plugin for Dolphin Agent that checks GitHub releases for software updates and manages the update process.

![Updates](https://img.shields.io/badge/Updates-GitHub%20Releases-4f46e5)
![Cross Platform](https://img.shields.io/badge/Platform-Cross%20Platform-10b981)
![Plugin](https://img.shields.io/badge/Plugin-Dolphin%20Agent-6366f1)

## ✨ Features

### 🔍 **Update Checking**
- **GitHub Integration**: Connects to GitHub Releases API for update information
- **Version Comparison**: Smart comparison between current and available versions
- **Prerelease Support**: Option to include or exclude prerelease versions
- **Structured Output**: Beautiful formatted results with release details

### 📦 **Release Management**
- **List All Releases**: View complete release history with details
- **Release Notes**: Access full changelog and release descriptions
- **Asset Information**: See available download assets and file sizes
- **Platform Detection**: Identifies compatible downloads for your platform

### ⬇️ **Download & Install**
- **Secure Downloads**: Downloads releases directly from GitHub
- **Platform Matching**: Automatically selects correct binary for your OS/architecture
- **Safety First**: Downloads to temporary directory for manual verification
- **Progress Tracking**: Clear feedback during download process

### 🛡️ **Security & Safety**
- **No Auto-Install**: Downloads only, manual installation required for security
- **Temporary Storage**: Uses system temp directory for downloads
- **Verification**: Encourages manual verification before installation
- **Error Handling**: Comprehensive error checking and user feedback

## 🎯 Usage Examples

### Check for Updates
```
"Check for updates"
"Are there any new versions available?"
"What's the latest version?"
```

**Response:**
```
🔄 Update Check Results
Current Version: v1.0.0
Latest Version: v1.2.0
Status: Update Available ✨
Release Date: 2024-01-15
Assets: 3 files available
```

### List All Releases
```
"List all releases"
"Show me the release history"
"What versions are available?"
```

**Response:**
```
📦 Available Releases (12 found)
Repository: johnjallday/dolphin-agent

| Version | Name | Date | Assets |
|---------|------|------|--------|
| v1.2.0 | Major Update | 2024-01-15 | 3 |
| v1.1.5 | Bug Fixes | 2024-01-10 | 3 |
| v1.1.0 | Feature Release | 2024-01-05 | 3 |
```

### Install Specific Version
```
"Install version v1.2.0"
"Download the latest update"
"Install update v1.1.5"
```

**Response:**
```
✅ Downloaded update v1.2.0 to /tmp/dolphin-update-123/dolphin-agent-v1.2.0-darwin-amd64.tar.gz
⚠️  Manual installation required for security
```

### Include Prerelease Versions
```
"Check for updates including prereleases"
"List releases including beta versions"
```

## 🏗️ Function Definition

The plugin exposes a single function `update_manager` with the following parameters:

```json
{
  "name": "update_manager",
  "description": "Check for software updates from GitHub releases and manage update installation",
  "parameters": {
    "action": {
      "type": "string",
      "enum": ["check_updates", "list_releases", "install_update", "get_current_version"],
      "description": "Action to perform"
    },
    "version": {
      "type": "string",
      "description": "Specific version to install (required for install_update)"
    },
    "include_prerelease": {
      "type": "boolean",
      "description": "Include pre-release versions (default: false)"
    }
  }
}
```

## 🔧 Configuration

### Repository Settings
The plugin is pre-configured for the Dolphin Agent repository:
- **Owner**: `johnjallday`
- **Repository**: `dolphin-agent`
- **Current Version**: `v1.0.0` (can be updated)

### Version Detection
The plugin uses the GitHub Releases API to fetch version information:
- **URL**: `https://api.github.com/repos/johnjallday/dolphin-agent/releases`
- **Filtering**: Excludes draft releases, optionally includes prereleases
- **Sorting**: Shows newest releases first

### Platform Compatibility
Automatic platform detection for downloads:

| Platform | Supported Identifiers |
|----------|----------------------|
| **macOS** | `darwin`, `macos`, `osx` |
| **Linux** | `linux` |
| **Windows** | `windows`, `win` |

| Architecture | Supported Identifiers |
|--------------|----------------------|
| **x86_64** | `amd64`, `x86_64`, `x64` |
| **ARM64** | `arm64`, `aarch64` |

## 📊 Structured Data Output

The plugin returns beautifully formatted structured data for:

### Update Check Results
```json
{
  "type": "update_check",
  "title": "🔄 Update Check Results",
  "currentVersion": "v1.0.0",
  "latestVersion": "v1.2.0",
  "updateAvailable": true,
  "releaseDate": "2024-01-15T10:30:00Z",
  "releaseNotes": "Major feature update with...",
  "assets": [
    {"name": "dolphin-agent-darwin-amd64.tar.gz", "size": 15728640}
  ],
  "repository": "johnjallday/dolphin-agent"
}
```

### Releases List
```json
{
  "type": "releases_list",
  "title": "📦 Available Releases",
  "repository": "johnjallday/dolphin-agent",
  "count": 12,
  "releases": [
    {
      "version": "v1.2.0",
      "name": "Major Update",
      "date": "2024-01-15T10:30:00Z",
      "prerelease": false,
      "assetCount": 3,
      "description": "This release includes..."
    }
  ]
}
```

## 🚀 Installation

### 1. Build the Plugin
```bash
cd plugins/update-manager
go mod tidy
go build -buildmode=plugin -o update-manager.so main.go
```

### 2. Upload to Dolphin Agent
- Start your Dolphin Agent server
- Open the web interface (http://localhost:8080)
- Go to **Plugins** tab in the sidebar
- Upload `update-manager.so` using the file input
- Click **Load** to activate the plugin

### 3. Verify Installation
```bash
curl http://localhost:8080/api/plugins
```

Expected response:
```json
{
  "plugins": [
    {
      "description": "Check for software updates from GitHub releases and manage update installation",
      "name": "update_manager"
    }
  ]
}
```

## 📝 API Reference

### Check Updates
```json
{
  "action": "check_updates",
  "include_prerelease": false
}
```

### List Releases
```json
{
  "action": "list_releases",
  "include_prerelease": false
}
```

### Install Update
```json
{
  "action": "install_update",
  "version": "v1.2.0"
}
```

### Get Current Version
```json
{
  "action": "get_current_version"
}
```

## 🛡️ Security Considerations

### Download Safety
- **Temporary Storage**: All downloads go to system temp directory
- **Manual Installation**: No automatic installation for security
- **User Verification**: Encourages manual verification of downloads
- **No Execution**: Plugin never executes downloaded files

### API Security
- **Read-Only**: Only reads from GitHub API, no write operations
- **Public Data**: Only accesses public release information
- **No Authentication**: Uses public GitHub API endpoints
- **Rate Limiting**: Respects GitHub API rate limits

### File System
- **Temporary Files**: Uses `os.MkdirTemp()` for secure temp directories
- **Cleanup**: Automatically removes temp files after operation
- **No System Modification**: Downloads only, no system changes

## 🔍 Troubleshooting

### "Failed to fetch releases"
**Common Causes:**
- Network connectivity issues
- GitHub API rate limiting
- Repository doesn't exist or is private
- Invalid repository configuration

**Solutions:**
```bash
# Test GitHub API directly
curl https://api.github.com/repos/johnjallday/dolphin-agent/releases

# Check network connectivity
ping api.github.com
```

### "No compatible asset found"
**Common Causes:**
- Release doesn't have assets for your platform
- Asset naming doesn't match platform detection patterns
- Only source code releases available

**Solutions:**
- Check release page manually on GitHub
- Look for different naming patterns in assets
- Contact repository maintainer for platform support

### "Version not found"
**Common Causes:**
- Typo in version number
- Version is a draft release
- Version doesn't exist in repository

**Solutions:**
- List all releases first to see available versions
- Check exact version format (e.g., "v1.2.0" vs "1.2.0")
- Verify version exists on GitHub releases page

### "Download failed"
**Common Causes:**
- Network interruption
- Asset URL expired or changed
- Insufficient disk space in temp directory

**Solutions:**
- Retry the download
- Check available disk space
- Try downloading manually from GitHub

## 🎯 Future Enhancements

- [ ] **Automatic Version Detection**: Read current version from build info
- [ ] **Semantic Version Parsing**: Proper semver comparison
- [ ] **Backup Creation**: Backup current version before update
- [ ] **Rollback Support**: Easy rollback to previous version
- [ ] **Update Notifications**: Proactive update checking
- [ ] **Custom Repositories**: Support for other GitHub repositories
- [ ] **Progress Bars**: Real-time download progress
- [ ] **Delta Updates**: Incremental updates for large binaries

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Update documentation
5. Submit a pull request

### Development Guidelines
- Follow Go best practices
- Add comprehensive error handling
- Test with different repository configurations
- Update README for new features
- Maintain security-first approach

## 📄 License

This project is licensed under the MIT License - see the main project LICENSE file for details.

## 🙏 Acknowledgments

- [GitHub API](https://docs.github.com/en/rest) for release information
- Go standard library for HTTP and JSON handling
- Dolphin Agent community for feature requests and feedback
- Open source projects for inspiration on update mechanisms

---

**Made with ❤️ for seamless software updates**