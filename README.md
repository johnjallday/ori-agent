# 🦆 Ori Agent

A modular, plugin-driven framework for building tool-calling AI agents.
It provides secure plugin loading, agent orchestration, and HTTP/WebSocket interfaces — letting you create lightweight autonomous systems that can use tools or sub-agents efficiently.

![Version](https://img.shields.io/badge/Version-v0.0.7-blue)
![Go](https://img.shields.io/badge/Go-1.24-00add8)
![UI](https://img.shields.io/badge/UI-Modern%20Design-6366f1)
![Plugin System](https://img.shields.io/badge/Plugins-Extensible-10b981)
![License](https://img.shields.io/badge/License-MIT-green)

## ✨ Features

### 🔌 Extensible Plugin System
- **Hot-loadable Plugins**: Add functionality without restarting the server
- **Plugin Registry**: Local and remote plugin support with auto-updates
- **Security**: SHA256 checksum verification for external plugins
- **Caching**: Intelligent plugin caching to prevent reload issues
- **Plugin Configuration**: Advanced initialization and settings management
- **Direct Tool Calls**: Execute plugin operations without OpenAI API calls via `/api/plugins/tool-call`

### 🤖 Multi-Agent Support
- **Agent Management**: Create, switch, and delete agents through the UI
- **Isolated Contexts**: Each agent maintains its own plugin configuration
- **Status Tracking**: Visual indicators for active agents and loaded plugins
- **Agent-Specific Settings**: Individual configurations per agent
- **Per-Agent Plugin State**: Plugins maintain separate state for each agent

### 📊 Structured Content Rendering
- **Multiple Display Types**: Support for tables, modals, lists, cards, JSON, and text
- **Smart Detection**: Automatically identifies and renders structured data
- **Interactive Tables**: Sortable columns, click-to-copy cells, row selection
- **Modal Dialogs**: Rich modals with multi-select support and action buttons
- **Markdown Support**: Full GitHub Flavored Markdown rendering
- **Code Highlighting**: Syntax highlighting for code blocks and inline code
- **Metadata Support**: Custom actions and behaviors via metadata fields

### ⚙️ Advanced Configuration
- **Multiple AI Models**: Support for GPT-4o, GPT-4o-mini, GPT-4-turbo, o1-preview, o1-mini
- **Temperature Control**: Fine-tune response creativity and focus
- **API Key Management**: Secure storage in `settings.json` or environment variables
- **Settings Persistence**: Configuration saved per agent
- **System Prompts**: Customizable system prompts per agent

### 🔄 Auto-Update System
- **Version Checking**: Automatic checks for new releases from GitHub
- **Release Management**: Download and install updates through the UI
- **Version Display**: Current version shown in settings panel
- **Release Notes**: View changelog and release information

### 🎨 Modern UI/UX
- **Responsive Design**: Works on desktop, tablet, and mobile
- **Dark Theme**: Easy on the eyes with modern color scheme
- **Smooth Animations**: CSS transitions for better user experience
- **Real-time Updates**: Dynamic content updates without page reload
- **Accessibility**: Keyboard navigation and screen reader support

## 🚀 Quick Start

### Prerequisites
- Go 1.25 or later
- Modern web browser (Chrome, Firefox, Safari, Edge)
- OpenAI API key

### Installation

1. **Clone the repository**
   ```bash
   git clone https://github.com/johnjallday/ori-agent.git
   cd ori-agent
   ```

2. **Install dependencies**
   ```bash
   go mod tidy
   ```

3. **Set up environment variables**

   Option 1: Environment variable
   ```bash
   export OPENAI_API_KEY="your-openai-api-key"
   ```

   Option 2: settings.json file
   ```json
   {
     "openai_api_key": "your-openai-api-key"
   }
   ```

   Option 3: Enter via UI (sidebar settings)

4. **Build the project**
   ```bash
   # Build everything (server + plugins)
   ./scripts/build.sh

   # Or build components separately:
   ./scripts/build-server.sh        # Server only
   ./scripts/build-plugins.sh       # Built-in plugins
   ```

5. **Run the server**
   ```bash
   ./bin/ori-agent

   # Or during development:
   go run cmd/server/main.go
   ```

6. **Open your browser**
   ```
   http://localhost:8765
   ```

### 🍎 macOS Menu Bar App

For macOS users, Ori Agent includes a convenient menu bar application that provides easy access to server controls.

#### Features
- **Menu Bar Icon**: Visual server status indicator (gray/yellow/green/red)
- **Start/Stop Server**: Control the server with a click
- **Open Browser**: Quick access to the web UI
- **Auto-start on Login**: Optional LaunchAgent integration
- **Graceful Shutdown**: Clean server shutdown when quitting

#### Installation

**Option 1: Build from source**
```bash
make menubar              # Build menu bar app
./bin/ori-menubar         # Run it
```

**Option 2: Use the installer** (recommended)
```bash
./scripts/create-mac-installer.sh 0.0.8
# Opens: releases/OriAgent-0.0.8.dmg
# Drag "Ori Agent.app" to Applications folder
```

#### Usage
1. Launch "Ori Agent" from Applications
2. A menu bar icon appears in the top-right of your screen
3. Click the icon to:
   - Start/Stop the server
   - Open the web UI in your browser
   - Enable auto-start on login
   - View server status

#### Auto-start on Login
1. Click the menu bar icon
2. Select "Auto-start on Login"
3. The app will launch automatically when you log in

The setting is saved in `app_state.json` and creates a LaunchAgent at:
`~/Library/LaunchAgents/com.ori.menubar.plist`

#### Logs
Menu bar app logs are stored at:
- `~/Library/Logs/ori-menubar.log`
- `~/Library/Logs/ori-menubar.error.log`

## 🔧 Usage

### Creating Agents
1. Open the sidebar using the hamburger menu (☰)
2. Navigate to the "Agents" tab
3. Enter a name and click "Create Agent"
4. Switch between agents using the "Switch" button
5. Delete agents with the "Delete" button (except the current agent)

### Managing Plugins
1. Go to the "Plugins" tab in the sidebar
2. **Browse Registry**: View available plugins from the remote registry
3. **Load Plugins**: Click "Load" next to available plugins
4. **Upload Custom**: Use the file input to upload `.so` plugin files
5. **Configure Plugins**: Click "Configure" to set up plugin-specific settings
6. **View Loaded**: See all plugins loaded for the current agent
7. **Check Updates**: See if newer versions of plugins are available

### Direct Plugin Tool Calls
Execute plugin operations directly without using the chat interface:

```bash
curl -X POST http://localhost:8765/api/plugins/tool-call \
  -H "Content-Type: application/json" \
  -d '{
    "plugin_name": "reaper-script-launcher",
    "operation": "list",
    "args": {}
  }'
```

This bypasses OpenAI API calls for faster, free plugin execution.

### Configuring Settings
1. Access the "Settings" tab
2. Select your preferred AI model
3. Adjust temperature (0.0 = focused, 2.0 = creative)
4. Update API key if needed
5. Modify system prompt (optional)
6. Click "Save Settings"

### Special Commands
- **`/agent`** - Display comprehensive agent status dashboard
  - Shows current agent name and configuration
  - Lists model settings (model type, temperature)
  - Displays API key status
  - Shows all loaded plugins with versions
  - Provides system status information

- **`/tools`** - List all available tools and functions
  - Shows all loaded plugins with descriptions
  - Displays function parameters (required vs optional)
  - Lists available options for enum parameters
  - Works entirely offline without API calls

## 🔌 Plugin Development

For detailed information on creating custom plugins, see the Plugin Development section in [CLAUDE.md](CLAUDE.md#plugin-development).

**Quick Overview:**
- Plugins are Go shared libraries (`.so` files) that implement the `pluginapi.Tool` interface
- Support for initialization, configuration, and agent-specific contexts
- Can return structured results (tables, modals, lists) for enhanced UI display
- Example plugins included: Math, Weather, Result Handler, and REAPER Script Launcher

**Structured Result System:**

Plugins can return structured data that the UI automatically renders:

```go
// Table result
result := pluginapi.NewTableResult(
    "Project List",
    []string{"Name", "Path", "Date"},
    rows,
)

// Modal result with multi-select
result := pluginapi.NewModalResult(
    "Available Scripts",
    "Select scripts to download",
    items,
)
result.Metadata["action"] = "download_script"
result.Metadata["buttonLabel"] = "Download"

return result.ToJSON()
```

**Display Types:**
- `table` - Sortable, interactive tables
- `modal` - Pop-up dialogs with actions
- `list` - Bulleted/numbered lists
- `card` - Card-based layouts
- `json` - Formatted JSON display
- `text` - Plain text (default)

See the Plugin Development section in [CLAUDE.md](CLAUDE.md#plugin-development) for complete examples.

## 🌐 API Reference

For detailed API documentation including request/response formats, authentication, and examples, see the [API Reference Guide](docs/api/API_REFERENCE.md).

**Available Endpoints:**
- **Agents**: `/api/agents` - Create, list, switch, and delete agents
- **Plugins**: `/api/plugins` - Upload, configure, and manage plugins
- **Plugin Registry**: `/api/plugin-registry` - Browse and install plugins
- **Plugin Tool Calls**: `/api/plugins/tool-call` - Direct plugin execution
- **Settings**: `/api/settings` - Manage agent configuration and API keys
- **Chat**: `/api/chat` - Send messages and interact with agents
- **Updates**: `/api/updates/*` - Check for and install updates

## 📁 Project Structure

```
ori-agent/
├── cmd/server/           # Main server application
├── internal/
│   ├── server/          # HTTP server and routing
│   ├── agenthttp/       # Agent HTTP handlers
│   ├── chathttp/        # Chat HTTP handlers
│   ├── settingshttp/    # Settings HTTP handlers
│   ├── pluginhttp/      # Plugin HTTP handlers
│   │   ├── plugins.go   # Core plugin operations
│   │   ├── registry.go  # Plugin registry management
│   │   ├── init.go      # Plugin initialization
│   │   └── tool_call.go # Direct tool call endpoint
│   ├── pluginloader/    # Plugin loading and caching
│   ├── plugindownloader/# External plugin downloading
│   ├── registry/        # Plugin registry management
│   ├── store/           # Data persistence layer
│   ├── types/           # Shared data structures
│   ├── updatemanager/   # Software update management
│   ├── updatehttp/      # Update HTTP handlers
│   ├── config/          # Configuration management
│   ├── client/          # OpenAI client factory
│   ├── version/         # Version information
│   ├── filehttp/        # File parsing handlers
│   └── web/             # Web server and static files
│       ├── static/      # CSS, JS, and assets
│       └── templates/   # HTML templates
├── plugins/
│   ├── math/            # Example math plugin
│   ├── weather/         # Example weather plugin
│   └── result-handler/  # File/URL handler plugin
├── pluginapi/           # Plugin interface definitions
│   ├── plugin.go        # Core plugin interface
│   └── result.go        # Structured result types
├── scripts/             # Build and maintenance scripts
├── uploaded_plugins/    # User-uploaded plugin binaries
├── agents/              # Agent-specific configurations
├── plugin_cache/        # External plugin cache
├── settings.json        # Global configuration
├── agents.json          # Agent configurations
└── go.mod               # Go module definition
```

## 🏗️ Architecture Highlights

+-------------------------------+
|        Tool-Calling Agent     |  ← top-level controller
|-------------------------------|
|  • Parses user requests       |
|  • Chooses the best tool/agent|
|  • Enforces budgets/timeouts  |
+-------------------------------+
            ↓
   +-------------------------+
   |  Plugin / Tool Layer    |
   |-------------------------|
   |  • Atomic tools (calc,  |
   |    search, db, etc.)    |
   |  • Complex sub-agents   |
   |    (research, code, etc.)|
   +-------------------------+
            ↓
   +-------------------------+
   |  Plugin Registry        |
   |-------------------------|
   |  • Signature + checksum |
   |  • Version + caching    |
   |  • Remote registry sync |
   +-------------------------+
            ↓
   +-------------------------+
   |  Runtime / Workspace    |
   |-------------------------|
   |  • Agent isolation      |
   |  • Context + memory     |
   |  • Result handler       |
   +-------------------------+


### Modular Handler System
- **Separated Concerns**: Each domain has its own handler module
- **Improved Maintainability**: Focused, single-responsibility modules
- **Better Testing**: Independent modules can be tested separately
- **Cleaner Code**: Main server reduced from 1700+ to ~300 lines

### Plugin System
- **HashiCorp go-plugin**: Robust plugin architecture with RPC communication
- **Version Support**: Plugins can report their version information
- **Settings Support**: Plugins can define and manage their own settings
- **Agent Context**: Plugins receive agent-specific context and storage paths
- **Schema Extraction**: Automatic extraction of plugin settings schemas

### Structured Results
- **Type-Safe**: Defined result types with validation
- **Extensible**: Easy to add new display types
- **Metadata Support**: Custom actions and behaviors
- **Backward Compatible**: Plain text results still work

### State Management
- **File-Based Storage**: JSON files for persistence
- **In-Memory Cache**: Fast access to frequently used data
- **Per-Agent Isolation**: Each agent has isolated state
- **Atomic Writes**: Safe concurrent access

### 🧠 Core Concepts


| Concept                | Description                                                                                                                   |
| ---------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| **Tool-Calling Agent** | The main orchestrator. Routes tasks to tools or sub-agents based on intent, keywords, or routing models.                      |
| **Plugins**            | Self-contained tools or agents that expose a consistent interface. Each declares its metadata, side-effects, and permissions. |
| **Plugin Registry**    | Secure manifest system that stores, validates, and caches plugins (with SHA256 verification).                                 |
| **Plugin Loader**      | Dynamically loads plugins at runtime, checks signatures, and isolates their execution.                                        |
| **Workspace**          | Per-agent sandbox that handles session state, memory, and file I/O.                                                           |
| **Result Handler**     | Renders or exports agent outputs (text, structured data, downloadable results).                                               |


## 🛡️ Security

- **Input Validation**: All API inputs are validated and sanitized
- **Plugin Isolation**: Plugins run in isolated contexts per agent
- **Checksum Verification**: External plugins verified with SHA256
- **Error Handling**: Comprehensive error handling prevents crashes
- **No Secrets Exposure**: API keys and sensitive data never logged
- **Secure Configuration**: Settings stored locally with proper permissions
- **CORS Support**: Configurable CORS for API access

## 🔧 Environment Configuration

### Environment Variables
- `OPENAI_API_KEY` - Your OpenAI API key (can also be set in settings.json)
- `AGENT_STORE_PATH` - Custom path for agent storage (default: `agents.json`)
- `PLUGIN_CACHE_DIR` - Custom plugin cache directory (default: `plugin_cache`)

### Configuration Files
- `settings.json` - Global configuration including API key
- `agents.json` - Agent configurations and state
- `local_plugin_registry.json` - User-uploaded plugin registry
- `plugin_registry_cache.json` - Cached remote plugin registry
- `agents/<agent-name>/config.json` - Agent-specific plugin configurations

## 🎯 Roadmap

- [ ] Ollama Support
- [ ] MCP Support
- [ ] WebSocket support for real-time chat
- [ ] Voice interface support
- [ ] Multi-user support with authentication
- [ ] Plugin sandboxing enhancements
- [ ] Conversation history and export
- [ ] Plugin dependency management
- [ ] Real-time plugin status monitoring
- [ ] Browser extension integration
- [ ] Mobile app

### Building and Testing

```bash
# Build all components
./scripts/build.sh

# Build server only
./scripts/build-server.sh

# Build plugins
./scripts/build-plugins.sh

# Clean build artifacts
./scripts/cleanup.sh

# Run tests
go test ./...
```

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- [OpenAI](https://openai.com/) for the GPT APIs
- [HashiCorp go-plugin](https://github.com/hashicorp/go-plugin) for the plugin system
- [Bootstrap](https://getbootstrap.com/) for the UI framework
- [Inter](https://rsms.me/inter/) font family
- Go community for excellent tooling and libraries

## 💬 Support

- 🐛 **Issues**: [GitHub Issues](https://github.com/johnjallday/ori-agent/issues)
- 📖 **Documentation**: [GitHub Wiki](https://github.com/johnjallday/ori-agent/wiki)
- 💡 **Feature Requests**: Open an issue with the "enhancement" label
- 💬 **Discussions**: [GitHub Discussions](https://github.com/johnjallday/ori-agent/discussions)

- buymeacoffee.com/johnjallday

## 🌟 Example Plugins

### Built-in Plugins
- **Math**: Basic arithmetic operations
- **Weather**: Weather information lookup
- **Result Handler**: File and URL processing

### External Plugins
- **REAPER Script Launcher**: Control REAPER DAW with scripts
  - List and run ReaScripts
  - Download scripts from repository
  - Register scripts to REAPER
  - Get current project context
  - Clean orphaned script entries

See individual plugin directories for detailed documentation.

---

Made with ❤️ using Go and modern web technologies
