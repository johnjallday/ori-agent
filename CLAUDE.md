# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Ori Agent** is a modular, plugin-driven framework for building tool-calling AI agents. The system provides secure plugin loading (via HashiCorp go-plugin), multi-agent orchestration, workspace collaboration, and HTTP interfaces for building autonomous AI systems.

**Key Design Philosophy**: Plugins run as separate RPC processes (not shared libraries), providing strong isolation and cross-platform compatibility.

## Build & Development Commands

### Initial Setup
```bash
# Install dependencies
go mod tidy

# Set required environment variables
export OPENAI_API_KEY="your-key-here"
export ANTHROPIC_API_KEY="your-key-here"  # Optional, for Claude support
```

### Building

```bash
# Build everything (server + menubar + plugins)
./scripts/build.sh

# Build server only
./scripts/build-server.sh
# OR
go build -o bin/ori-agent ./cmd/server

# Build menu bar app (macOS only)
go build -o bin/ori-menubar ./cmd/menubar

# Build plugins as RPC executables (NOT shared libraries)
./scripts/build-plugins.sh

# Using Makefile
make build         # Build server binary
make menubar       # Build menu bar app (macOS)
make plugins       # Build all plugins
make all           # Build server + menubar + plugins
make build-all     # Cross-compile for multiple platforms
```

**IMPORTANT**: Plugins are built as standalone executables, NOT using `-buildmode=plugin`. They communicate via gRPC/RPC.

### Running

```bash
# Run server (development)
go run ./cmd/server

# Run built binary
./bin/ori-agent

# Run menu bar app (macOS)
./bin/ori-menubar

# Run with custom port
PORT=9000 go run ./cmd/server

# Using Makefile
make run           # Requires OPENAI_API_KEY
make run-dev       # Run with go run
make run-menubar   # Run menu bar app (macOS)
```

**Menu Bar App**: The macOS menu bar app provides a GUI to start/stop the server, with auto-start on login support and visual status indicators.

### Testing

```bash
# Run all tests
go test ./...

# Run specific package tests
go test ./internal/llm/
go test ./internal/registry/

# Run specific test
go test -v ./internal/llm/... -run TestOpenAIProvider

# Run with coverage
go test -cover ./...

# Check for issues
go vet ./...
go fmt ./...

# Using Makefile
make test
make lint          # Requires golangci-lint
```

## Architecture Overview

### Core Technology Stack

- **Language**: Go 1.25+
- **Plugin System**: HashiCorp go-plugin with gRPC/Protocol Buffers
- **LLM Providers**: OpenAI, Anthropic Claude, Ollama (via provider abstraction)
- **Protocol Buffers**: `pluginapi/proto/tool.proto` defines plugin interface
- **UI**: HTML/CSS/JavaScript (Bootstrap-based, embedded in `internal/web/`)

### Key Architectural Patterns

**1. Plugin System (RPC-Based, NOT Shared Libraries)**
- Plugins are **separate executable processes**, not `.so` files
- Communication via gRPC using Protocol Buffers (`pluginapi/proto/tool.proto`)
- Plugin interface: `pluginapi.Tool` in `pluginapi/pluginapi.go`
- Plugins run in isolated processes managed by HashiCorp go-plugin
- Build plugins with: `go build -o plugin-name main.go` (NOT `-buildmode=plugin`)

**2. Modular HTTP Handler Pattern**
Each domain has dedicated handler modules in `internal/*http/`:
- `agenthttp` - Agent CRUD operations
- `chathttp` - Chat interactions
- `pluginhttp` - Plugin management (upload, configure, registry, web pages)
- `settingshttp` - Configuration management
- `updatehttp` - Update management
- `devicehttp` - Device detection
- `onboardinghttp` - User onboarding flow
- `orchestrationhttp` - Multi-agent orchestration
- `usagehttp` - Usage tracking and cost monitoring
- `mcphttp` - MCP (Model Context Protocol) integration

**3. LLM Provider Abstraction** (`internal/llm/`)
- Factory pattern supports multiple providers: `factory.go`
- Provider interface: `provider.go`
- Implementations: `openai_provider.go`, `claude_provider.go`, `ollama_provider.go`
- Common interface allows easy switching between providers
- Tool calling support via unified interface
- Cost tracking: `cost_tracker.go`
- See `internal/llm/README.md` for detailed usage patterns

**4. Plugin Registry System**
- Local registry: `local_plugin_registry.json` (user-uploaded plugins)
- Remote registry: Cached in `plugin_registry_cache.json`
- Registry manager: `internal/registry/registry.go`
- Auto-validation on startup, searches common directories for moved plugins
- Search locations (in order): `plugins/`, `uploaded_plugins/`, `example_plugins/`, `../plugins/`, `../uploaded_plugins/`

**5. Agent Isolation & Workspaces**
- Each agent has isolated plugin contexts
- Agent configs stored in `agents/<agent-name>/config.json`
- Plugins maintain per-agent state via `AgentContext`
- **Workspace System** (`internal/workspace/`): Multi-agent collaboration
  - Shared data between agents
  - Task delegation and execution
  - Scheduled tasks with cron-like scheduling
  - Event bus for inter-agent communication
  - Workflow orchestration

