# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Ori Agent** is a modular framework for building tool-calling AI agents. The system provides multi-agent orchestration, workspace collaboration, MCP (Model Context Protocol) integration, skills management, and HTTP interfaces for building autonomous AI systems.

**Key Design Philosophy**: Tool capabilities are provided via MCP servers and skills, not plugins. The legacy plugin system has been removed.

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
# Build server only
./scripts/build-server.sh
# OR
go build -o bin/ori-agent ./cmd/server

# Build menu bar app (macOS only)
go build -o bin/ori-menubar ./cmd/menubar

# Using Makefile
make build         # Build server binary
make menubar       # Build menu bar app (macOS)
make all           # Build server + menubar
make build-all     # Cross-compile for multiple platforms
```

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
go test ./internal/workspace/

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
- **Tool System**: MCP (Model Context Protocol) servers + Skills
- **LLM Providers**: OpenAI, Anthropic Claude, Ollama (via provider abstraction)
- **Tool Interface**: `internal/toolapi/` defines the internal tool interface
- **UI**: HTML/CSS/JavaScript (Bootstrap-based, embedded in `internal/web/`)

### Key Architectural Patterns

**1. MCP + Skills Tool System**
- Tool capabilities are provided via **MCP servers** (external processes speaking Model Context Protocol) and **Skills** (reusable prompt-based capabilities)
- MCP servers are configured per workspace and managed via `internal/mcp/`
- Skills are managed via `internal/skills/` and `internal/skillshttp/`
- Internal tool interface: `toolapi.Tool` in `internal/toolapi/tool.go`
- The legacy gRPC plugin system has been fully removed

**2. Modular HTTP Handler Pattern**
Each domain has dedicated handler modules in `internal/*http/`:
- `agenthttp` - Agent CRUD operations
- `chathttp` - Chat interactions and workspace tools
- `settingshttp` - Configuration management
- `updatehttp` - Update management
- `devicehttp` - Device detection
- `onboardinghttp` - User onboarding flow
- `orchestrationhttp` - Multi-agent orchestration
- `usagehttp` - Usage tracking and cost monitoring
- `mcphttp` - MCP (Model Context Protocol) integration
- `skillshttp` - Skills management and marketplace
- `sessionhttp` - Session and workspace data management

**3. LLM Provider Abstraction** (`internal/llm/`)
- Factory pattern supports multiple providers: `factory.go`
- Provider interface: `provider.go`
- Implementations: `openai_provider.go`, `claude_provider.go`, `ollama_provider.go`
- Common interface allows easy switching between providers
- Tool calling support via unified interface
- Cost tracking: `cost_tracker.go`
- See `internal/llm/README.md` for detailed usage patterns

**4. Agent Isolation & Workspaces**
- Agent configs stored in `agents/<agent-name>/config.json`
- **Workspace System** (`internal/workspace/`): Multi-agent collaboration
  - Workspace-scoped MCP bindings and skill bindings
  - Workspace-scoped tools for notes, tasks, sessions, files
  - Folder-based storage with file sync
  - Task delegation and execution
  - Scheduled tasks with cron-like scheduling
  - Event bus for inter-agent communication

**5. Communication & Orchestration**
- `internal/agentcomm/`: Inter-agent communication system
- `internal/orchestration/`: Multi-agent workflow orchestration
- `internal/orchestration/templates/`: Pre-built orchestration templates

**6. macOS Menu Bar App** (`cmd/menubar/`, `internal/menubar/`)
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

### Tool Execution Flow

```
Chat Message → LLM Provider → Tool Call Decision → MCP Server / Workspace Tool →
Tool Execution → Result → UI Rendering
```

## Important File Locations

### Configuration Files
- `settings.json` - Global settings, API keys, LLM provider config
- `agents.json` - Agent configurations
- `app_state.json` - Onboarding and application state
- `agents/<agent-name>/config.json` - Per-agent settings
- `agents/<agent-name>/agent_settings.json` - Per-agent settings

### Build Outputs
- `bin/ori-agent` - Server binary
- `bin/ori-menubar` - Menu bar app (macOS)

### Source Code Organization
- `cmd/server/` - Main server entry point
- `internal/server/server.go` - Server initialization and dependency injection
- `internal/*http/` - HTTP handlers (modular by domain)
- `internal/llm/` - LLM provider abstraction layer
- `internal/toolapi/` - Internal tool interface definitions
- `internal/mcp/` - MCP server integration
- `internal/skills/` - Skills management
- `internal/workspace/` - Multi-agent workspace system
- `internal/orchestration/` - Multi-agent orchestration
- `internal/web/` - Web server, templates, static files

## Key Development Patterns

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
- `workspaceStore` - Workspace storage
- `taskExecutor` / `stepExecutor` - Task execution
- `taskScheduler` - Scheduled task management
- `eventBus` - Inter-agent event system
- `costTracker` - LLM usage cost tracking
- Multiple HTTP handlers (modular by domain)

## Testing Strategy

### Smoke Testing (manual verification against a running server)

**Always isolate smoke servers from real app data.** `DefaultWorkspaceRoot()` resolves to `$HOME/Ori Workspaces` and does NOT respect `ORI_DATA_DIR` (which only scopes the database, vaults, and templates). A smoke server started without isolation will write workspaces into the user's real tree.

Required recipe:

```bash
SMOKE_DIR=$(mktemp -d)
# HOME override redirects "Ori Workspaces"; ORI_DATA_DIR redirects DB/vaults/templates
HOME="$SMOKE_DIR" ORI_DATA_DIR="$SMOKE_DIR" PORT=8931 ./bin/ori-agent
```

Rules:
- Start the server as a tracked background process (`run_in_background`) and stop it by PID — never `pkill -f <pattern>`.
- Keep every smoke artifact under `$SMOKE_DIR`; cleanup is then a single `rm -rf "$SMOKE_DIR"` of a temp path. Nothing under the real `$HOME` should ever need deleting after a smoke test.
- Run destructive commands (`rm`, `kill`) as standalone commands, not chained with `&&`/`;` onto safe ones — chaining forces a permission prompt for the whole compound and a denial kills the safe parts too.

### Unit & Integration Tests

- Unit tests for providers: `internal/llm/*_test.go`
- Integration tests: `internal/llm/integration_test.go`
- Test data isolated per package
- Use context for timeouts in tests
- Mock providers available for testing

## Common Issues & Solutions

### "cannot load module X listed in go.work file"
There is a `go.work` file in the parent directory. Edit it and remove non-existent module references.

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
- ✅ "Add workspace skill bindings"
- ✅ "Implement Claude provider cost tracking"
- ✅ "Fix MCP server connection handling"
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

## Skills

When I use these words/phrases, invoke the corresponding skill:

| Trigger | Skill |
|---------|-------|
| "commit", "commit this", "commit changes" | `/git-commit-message` (git-commit-message) |
