# <img src="assets/logo.svg" alt="Ori Agent logo" width="48" height="48" align="middle" /> Ori Agent

<!-- AUTO:VERSION -->
![Version](https://img.shields.io/badge/Version-v0.0.33-blue)
<!-- AUTO:VERSION_END -->
<!-- AUTO:GO_VERSION -->
![Go](https://img.shields.io/badge/Go-1.25.5-00add8)
<!-- AUTO:GO_VERSION_END -->

**Ori Agent** is a local AI agent management platform. Spin up multiple named agents, each with its own model, prompt, and tool loadout, and run them through a browser UI or API. Agents call plugins (gRPC tools) to act—everything stays on your machine unless you opt into cloud LLMs.

If you want to keep your information local, this is a way to go.

Honestly, everybody is chasing for the AGI, and this is a budget friendly version of "smarter" - AI agent that may or may not lead to AGI.

I don't plan on promoting this until Q426 or Q127 (This date may change).
If you are here early, then welcome! Hope you enjoy this. 
Let me know, how this is.


## 🤖 Supported Providers

Ori Agent supports multiple AI providers, giving you flexibility in choosing your preferred AI model:

### Cloud Providers
- **OpenAI**
  - Requires: `OPENAI_API_KEY`
  - Best for: Production use, latest models, reliable performance

- **Anthropic Claude**
  - Requires: `ANTHROPIC_API_KEY`
  - Best for: Long context windows, detailed reasoning

### Local Providers
- **Ollama** - Run models locally on your machine
  - Requires: Ollama installed and running (http://localhost:11434)
  - Best for: Privacy, offline use, cost savings
  - Supports: Llama 3, Mistral, Phi-3, and other Ollama models

## 🚀 Quick Start

### Prerequisites
- Go 1.25 or later
- An API key from one of the supported providers (OpenAI, Claude) **OR** Ollama installed locally

### Installation

#### Option 1: macOS DMG Installer (Recommended for macOS)

1. **Download the DMG** from the [latest release](https://github.com/johnjallday/ori-agent/releases/latest):
   - **Apple Silicon**: `OriAgent-{version}-arm64.dmg`
   - **Intel Macs**: `OriAgent-{version}-amd64.dmg`

2. **Open the DMG** and drag `OriAgent.app` to Applications

3. **Handle macOS Security Warning**

   When first opening OriAgent, macOS may show one of these warnings:
   - *"OriAgent is damaged and can't be opened"* (most common)
   - *"Apple cannot verify OriAgent is free of malware"*

   **This is normal for open-source apps not notarized by Apple.** To install safely:

   **Method 1 - Right-Click (Easiest):**
   1. Drag `OriAgent.app` to Applications folder
   2. Right-click (or Control+click) `OriAgent.app` in Applications
   3. Select "Open" from the menu
   4. Click "Open" in the dialog that appears

   **Method 2 - Terminal Command:**
   ```bash
   xattr -rc /Applications/OriAgent.app
   open /Applications/OriAgent.app
   ```

   After the first launch, you can open normally by double-clicking.

4. **Configure your API key** through the Settings panel in the app, or export it:
   ```bash
   export OPENAI_API_KEY="your-api-key"
   ```

5. **Access the interface** at `http://localhost:8765`

#### Option 2: Build from Source

1. **Clone the repository**
   ```bash
   git clone https://github.com/johnjallday/ori-agent.git
   cd ori-agent
   ```

2. **Install dependencies**
   ```bash
   go mod tidy
   ```

3. **Set up your API key** (choose one)
   ```bash
   # For OpenAI
   export OPENAI_API_KEY="your-openai-api-key"

   # For Claude
   export ANTHROPIC_API_KEY="your-anthropic-api-key"

   # For Ollama - just make sure it's running
   # No API key needed!
   ```

4. **Build and run**
   ```bash
   ./scripts/build.sh
   ./bin/ori-agent
   ```

5. **Open your browser**
   ```
   http://localhost:8765
   ```

#### Option 3: Linux Package Installer

1. **Download the package** from the [latest release](https://github.com/johnjallday/ori-agent/releases/latest):

   **Debian/Ubuntu:**
   ```bash
   # Download .deb file
   wget https://github.com/johnjallday/ori-agent/releases/latest/download/ori-agent_{version}_amd64.deb

   # Install
   sudo dpkg -i ori-agent_{version}_amd64.deb
   ```

   **Red Hat/Fedora/CentOS:**
   ```bash
   # Download .rpm file
   wget https://github.com/johnjallday/ori-agent/releases/latest/download/ori-agent_{version}_amd64.rpm

   # Install
   sudo rpm -i ori-agent_{version}_amd64.rpm
   ```

2. **Configure API key** via environment variable or `/etc/ori-agent/settings.json`

3. **Start the service**:
   ```bash
   sudo systemctl start ori-agent
   sudo systemctl enable ori-agent  # Auto-start on boot
   ```

4. **Access the interface** at `http://localhost:8765`

#### Option 4: Windows Archive

1. **Download** `ori-agent_{version}_windows_x86_64.tar.gz` from the [latest release](https://github.com/johnjallday/ori-agent/releases/latest)

2. **Extract** the archive to a folder (e.g., `C:\Program Files\OriAgent`)

3. **Set API key** via Environment Variables or create `settings.json` in the same folder

4. **Run** `ori-agent.exe`

5. **Access the interface** at `http://localhost:8765`

## 💬 Session Management

Ori Agent includes a comprehensive session management system for organizing and managing your chat conversations.

### Features

- **Persistent Chat Sessions**: All conversations are automatically saved and can be resumed anytime
- **Folder Organization**: Group related sessions into folders for better organization
- **Multi-Tab Support**: Work with multiple sessions simultaneously in separate browser tabs
- **Full-Text Search**: Quickly find messages across all your sessions
- **Tagging System**: Add tags to sessions for easy categorization and filtering
- **Automatic Cleanup**: Optionally clean up old, inactive sessions to manage storage

### Session Storage

Sessions are stored in a SQLite database with an in-memory LRU cache for fast access:

- **Cache**: Recently accessed sessions are kept in memory for instant retrieval
- **Database**: All sessions are persisted to SQLite with full-text search support
- **Hybrid Architecture**: Automatic cache warming and write-through for optimal performance

### Storage Management

Configure session storage limits via the Settings page or `settings.json`:

| Setting | Default | Description |
|---------|---------|-------------|
| `session_cleanup_enabled` | `true` | Enable automatic cleanup of old sessions |
| `session_cleanup_days` | `30` | Days of inactivity before a session is eligible for cleanup |
| `session_max_count` | `1000` | Maximum number of sessions to keep (0 = unlimited) |

**Via Settings Page:**
1. Navigate to Settings → Session Management
2. Configure cleanup options and limits
3. View current storage statistics
4. Run manual cleanup if needed

**Via settings.json:**
```json
{
  "session_cleanup_enabled": true,
  "session_cleanup_days": 30,
  "session_max_count": 1000
}
```

### Session API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/sessions` | GET | List all sessions (with pagination, filtering) |
| `/api/sessions` | POST | Create a new session |
| `/api/sessions/{id}` | GET | Get a specific session |
| `/api/sessions/{id}` | PUT | Update session metadata |
| `/api/sessions/{id}` | DELETE | Delete a session |
| `/api/sessions/{id}/messages` | GET | Get messages for a session |
| `/api/sessions/{id}/messages` | POST | Add a message to a session |
| `/api/sessions/search` | GET | Search across all sessions |
| `/api/sessions/storage/stats` | GET | Get storage statistics |
| `/api/sessions/cleanup` | POST | Trigger manual cleanup |

### Performance

The session system is optimized for handling many sessions efficiently:

- **100+ Sessions**: Tested to handle 150+ sessions with sub-millisecond list operations
- **Concurrent Access**: Thread-safe operations support multiple tabs and clients
- **Efficient Search**: Full-text search returns results in under 500µs for typical workloads

## 🔌 Plugin Development

Ori Agent uses a plugin system that lets you extend functionality with custom tools. Build plugins as standalone executables that communicate via gRPC.

### Plugin Optimization APIs (New!)

We've introduced three powerful APIs that dramatically simplify plugin development:

#### 1. **YAML-Based Tool Definitions** (70% less code!)
Define parameters in `plugin.yaml` instead of code:

```yaml
tool_definition:
  description: "Your tool description"
  parameters:
    - name: operation
      type: string
      required: true
      enum: [create, list, delete]
    - name: count
      type: integer
      min: 1
      max: 100
```

```go
func (t *MyTool) Definition() pluginapi.Tool {
    tool, _ := t.GetToolDefinition()  // Auto-loads from plugin.yaml
    return tool
}
```

#### 2. **Settings API**
Simple, thread-safe key-value storage for plugin configuration:

```go
sm := t.Settings()
sm.Set("api_key", "sk-123")
apiKey, _ := sm.GetString("api_key")
```

#### 3. **Template Rendering API**
Serve beautiful web pages with Go templates:

```go
//go:embed templates
var templatesFS embed.FS

html, err := pluginapi.RenderTemplate(templatesFS, "templates/page.html", data)
```

### Getting Started with Plugins

- **Documentation**: See [PLUGIN_OPTIMIZATION_GUIDE.md](PLUGIN_OPTIMIZATION_GUIDE.md) for complete migration guide
- **Examples**: Check out `example_plugins/minimal/` and `example_plugins/webapp/`
- **Reference**: See [CLAUDE.md](CLAUDE.md) for detailed plugin development patterns

### Building a Plugin

```bash
cd example_plugins/minimal
go build -o minimal-plugin main.go
cp minimal-plugin ../../uploaded_plugins/
```

Restart Ori Agent, and your plugin will be automatically loaded!

### Available Plugins

<!-- AUTO:PLUGINS -->
| Plugin | Description | Type |
|--------|-------------|------|
| math | Perform basic math operations: add, subtract, multiply, divide | Example |
| minimal | Minimal example tool demonstrating YAML-based configuration. Supports echo and status operations. | Example |
| result-handler | Handle actions on chat results like opening directories, files, or URLs | Example |
| weather | Get weather for a given location | Example |
| webapp | Example tool with web interface. Manages a simple list of items. | Example |
<!-- AUTO:PLUGINS_END -->

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 💬 Support

- 🐛 **Issues**: [GitHub Issues](https://github.com/johnjallday/ori-agent/issues)
- 💡 **Feature Requests**: Open an issue with the "enhancement" label
- 💬 **Discussions**: [GitHub Discussions](https://github.com/johnjallday/ori-agent/discussions)

While this app is very functional, there will be a lot of breaking changes. Feel free to give feedbacks.
MVP = v0.1.0  => I will be releasing at least once a week until v0.0.99

## 🛣️Roadmap
- v0.1.0 => AI Agent Functionality
- v0.2.0 => Blockchain Integration



---

Made with ❤️ using Go and modern web technologies