**6. Communication & Orchestration**
- `internal/agentcomm/`: Inter-agent communication system
- `internal/orchestration/`: Multi-agent workflow orchestration
- `internal/orchestration/templates/`: Pre-built orchestration templates

**7. macOS Menu Bar App** (`cmd/menubar/`, `internal/menubar/`)
- Menu bar GUI for controlling the server (macOS only)
- Components:
  - `controller.go` - Server lifecycle management (start/stop/status)
  - `launchagent.go` - macOS auto-start integration (LaunchAgent plist)
  - `settings.go` - Preference persistence via app_state.json
  - `icons.go` - Embedded status icons (go:embed)
  - `main.go` - Systray integration and menu handling
- Features:
  - Visual server status indicators (colored icons)
  - Start/Stop server controls
  - Open browser quick action
  - Auto-start on login toggle
  - Graceful shutdown handling

### Data Flow

```
HTTP Request → Handler (internal/*http/) → Business Logic → Store/Registry → File System
```

### Plugin Execution Flow

```
Chat Message → LLM Provider → Tool Call Decision → Plugin Loader →
RPC Call to Plugin Process → Plugin Execution → Structured Result → UI Rendering
```

## Plugin Development

### Plugin Interface

All plugins implement `pluginapi.Tool` (defined in `pluginapi/pluginapi.go`):

```go
type Tool interface {
    Definition() openai.FunctionDefinitionParam
    Call(ctx context.Context, args string) (string, error)
}
```

### Optional Plugin Interfaces

Plugins can optionally implement additional interfaces for enhanced functionality:

- `VersionedTool` - Provides version information
- `PluginCompatibility` - Declares version compatibility requirements (min/max agent version, API version)
- `AgentAwareTool` - Receives agent context (name, config path, agent dir)
- `DefaultSettingsProvider` - Provides default configuration
- `InitializationProvider` - Describes required configuration variables
- `WebPageProvider` - Enables plugins to serve custom web pages through ori-agent
  - URL pattern: `http://localhost:8765/api/plugins/{plugin-name}/pages/{page-path}`
  - Useful for: script marketplaces, configuration UIs, data visualization
- `MetadataProvider` - Provides plugin metadata (maintainers, license, repository)
- `HealthCheckProvider` - Implements custom health checks

### Building a Plugin

```bash
# Plugins are built as executables (NOT shared libraries)
cd plugins/math
go build -o math math.go  # NOT -buildmode=plugin

# Plugin automatically uses gRPC for communication with server
```

### Plugin Optimization APIs (Recommended)

**New in 2024**: Ori Agent now provides optimization APIs that dramatically simplify plugin development:

**1. YAML-Based Tool Definitions**
Define tool parameters in `plugin.yaml` instead of code (70% less code):

```yaml
# plugin.yaml
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
// Simplified Definition() method
func (t *MyTool) Definition() pluginapi.Tool {
    tool, _ := t.GetToolDefinition()  // Reads from plugin.yaml
    return tool
}
```

**2. Settings API**
Simple key-value storage for plugin configuration:

```go
// Store settings
sm := t.Settings()
sm.Set("api_key", "sk-123")
sm.Set("max_retries", 5)

// Retrieve settings (type-safe)
apiKey, _ := sm.GetString("api_key")
retries, _ := sm.GetInt("max_retries")
```

**3. Template Rendering API**
Serve web pages with Go templates:

```go
//go:embed templates
var templatesFS embed.FS

html, err := pluginapi.RenderTemplate(templatesFS, "templates/page.html", data)
```

**Benefits**:
- 70-90% reduction in boilerplate code
- Thread-safe settings with atomic writes
- Automatic XSS protection in templates
- Clean separation of concerns

**Documentation**: See `PLUGIN_OPTIMIZATION_GUIDE.md` for complete migration guide.

**Examples**: See `example_plugins/minimal/` and `example_plugins/webapp/` for working examples.

### Plugin Locations

1. `plugins/` - Built-in plugins (math, weather, result-handler)
2. `uploaded_plugins/` - User-uploaded plugins (auto-scanned on startup)
3. `plugin_cache/` - External plugins from remote registry

### Structured Results

Plugins can return structured data that the UI renders specially:
- Tables (sortable, interactive)
- Modals (with actions, multi-select)
- Lists, Cards, JSON, Text

See `pluginapi/result.go` for helper functions: `NewTableResult()`, `NewModalResult()`, etc.

## Important File Locations

### Configuration Files
- `settings.json` - Global settings, API keys, LLM provider config
- `agents.json` - Agent configurations
- `local_plugin_registry.json` - User plugins registry
- `plugin_registry_cache.json` - Cached remote registry
- `app_state.json` - Onboarding and application state
- `agents/<agent-name>/config.json` - Per-agent plugin settings
- `agents/<agent-name>/agent_settings.json` - Per-agent settings

