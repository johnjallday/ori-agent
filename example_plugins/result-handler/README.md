# 🔧 Result Handler Plugin

A powerful utility plugin for Ori Agent that enables users to interact with chat results by opening directories, files, and URLs directly from conversation context.

![Actions](https://img.shields.io/badge/Actions-File%20System-4f46e5)
![Cross Platform](https://img.shields.io/badge/Platform-Cross%20Platform-10b981)
![Plugin](https://img.shields.io/badge/Plugin-Ori%20Agent-6366f1)

## ✨ Features

### 📁 **Directory Operations**
- **Open in Finder/Explorer**: Open directories in the system file manager
- **Cross-platform Support**: Works on macOS (Finder), Windows (Explorer), and Linux (nautilus/dolphin/etc.)
- **Context Awareness**: Remembers what triggered the action for better user experience

### 📄 **File Operations**
- **Open Files**: Launch files with their default system applications
- **Smart Handling**: Automatically uses the appropriate system command for file opening
- **Error Handling**: Provides clear feedback when files cannot be opened

### 🌐 **URL Operations**
- **Open URLs**: Launch URLs in the default web browser
- **Protocol Support**: Handles http, https, and file protocols
- **Auto-prefix**: Automatically adds https:// prefix when missing

## 🎯 Usage Examples

### Open REAPER Scripts Directory
After listing REAPER scripts, you can say:
```
"Open the REAPER scripts directory in Finder"
"Show me the scripts folder"
"Open scripts directory"
```

**Response:**
```
📁 Opened directory in Finder: /Users/username/Library/Application Support/REAPER/Scripts (reaper_scripts)
```

### Open Configuration Files
```
"Open the config file"
"Show me the settings file in the editor"
```

### Open URLs
```
"Open the REAPER website"
"Launch the documentation URL"
"Show me the GitHub repository"
```

## 🏗️ Function Definition

The plugin exposes a single function `result_handler` with the following parameters:

```json
{
  "name": "result_handler",
  "description": "Handle actions on chat results like opening directories, files, or URLs",
  "parameters": {
    "action": {
      "type": "string",
      "enum": ["open_directory", "open_file", "open_url", "reveal_in_finder"],
      "description": "Action to perform"
    },
    "path": {
      "type": "string",
      "description": "File path, directory path, or URL to open"
    },
    "context": {
      "type": "string",
      "description": "Optional context about what triggered this action"
    }
  }
}
```

## 🔧 Platform-Specific Commands

### macOS
| Action | Command Used |
|--------|--------------|
| **Open Directory** | `open /path/to/directory` |
| **Open File** | `open /path/to/file` |
| **Open URL** | `open https://example.com` |

### Windows
| Action | Command Used |
|--------|--------------|
| **Open Directory** | `explorer C:\path\to\directory` |
| **Open File** | `cmd /c start "" "C:\path\to\file"` |
| **Open URL** | `cmd /c start "" "https://example.com"` |

### Linux
| Action | Command Used | Fallback Order |
|--------|--------------|----------------|
| **Open Directory** | File manager detection | nautilus → dolphin → thunar → pcmanfm → xdg-open |
| **Open File** | `xdg-open /path/to/file` | - |
| **Open URL** | `xdg-open https://example.com` | - |

## 🚀 Installation

### 1. Build the Plugin
```bash
cd plugins/result-handler
go mod tidy
go build -buildmode=plugin -o result-handler.so main.go
```

### 2. Upload to Ori Agent
- Start your Ori Agent server
- Open the web interface (http://localhost:8765)
- Go to **Plugins** tab in the sidebar
- Upload `result-handler.so` using the file input
- Click **Load** to activate the plugin

### 3. Verify Installation
```bash
curl http://localhost:8765/api/plugins
```

Expected response:
```json
{
  "plugins": [
    {
      "description": "Handle actions on chat results like opening directories, files, or URLs",
      "name": "result_handler"
    }
  ]
}
```

## 📝 API Reference

### Open Directory
```json
{
  "action": "open_directory",
  "path": "/Users/username/Documents",
  "context": "project_files"
}
```

### Open File
```json
{
  "action": "open_file",
  "path": "/Users/username/config.json",
  "context": "configuration"
}
```

### Open URL
```json
{
  "action": "open_url",
  "path": "https://github.com/johnjallday/ori-agent",
  "context": "documentation"
}
```

## 🎨 Integration Examples

### With REAPER Plugin
After using the REAPER plugin to list scripts, users can naturally say:
```
User: "List my REAPER scripts"
AI: [Shows structured table of scripts]
User: "Open the scripts folder"
AI: [Uses result_handler to open the directory]
```

### With Configuration Management
```
User: "Show me my app settings"
AI: [Displays configuration]
User: "Open the config file to edit it"
AI: [Uses result_handler to open file in default editor]
```

### With Documentation
```
User: "How do I use this feature?"
AI: [Provides explanation]
User: "Show me the online documentation"
AI: [Uses result_handler to open docs URL]
```

## 🛡️ Security & Safety

### Path Validation
- **No Path Traversal**: Paths are used as provided without modification
- **System Commands**: Uses standard system commands for file operations
- **Error Handling**: Graceful failure with informative error messages

### Supported Protocols
- **HTTP/HTTPS**: Web URLs
- **File**: Local file URLs
- **No Arbitrary Execution**: Only uses predefined system commands

## 🔍 Troubleshooting

### "No supported file manager found on Linux"
**Solution:** Install a supported file manager:
```bash
# Ubuntu/Debian
sudo apt install nautilus

# Fedora
sudo dnf install nautilus

# Arch
sudo pacman -S nautilus
```

### "Failed to open directory"
**Common Causes:**
- Directory doesn't exist
- No permission to access directory
- File manager not installed or in PATH

### "Failed to open file"
**Common Causes:**
- File doesn't exist
- No default application associated with file type
- File permissions issue

### "Failed to open URL"
**Common Causes:**
- No default browser configured
- Network connectivity issues
- Invalid URL format

## 🎯 Future Enhancements

- [ ] **Custom Applications**: Allow specifying which app to open files with
- [ ] **Bulk Operations**: Handle multiple files/directories at once
- [ ] **Recent Items**: Track recently opened items
- [ ] **Favorites**: Save frequently accessed paths
- [ ] **Preview Mode**: Show file contents without opening
- [ ] **Integration Hooks**: Connect with other plugins for enhanced workflows

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Update documentation
5. Submit a pull request

### Development Guidelines
- Follow Go best practices
- Add comprehensive error handling
- Test on multiple platforms
- Update README for new features
- Maintain backward compatibility

## 📄 License

This project is licensed under the MIT License - see the main project LICENSE file for details.

## 🙏 Acknowledgments

- Cross-platform file operations inspired by various open source projects
- Thanks to the Ori Agent community for feature requests and feedback
- Platform-specific implementations based on standard system practices

---

**Made with ❤️ for seamless file system integration**
