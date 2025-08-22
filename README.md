# 🐬 Dolphin Agent

A modern, extensible AI agent platform with a sleek web interface and powerful plugin system. Dolphin Agent allows you to create intelligent assistants that can be extended with custom tools and integrations.

![Modern UI](https://img.shields.io/badge/UI-Modern%20Design-6366f1)
![Plugin System](https://img.shields.io/badge/Plugins-Extensible-10b981)
![Go](https://img.shields.io/badge/Go-1.24-00add8)
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

### 🤖 Multi-Agent Support
- **Agent Management**: Create, switch, and delete agents through the UI
- **Isolated Contexts**: Each agent maintains its own plugin configuration
- **Status Tracking**: Visual indicators for active agents and loaded plugins

### ⚙️ Advanced Configuration
- **Multiple AI Models**: Support for GPT-4o, GPT-4.1, GPT-5 series
- **Temperature Control**: Fine-tune response creativity and focus
- **Model Restrictions**: Automatic validation (e.g., GPT-5 requires temperature=1)

### 📊 Structured Content Rendering
- **Smart Detection**: Automatically identifies tables, lists, and formatted content
- **Markdown Support**: Full GitHub Flavored Markdown rendering
- **Interactive Tables**: Click-to-copy cells, row selection, hover effects
- **Code Highlighting**: Syntax highlighting for code blocks and inline code
- **Table Analytics**: Row/column counts and usage hints
- **Copy Everything**: One-click table copying with proper formatting

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

4. **Build the project**
   ```bash
   # Build server and all plugins
   ./scripts/build.sh
   
   # Or build components separately:
   ./scripts/build-server.sh        # Server only
   ./scripts/build-plugins.sh       # Plugins only
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
4. **View Loaded**: See all plugins loaded for the current agent

### Configuring Settings
1. Access the "Settings" tab
2. Select your preferred AI model
3. Adjust temperature (creativity vs. focus)
4. Click "Save Settings"

### Chatting with Agents
- Type messages in the chat input
- Use **Enter** to send (or **Shift+Enter** for new lines)
- View responses with timestamps and tool usage
- Click the agent badge in the navbar for quick info

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

type myTool struct{}

// Ensure interface compliance
var _ pluginapi.Tool = myTool{}

func (t myTool) Definition() openai.FunctionDefinitionParam {
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

func (t myTool) Call(ctx context.Context, args string) (string, error) {
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
var Tool myTool
```

#### 2. Build the Plugin

```bash
go build -buildmode=plugin -o my_plugin.so my_plugin.go
```

#### 3. Upload via UI

Use the file upload in the Plugins tab or the API:

```bash
curl -X POST -F "plugin=@my_plugin.so" http://localhost:8080/api/plugins
```

### Example Plugins

The project includes several example plugins:

- **Math Plugin** (`plugins/math/`): Basic arithmetic operations
- **Weather Plugin** (`plugins/weather/`): Weather information (mock implementation)
- **REAPER Plugin** (`../dolphin-reaper/`): Launch REAPER ReaScripts

## 🌐 API Reference

### Agents
- `GET /api/agents` - List all agents and current agent
- `POST /api/agents` - Create new agent
- `PUT /api/agents?name=<name>` - Switch to agent
- `DELETE /api/agents?name=<name>` - Delete agent

### Plugins
- `GET /api/plugins` - List loaded plugins for current agent
- `POST /api/plugins` - Upload and load plugin (multipart/form-data)
- `DELETE /api/plugins?name=<name>` - Unload plugin

### Plugin Registry
- `GET /api/plugin-registry` - List available plugins
- `POST /api/plugin-registry` - Load plugin from registry

### Settings
- `GET /api/settings` - Get current settings
- `POST /api/settings` - Update settings

### Chat
- `POST /api/chat` - Send message to current agent

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
  - Custom multiplexer for routing
  - RESTful API design
  - JSON-based communication
  - CORS support for web interface

#### AI & Machine Learning
- **OpenAI Go SDK v2** - Official OpenAI client
  - GPT-4o, GPT-4.1, GPT-5 series support
  - Function calling for tool integration
  - Streaming responses (future)
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
  - File-based persistence for simplicity
  - Easy backup and version control

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
  - Grid system for layouts
  - Component library
  - Utility classes
- **Custom CSS** - Modern design system
  - CSS Custom Properties (variables)
  - Glassmorphism effects
  - Dark/light theme support
  - Smooth animations and transitions

#### Fonts & Icons
- **Inter Font Family** - Modern typography
- **Custom SVG Icons** - Lightweight vector graphics
- **Responsive Typography** - Fluid scale system

### Development & Build Tools

#### Build System
- **Custom Shell Scripts** - Automated building
  - `scripts/build.sh` - Complete project build
  - `scripts/build-plugins.sh` - Plugin compilation
  - `scripts/build-release.sh` - Cross-platform releases
  - Version embedding and Git integration

#### Plugin Development
- **Go Build Mode Plugin** - Native plugin compilation
  - Interface-based contracts (`pluginapi.Tool`)
  - Version compatibility checking
  - Semantic versioning support

#### Development Tools
- **Go Modules** - Dependency management
- **Git** - Version control with comprehensive `.gitignore`
- **Cross-compilation** - Multiple platform support
  - Linux (amd64, arm64)
  - macOS (amd64, arm64)
  - Windows (amd64)

### External Integrations

#### AI Services
- **OpenAI API** - Primary AI provider
  - Chat completions
  - Function calling
  - Model parameter control
- **Future**: Anthropic, Google, Ollama support

#### External Plugin Ecosystem
- **Plugin Registry** - Local and remote plugins
  - SHA256 checksum verification
  - Auto-update capabilities
  - Version management
- **Example Integrations**:
  - REAPER DAW automation
  - Weather services
  - Mathematical operations
  - File system operations

### Architecture Patterns

#### Design Patterns
- **Plugin Architecture** - Extensible system
- **Repository Pattern** - Data access abstraction
- **Handler Pattern** - HTTP request processing
- **Factory Pattern** - Plugin instantiation
- **Observer Pattern** - Real-time updates (future)

#### Security & Performance
- **Input Validation** - All API endpoints
- **Error Handling** - Comprehensive error management
- **Caching** - Plugin and resource caching
- **Concurrent Processing** - Go routines for performance
- **Memory Safety** - Go's built-in safety features

### Project Structure
```
dolphin-agent/
├── cmd/server/           # Main server application
├── internal/
│   ├── agenthttp/       # Agent HTTP handlers
│   ├── pluginhttp/      # Plugin HTTP handlers
│   ├── pluginloader/    # Plugin loading and caching
│   ├── plugindownloader/# External plugin downloading
│   ├── store/           # Data persistence layer
│   ├── types/           # Shared data structures
│   ├── updatemanager/   # Plugin update management
│   ├── version/         # Version information
│   └── web/             # Web server and static files
├── plugins/
│   ├── math/            # Example math plugin
│   ├── weather/         # Example weather plugin
│   └── result-handler/  # File/URL handler plugin
├── pluginapi/           # Plugin interface definition
├── scripts/             # Build and maintenance scripts
├── uploaded_plugins/    # Compiled plugin binaries
└── go.mod               # Go module definition
```

### Key Components

- **Agent System**: Multi-agent support with isolated plugin contexts
- **Plugin Loader**: Dynamic plugin loading with caching and error handling
- **Plugin Downloader**: Secure downloading and caching of external plugins
- **Web Interface**: Modern SPA with real-time updates
- **HTTP Handlers**: RESTful API for all operations
- **Update Manager**: Plugin versioning and update system
- **Build System**: Automated compilation and release management

## 🛡️ Security

- **Input Validation**: All API inputs are validated and sanitized
- **Plugin Isolation**: Plugins run in isolated contexts
- **Checksum Verification**: External plugins verified with SHA256
- **Error Handling**: Comprehensive error handling prevents crashes
- **No Secrets Exposure**: API keys and sensitive data never logged

## 🎯 Roadmap

- [ ] WebSocket support for real-time chat
- [ ] Plugin marketplace integration
- [ ] Voice interface support
- [ ] Multi-user support with authentication
- [ ] Plugin sandboxing and security enhancements
- [ ] Conversation history and export
- [ ] Custom model integration (Ollama, local models)

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

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- [OpenAI](https://openai.com/) for the GPT APIs
- [Bootstrap](https://getbootstrap.com/) for the UI framework
- [Inter](https://rsms.me/inter/) font family
- Go community for excellent tooling and libraries

## 💬 Support

- 📧 Email: support@dolphin-agent.com
- 🐛 Issues: [GitHub Issues](https://github.com/johnjallday/dolphin-agent/issues)
- 📖 Documentation: [Wiki](https://github.com/johnjallday/dolphin-agent/wiki)
coff.ee/johnjallday
---

Made with ❤️ by the Dolphin Agent team