### Build Outputs
- `bin/ori-agent` - Server binary
- `plugins/*/[plugin-name]` - Built plugin executables (NOT .so files)

### Source Code Organization
- `cmd/server/` - Main server entry point
- `internal/server/server.go` - Server initialization and dependency injection
- `internal/*http/` - HTTP handlers (modular by domain)
- `internal/llm/` - LLM provider abstraction layer
- `internal/pluginloader/` - Plugin loading and caching
- `internal/workspace/` - Multi-agent workspace system
- `internal/orchestration/` - Multi-agent orchestration
- `internal/web/` - Web server, templates, static files
- `pluginapi/` - Plugin interface definitions
- `pluginapi/proto/tool.proto` - Protocol Buffers definition

## Key Development Patterns

### Plugin Path Resolution

On server startup:
1. Scans `uploaded_plugins/` for new plugins
2. Validates all plugin paths in `local_plugin_registry.json`
3. Updates paths if plugins moved to common locations
4. Removes entries for missing plugins

### API Key Configuration

API keys can be set via (priority order):
1. Environment variables (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`)
2. `settings.json` file
3. UI settings panel

### Adding a New LLM Provider

1. Implement `internal/llm/provider.go` interface
2. Create provider file (e.g., `my_provider.go`) in `internal/llm/`
3. Register in `internal/server/server.go`:
   ```go
   s.llmFactory.Register("provider-name", newProvider)
   ```
4. Add tests following pattern in `internal/llm/*_test.go`

See `internal/llm/README.md` for complete provider implementation guide.

### Adding a New API Endpoint

```go
// 1. Create handler in appropriate package (e.g., internal/agenthttp/)
func (h *Handler) NewEndpoint(w http.ResponseWriter, r *http.Request) {
    // Implementation
}

// 2. Register in internal/server/server.go
mux.HandleFunc("/api/new-endpoint", handler.NewEndpoint)
```

### Server Initialization

The `Server` struct in `internal/server/server.go` holds all dependencies:
- `clientFactory` - OpenAI client factory
- `llmFactory` - LLM provider factory (multi-provider support)
- `registryManager` - Plugin registry
- `workspaceStore` - Workspace storage
- `taskExecutor` / `stepExecutor` - Task execution
- `taskScheduler` - Scheduled task management
- `eventBus` - Inter-agent event system
- `costTracker` - LLM usage cost tracking
- Multiple HTTP handlers (modular by domain)

## Testing Strategy

- Unit tests for providers: `internal/llm/*_test.go`
- Integration tests: `internal/llm/integration_test.go`
- Test data isolated per package
- Use context for timeouts in tests
- Mock providers available for testing

## Common Issues & Solutions

### "cannot load module X listed in go.work file"
There is a `go.work` file in the parent directory. Edit it and remove non-existent module references.

### Plugin not loading
1. Ensure plugin built as **executable** (not `-buildmode=plugin`)
2. Verify plugin in correct directory (`plugins/`, `uploaded_plugins/`)
3. Check path in `local_plugin_registry.json`
4. Review server logs for RPC communication errors

### API key not recognized
1. Check environment variable: `echo $OPENAI_API_KEY`
2. Verify in `settings.json`
3. Clear browser cache and reload UI

### Port already in use
```bash
# Kill process on port 8765
./kill-8080.sh  # Script name unchanged for backward compatibility

# Or use custom port
PORT=9000 go run ./cmd/server
```

## Code Conventions

### File Naming
- Go files: `lowercase_snake_case.go`
- Tests: `*_test.go`
- Packages: Single word, lowercase

### Error Handling
- Always handle errors explicitly
- Return errors up the stack
- Use structured logging with context
- Don't panic unless critical

### Git Workflow

This project follows a feature branch workflow with squash merging.

**Complete workflow documentation**: See `/GIT_WORKFLOW.md` in the project root

**Quick reference for branches**:
- `feature/` - New functionality
- `fix/` - Bug fixes
- `refactor/` - Code restructuring
- `docs/` - Documentation updates
- `test/` - Test additions/improvements
- `chore/` - Maintenance tasks

**Commands**:
- Always use `git switch` instead of `git checkout`
- Create branch: `git switch -c feature/descriptive-name`
- Switch branch: `git switch main`

**Commit message format**: Present tense, descriptive
- ✅ "Add plugin validation on startup"
- ✅ "Implement Claude provider cost tracking"
- ✅ "Fix plugin path resolution for uploaded plugins"
- ❌ "Fixed stuff"
- ❌ "WIP"
- ❌ "Updates"

## Additional Resources

### Documentation
- `README.md` - Main project documentation
- `TESTING.md` - Complete testing guide
- `docs/` - Organized documentation directory
  - `docs/api/API_REFERENCE.md` - HTTP API endpoint reference
  - `docs/testing/TEST_CHEATSHEET.md` - Quick testing commands
  - `docs/testing/TESTING_SETUP_SUMMARY.md` - Testing infrastructure overview
- `internal/llm/README.md` - LLM provider abstraction guide

### Development
- See `docs/README.md` for complete documentation index
- Run `make help` for available development commands
