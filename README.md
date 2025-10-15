# 🐬 Dolphin Agent

A modern, extensible AI agent platform with a sleek web interface and powerful plugin system. Dolphin Agent allows you to create intelligent assistants that can be extended with custom tools and integrations.

![Version](https://img.shields.io/badge/Version-v0.0.6-blue)
![Go](https://img.shields.io/badge/Go-1.24-00add8)
![UI](https://img.shields.io/badge/UI-Modern%20Design-6366f1)
![Plugin System](https://img.shields.io/badge/Plugins-Extensible-10b981)
![License](https://img.shields.io/badge/License-MIT-green)

## ✨ Features

### 🔌 Extensible Plugin System
- **Hot-loadable Plugins**: Add functionality without restarting the server
- **Plugin Registry**: Local and external plugin support with auto-updates
- **Security**: SHA256 checksum verification for external plugins
- **Caching**: Intelligent plugin caching to prevent reload issues
- **Plugin Configuration**: Advanced initialization and settings management

### 🤖 Multi-Agent Support
- **Agent Management**: Create, switch, and delete agents through the UI
- **Isolated Contexts**: Each agent maintains its own plugin configuration
- **Status Tracking**: Visual indicators for active agents and loaded plugins
- **Agent-Specific Settings**: Individual configurations per agent

### ⚙️ Advanced Configuration
- **Multiple AI Models**: Support for GPT-4o, GPT-4o-mini, GPT-4-turbo
- **Temperature Control**: Fine-tune response creativity and focus
- **API Key Management**: Secure storage and management
- **Settings Persistence**: Configuration saved per agent

### 📊 Structured Content Rendering
- **Smart Detection**: Automatically identifies tables, lists, and formatted content
- **Markdown Support**: Full GitHub Flavored Markdown rendering
- **Interactive Tables**: Click-to-copy cells, row selection, hover effects
- **Code Highlighting**: Syntax highlighting for code blocks and inline code
- **Table Analytics**: Row/column counts and usage hints

## 🚀 Quick Start

### Prerequisites
- Go 1.24 or later
- Modern web browser

### Installation

1. **Clone the repository**
   ```bash
   git clone https://github.com/johnjallday/dolphin-agent.git
   cd dolphin-agent
   ```

2. **Install dependencies**
   ```bash
   go mod tidy
   ```

3. **Set up environment variables**
   ```bash
   export OPENAI_API_KEY="your-openai-api-key"
   ```

   Or Simply enter your api key on the bottom of the sidebar
   ```json
   {
     "openai_api_key": "your-openai-api-key"
   }
   ```

4. **Build the project**
   ```bash
   # Build everything (server + plugins)
   ./scripts/build.sh

   # Or build components separately:
   ./scripts/build-server.sh        # Server only
   ./scripts/build-plugins.sh       # Built-in plugins
   ./scripts/build-external-plugins.sh  # External plugins
   ```

5. **Run the server**
   ```bash
   ./bin/dolphin-agent
   # Or during development:
   go run cmd/server/main.go
   ```

6. **Open your browser**
   ```
   http://localhost:8080
   ```

## 🔧 Usage

### Creating Agents
1. Open the sidebar using the hamburger menu
2. Navigate to the "Agents" tab
3. Enter a name and click "Create"
4. Switch between agents using the "Switch" button

### Managing Plugins
1. Go to the "Plugins" tab in the sidebar
2. **Load from Registry**: Click "Load" next to available plugins
3. **Upload Custom**: Use the file input to upload `.so` plugin files
4. **Configure Plugins**: Click "Configure" to set up plugin-specific settings
5. **View Loaded**: See all plugins loaded for the current agent

### Configuring Settings
1. Access the "Settings" tab
2. Select your preferred AI model
3. Adjust temperature (creativity vs. focus)
4. Update API key if needed
5. Click "Save Settings"

#### Special Commands
- **`/agent`** - Display comprehensive agent status dashboard
  - Shows current agent name and configuration
  - Lists model settings (model type, temperature)
  - Displays API key status
  - Shows all loaded plugins with versions and emojis
  - Provides system status information

- **`/tools`** - List all available tools and functions
  - Shows all loaded plugins with descriptions
  - Displays function parameters (required vs optional)
  - Lists available options for enum parameters
  - Includes helpful emojis for easy identification
  - Works entirely offline without API calls

## 🔌 Plugin Development

For detailed information on creating custom plugins, see the [Plugin Development Guide](PLUGIN_DEVELOPMENT.md).

**Quick Overview:**
- Plugins are Go shared libraries (`.so` files) that implement the `pluginapi.Tool` interface
- Support for initialization, configuration, and agent-specific contexts
- Example plugins included: Math, Weather, and Result Handler

**Getting Started:**
1. Implement the `pluginapi.Tool` interface
2. Build with `go build -buildmode=plugin`
3. Upload via the UI or API
4. Configure through the web interface

See the [Plugin Development Guide](PLUGIN_DEVELOPMENT.md) for complete examples, advanced features, and best practices.

## 🌐 API Reference

For detailed API documentation including request/response formats, authentication, and examples, see the [API Reference Guide](API_REFERENCE.md).

**Available Endpoints:**
- **Agents**: Create, list, switch, and delete agents
- **Plugins**: Upload, configure, and manage plugins
- **Plugin Registry**: Browse and install plugins from registry
- **Settings**: Manage agent configuration and API keys
- **Chat**: Send messages and interact with agents
- **Updates**: Check for and install software updates

See the [API Reference Guide](API_REFERENCE.md) for complete endpoint documentation with examples.


### Project Structure
```
dolphin-agent/
├── cmd/server/           # Main server application
├── internal/
│   ├── agenthttp/       # Agent HTTP handlers
│   ├── chathttp/        # Chat HTTP handlers
│   ├── settingshttp/    # Settings HTTP handlers
│   ├── pluginhttp/      # Plugin HTTP handlers (modular)
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
│   └── web/             # Web server and static files
├── plugins/
│   ├── math/            # Example math plugin
│   ├── weather/         # Example weather plugin
│   └── result-handler/  # File/URL handler plugin
├── pluginapi/           # Plugin interface definition
├── scripts/             # Build and maintenance scripts
├── uploaded_plugins/    # User-uploaded plugin binaries
├── agents/              # Agent-specific configurations
├── plugin_cache/        # External plugin cache
└── go.mod               # Go module definition
```

### Key Architecture Improvements

#### Modular Handler System (Recent Update)
- **Separated Concerns**: Chat, settings, and plugin handlers in separate modules
- **Improved Maintainability**: Each handler module focuses on specific functionality
- **Better Testing**: Independent handler modules can be tested separately
- **Cleaner Code**: Reduced main.go from 1700+ lines to ~300 lines

#### Handler Modules
- **`settingshttp`**: Agent settings and API key management
- **`chathttp`**: Chat interactions and agent status
- **`pluginhttp`**: Plugin management split into:
  - `plugins.go`: Core plugin operations (load, list, upload)
  - `registry.go`: Plugin registry and download operations
  - `init.go`: Plugin initialization and configuration

## 🛡️ Security

- **Input Validation**: All API inputs are validated and sanitized
- **Plugin Isolation**: Plugins run in isolated contexts per agent
- **Checksum Verification**: External plugins verified with SHA256
- **Error Handling**: Comprehensive error handling prevents crashes
- **No Secrets Exposure**: API keys and sensitive data never logged
- **Secure Configuration**: Settings stored locally with proper permissions

## 🔧 Environment Configuration

### Environment Variables
- `OPENAI_API_KEY` - Your OpenAI API key
- `AGENT_STORE_PATH` - Custom path for agent storage (default: `agents.json`)
- `PLUGIN_CACHE_DIR` - Custom plugin cache directory (default: `plugin_cache`)

### Configuration Files
- `settings.json` - Global configuration including API key
- `agents.json` - Agent configurations and state
- `local_plugin_registry.json` - User-uploaded plugin registry
- `plugin_registry_cache.json` - Cached remote plugin registry

## 🎯 Roadmap

- [ ] WebSocket support for real-time chat
- [ ] Plugin marketplace integration
- [ ] Voice interface support
- [ ] Multi-user support with authentication
- [ ] Plugin sandboxing and security enhancements
- [ ] Conversation history and export
- [ ] Custom model integration (Ollama, local models)
- [ ] Plugin dependency management
- [ ] Advanced plugin configuration UI
- [ ] Real-time plugin status monitoring

## 🤝 Contributing



### Building and Testing

```bash
# Build all components
make build

# Clean and rebuild
make clean && make build

# Build specific components
./scripts/build-server.sh
./scripts/build-plugins.sh

# Clean up development artifacts
./scripts/cleanup.sh
```

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- [OpenAI](https://openai.com/) for the GPT APIs
- [Bootstrap](https://getbootstrap.com/) for the UI framework
- [Inter](https://rsms.me/inter/) font family
- Go community for excellent tooling and libraries

## 💬 Support

- 🐛 Issues: [GitHub Issues](https://github.com/johnjallday/dolphin-agent/issues)
- 📖 Documentation: [GitHub Wiki](https://github.com/johnjallday/dolphin-agent/wiki)
- 💡 Feature Requests: Open an issue with the "enhancement" label

---

Made with ❤️ using Go and modern web technologies
