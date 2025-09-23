# 🐬 Dolphin Agent

A modern, extensible AI agent platform with a sleek web interface and powerful plugin system. Dolphin Agent allows you to create intelligent assistants that can be extended with custom tools and integrations.

![Version](https://img.shields.io/badge/Version-v0.0.2-blue)
![Go](https://img.shields.io/badge/Go-1.24-00add8)
![UI](https://img.shields.io/badge/UI-Modern%20Design-6366f1)
![Plugin System](https://img.shields.io/badge/Plugins-Extensible-10b981)
![License](https://img.shields.io/badge/License-MIT-green)

## ✨ Features

### 🎨 Modern Web Interface
- **Glassmorphism Design**: Beautiful, contemporary UI with backdrop blur effects
- **Dark/Light Mode**: Seamless theme switching with excellent contrast
- **Responsive Layout**: Works perfectly on desktop and mobile devices
- **Real-time Updates**: Live status indicators and dynamic content
- **Interactive Chat**: Modern message bubbles with timestamps and avatars
- **Structured Content**: Intelligent markdown rendering with interactive tables

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

   Or create a `settings.json` file:
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

### Chatting with Agents
- Type messages in the chat input
- Use **Enter** to send (or **Shift+Enter** for new lines)
- View responses with timestamps and tool usage
- Click the agent badge in the navbar for quick status info

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

### Creating a Plugin

Plugins are Go packages compiled as shared libraries (`.so` files) that implement the `pluginapi.Tool` interface.

#### 1. Basic Structure

```go
package main

import (
    "context"
    "encoding/json"
    "github.com/johnjallday/dolphin-agent/pluginapi"
    "github.com/openai/openai-go/v2"
)

type MyTool struct{}

// Ensure interface compliance
var _ pluginapi.Tool = MyTool{}

func (t MyTool) Definition() openai.FunctionDefinitionParam {
    return openai.FunctionDefinitionParam{
        Name:        "my_function",
        Description: openai.String("Description of what this tool does"),
        Parameters: openai.FunctionParameters{
            "type": "object",
            "properties": map[string]any{
                "param1": map[string]any{
                    "type":        "string",
                    "description": "Parameter description",
                },
            },
            "required": []string{"param1"},
        },
    }
}

func (t MyTool) Call(ctx context.Context, args string) (string, error) {
    var params struct {
        Param1 string `json:"param1"`
    }
    if err := json.Unmarshal([]byte(args), &params); err != nil {
        return "", err
    }

    // Your tool logic here
    result := "Processed: " + params.Param1
    return result, nil
}

// Export the tool
var Tool MyTool
```

#### 2. Advanced Plugin Features

##### Plugin Initialization
```go
type MyTool struct {
    initialized bool
    config      map[string]string
}

// Implement InitializationProvider for configuration
func (t *MyTool) GetRequiredConfig() []pluginapi.ConfigVariable {
    return []pluginapi.ConfigVariable{
        {
            Name:        "api_key",
            Type:        "string",
            Description: "Your API key",
            Required:    true,
        },
    }
}

func (t *MyTool) ValidateConfig(config map[string]interface{}) error {
    if _, ok := config["api_key"]; !ok {
        return errors.New("api_key is required")
    }
    return nil
}

func (t *MyTool) InitializeWithConfig(config map[string]interface{}) error {
    t.config = make(map[string]string)
    for k, v := range config {
        t.config[k] = fmt.Sprintf("%v", v)
    }
    t.initialized = true
    return nil
}
```

##### Agent Context Awareness
```go
// Implement AgentAwareTool for agent-specific behavior
func (t *MyTool) SetAgentContext(ctx pluginapi.AgentContext) {
    log.Printf("Plugin loaded for agent: %s", ctx.Name)
    // Access agent-specific configuration path: ctx.ConfigPath
}
```

#### 3. Build the Plugin

```bash
go build -buildmode=plugin -o my_plugin.so my_plugin.go
```

#### 4. Upload via UI

Use the file upload in the Plugins tab or the API:

```bash
curl -X POST -F "plugin=@my_plugin.so" http://localhost:8080/api/plugins
```

### Example Plugins

The project includes several example plugins:

- **Math Plugin** (`plugins/math/`): Basic arithmetic operations
- **Weather Plugin** (`plugins/weather/`): Weather information (mock implementation)
- **Result Handler Plugin** (`plugins/result-handler/`): File and URL handling

## 🌐 API Reference

### Agents
- `GET /api/agents` - List all agents and current agent
- `POST /api/agents` - Create new agent (JSON: `{"name": "agent_name"}`)
- `PUT /api/agents?name=<name>` - Switch to agent
- `DELETE /api/agents?name=<name>` - Delete agent

### Plugins
- `GET /api/plugins` - List loaded plugins for current agent
- `POST /api/plugins` - Upload and load plugin (multipart/form-data)
- `DELETE /api/plugins?name=<name>` - Unload plugin
- `POST /api/plugins/save-settings` - Save plugin settings
- `GET /api/plugins/{name}/config` - Get plugin configuration
- `POST /api/plugins/{name}/initialize` - Initialize plugin with config
- `POST /api/plugins/execute` - Execute plugin function directly
- `GET /api/plugins/init-status` - Check plugin initialization status

### Plugin Registry
- `GET /api/plugin-registry` - List available plugins
- `POST /api/plugin-registry` - Load plugin from registry (JSON: `{"name": "plugin_name"}`)
- `DELETE /api/plugin-registry?name=<name>` - Delete plugin from local registry
- `GET /api/plugin-updates` - Check for plugin updates
- `POST /api/plugin-updates` - Update plugins
- `POST /api/plugins/download` - Download plugin from registry

### Settings
- `GET /api/settings` - Get current agent settings
- `POST /api/settings` - Update agent settings
- `GET /api/api-key` - Get masked API key info
- `POST /api/api-key` - Update API key

### Chat
- `POST /api/chat` - Send message to current agent (JSON: `{"question": "message"}`)

### Updates
- `GET /api/updates/check` - Check for software updates
- `GET /api/updates/releases` - List available releases
- `POST /api/updates/download` - Download update
- `GET /api/updates/version` - Get current version

## 🏗️ Technology Stack

### Backend Technologies

#### Core Language & Runtime
- **Go 1.24+** - Primary programming language
  - High-performance concurrent server
  - Native plugin system support
  - Excellent standard library
  - Cross-platform compilation

#### Web Framework & HTTP
- **Native `net/http`** - Go standard library HTTP server
  - Modular handler architecture
  - RESTful API design
  - JSON-based communication
  - CORS support for web interface

#### AI & Machine Learning
- **OpenAI Go SDK v2** - Official OpenAI client
  - GPT-4o, GPT-4o-mini, GPT-4-turbo support
  - Function calling for tool integration
  - Temperature and parameter control

#### Plugin System
- **Go Plugin Package** - Native dynamic loading
  - Shared library (`.so`) compilation
  - Hot-reloadable plugins
  - Interface-based architecture
  - Plugin caching and version management

#### Data Storage
- **JSON Files** - Configuration and state
  - `agents.json` - Agent configurations
  - `plugin_registry.json` - Plugin metadata
  - `settings.json` - Global configuration
  - File-based persistence for simplicity

### Frontend Technologies

#### Core Web Technologies
- **HTML5** - Modern semantic markup
- **CSS3** - Advanced styling with custom properties
- **Vanilla JavaScript ES6+** - No framework dependencies
  - Async/await for API calls
  - Modern DOM manipulation
  - Event-driven architecture

#### UI Framework & Design
- **Bootstrap 5.3** - Responsive framework
- **Custom CSS** - Modern design system
  - CSS Custom Properties (variables)
  - Glassmorphism effects
  - Dark/light theme support
  - Smooth animations and transitions

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

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

### Development Guidelines

- Follow Go best practices and conventions
- Add tests for new functionality
- Update documentation for API changes
- Ensure plugins follow the interface specification
- Use the modern UI design patterns
- Maintain the modular handler architecture

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