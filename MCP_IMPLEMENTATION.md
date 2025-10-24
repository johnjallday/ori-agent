# MCP Implementation Game Plan for Dolphin-Agent

## Overview

Model Context Protocol (MCP) is a standardized protocol for communication between AI assistants and external tools/data sources. Adding MCP support will allow ori-agent to:
- Connect to any MCP-compatible server
- Access a broader ecosystem of tools beyond custom plugins
- Support both stdio and SSE transport mechanisms
- Enable dynamic tool discovery and resource management

## Current Architecture Analysis

### Existing Plugin System
- Custom Go plugin interface (`pluginapi.Tool`)
- Direct function calls with JSON args/results
- Agent-aware plugins with context injection
- Configuration management per plugin
- Registry system for local + remote plugins

### Existing Provider Abstraction
- `internal/llm/provider.go` - Provider interface (cloud, local, hybrid)
- Currently only OpenAI client implementation active
- Designed for multi-provider support (OpenAI, Claude, Ollama, Gemini)
- Capability detection (tools, streaming, system prompts, etc.)

### Key Strengths to Preserve
- Per-agent plugin isolation
- Agent context awareness
- Type system integration
- Configuration management

## Multi-Provider Architecture

### Provider Agnostic Design

MCP is **completely provider-agnostic** and will work with ANY LLM provider that supports function/tool calling:

```
┌─────────────────┐
│   User Input    │
└────────┬────────┘
         │
         v
┌─────────────────────────────┐
│    Dolphin Agent            │
│  (Provider-Agnostic Logic)  │
└───────┬─────────────────┬───┘
        │                 │
        v                 v
┌──────────────┐   ┌──────────────────┐
│   Plugins    │   │   MCP Servers    │
│ (Go Native)  │   │  (Any Language)  │
└──────┬───────┘   └─────────┬────────┘
       │                     │
       └──────────┬──────────┘
                  │
                  v
        ┌─────────────────────┐
        │  Unified Tool List   │
        │ (OpenAI Function     │
        │  Definition Format)  │
        └──────────┬───────────┘
                   │
                   v
        ┌──────────────────────┐
        │  Provider Interface  │
        └──────────┬────────────┘
                   │
         ┌─────────┼──────────┬──────────┐
         v         v          v          v
    ┌────────┐ ┌──────┐ ┌────────┐ ┌────────┐
    │ OpenAI │ │Claude│ │ Ollama │ │ Gemini │
    └────────┘ └──────┘ └────────┘ └────────┘
```

### Provider Tool Calling Support

| Provider | Tool Support | Format | Notes |
|----------|-------------|--------|-------|
| **OpenAI** | ✅ Native | OpenAI Functions | Original format |
| **Claude** | ✅ Native | Anthropic Tools | Similar to OpenAI, minor differences |
| **Ollama** | ✅ Depends on model | OpenAI-compatible | Llama 3.1+, Mistral support tools |
| **Gemini** | ✅ Native | Google Function Calling | Similar structure |
| **Local models** | ⚠️ Limited | Varies | Depends on model |

## Implementation Strategy

### Phase 0: Complete Provider Abstraction (RECOMMENDED FIRST)

**Goal**: Make ori-agent truly multi-provider before adding MCP

**1. Implement Concrete Providers**

Create provider implementations in `internal/llm/`:
```
internal/llm/
├── provider.go         # Interface (exists)
├── types.go            # Common types
├── openai/
│   └── provider.go     # OpenAI implementation
├── claude/
│   └── provider.go     # Claude implementation
├── ollama/
│   └── provider.go     # Ollama implementation
├── gemini/
│   └── provider.go     # Gemini implementation (optional)
└── registry.go         # Provider registry
```

**2. Update Agent Structure**

```go
// internal/agent/agent.go
type Agent struct {
    Type        string
    Settings    types.Settings
    Provider    llm.Provider     // Use interface, not OpenAI client
    Plugins     map[string]types.LoadedPlugin
    MCPServers  map[string]*mcp.MCPAdapter  // Added in Phase 2
    Messages    []openai.ChatCompletionMessageParamUnion
}
```

**3. Update Settings**

```go
// internal/types/types.go
type Settings struct {
    Provider     string  `json:"provider"`     // "openai", "claude", "ollama"
    Model        string  `json:"model"`
    Temperature  float64 `json:"temperature"`
    APIKey       string  `json:"api_key,omitempty"`
    BaseURL      string  `json:"base_url,omitempty"`  // For Ollama, custom endpoints
    SystemPrompt string  `json:"system_prompt,omitempty"`
}
```

**4. Provider-Specific Tool Translation**

Each provider converts OpenAI function definitions to its native format:

```go
// Example: Claude provider
type ClaudeProvider struct {
    client *anthropic.Client
}

func (p *ClaudeProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
    // Convert OpenAI function definitions to Claude tools format
    claudeTools := convertToClaudeTools(req.Tools)

    // Call Claude API
    resp, err := p.client.Messages.Create(ctx, anthropic.MessageNewParams{
        Model: req.Model,
        Tools: claudeTools,
        // ...
    })

    // Convert Claude response back to standard format
    return convertClaudeResponse(resp), nil
}
```

### Phase 1: Core MCP Infrastructure (Foundation)

**1.1 Create MCP Package Structure**

```
internal/mcp/
├── protocol.go      # MCP protocol types (messages, requests, responses)
├── client.go        # MCP client implementation (JSON-RPC 2.0)
├── server.go        # MCP server process management
├── adapter.go       # MCP → pluginapi.Tool adapter
├── registry.go      # MCP server registry
├── config.go        # Configuration management
└── transport/
    ├── stdio.go     # Stdio transport (default)
    └── sse.go       # SSE transport (future)
```

**1.2 Core Components**

- **MCP Client**: JSON-RPC 2.0 client for communicating with MCP servers
- **Transport Layer**: Support stdio (default) and SSE transports
- **Process Management**: Lifecycle management for MCP server processes
- **Protocol Types**: Go structs for MCP request/response messages

**Key Types:**

```go
// internal/mcp/protocol.go
type MCPRequest struct {
    JSONRPC string      `json:"jsonrpc"`
    ID      string      `json:"id"`
    Method  string      `json:"method"`
    Params  interface{} `json:"params,omitempty"`
}

type MCPResponse struct {
    JSONRPC string      `json:"jsonrpc"`
    ID      string      `json:"id"`
    Result  interface{} `json:"result,omitempty"`
    Error   *RPCError   `json:"error,omitempty"`
}

type Tool struct {
    Name        string                 `json:"name"`
    Description string                 `json:"description"`
    InputSchema map[string]interface{} `json:"inputSchema"`
}
```

### Phase 2: MCP-to-Plugin Adapter (Bridge Layer)

**2.1 Create Adapter Pattern**

Bridge MCP servers to existing plugin system:

```go
// internal/mcp/adapter.go
type MCPAdapter struct {
    server   *MCPServer       // Running MCP server process
    tools    map[string]Tool  // Discovered MCP tools
    client   *MCPClient       // JSON-RPC client
    agentCtx pluginapi.AgentContext
}

// Implements pluginapi.Tool
func (a *MCPAdapter) Definition() openai.FunctionDefinitionParam {
    // Convert MCP tool schema to OpenAI function definition
    // This format works with ALL providers (OpenAI, Claude, etc.)
}

func (a *MCPAdapter) Call(ctx context.Context, args string) (string, error) {
    // Forward tool call to MCP server via JSON-RPC
}
```

**2.2 Benefits**

- Existing agent system works unchanged
- MCP tools appear as regular plugins
- Unified tool interface for LLM
- Reuse existing configuration/UI

### Phase 3: Configuration & Management

**3.1 MCP Server Configuration**

```go
// internal/types/types.go
type MCPServerConfig struct {
    Name        string            `json:"name"`
    Command     string            `json:"command"`      // e.g., "npx"
    Args        []string          `json:"args"`         // e.g., ["-y", "@modelcontextprotocol/server-filesystem"]
    Env         map[string]string `json:"env"`          // Environment variables
    Transport   string            `json:"transport"`    // "stdio" or "sse"
    Enabled     bool              `json:"enabled"`
}

type MCPRegistry struct {
    Servers []MCPServerConfig `json:"servers"`
}
```

**3.2 Storage**

- Store MCP configs in `agents/{name}/mcp_servers.json`
- Per-agent MCP server enablement
- Global MCP server registry in `mcp_registry.json`

**Example MCP Server Configurations:**

**Filesystem Server:**
```json
{
  "name": "filesystem",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/directory"],
  "env": {},
  "transport": "stdio",
  "enabled": true
}
```

**Git Server:**
```json
{
  "name": "git",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-git", "/path/to/repo"],
  "env": {},
  "transport": "stdio",
  "enabled": true
}
```

### Phase 4: Integration with Existing Systems

**4.1 Update Agent Store**

```go
// internal/store/file_store.go
// Load MCP servers alongside plugins
func (s *fileStore) loadAgent(name string) (*agent.Agent, error) {
    // Load agent settings
    ag := loadAgentSettings(name)

    // Load plugins
    ag.Plugins = loadPlugins(name)

    // Load MCP servers
    ag.MCPServers = loadMCPServers(name)

    return ag, nil
}
```

**4.2 Update Plugin Loading**

Modify `internal/server/server.go`:
- Load MCP servers on agent creation/switch
- Start MCP server processes
- Create MCP adapters
- Register as tools with agent

### Phase 5: HTTP API & Frontend

**5.1 HTTP Endpoints**

Create `internal/mcphttp/handlers.go`:

```
GET    /api/mcp/servers              # List available MCP servers
POST   /api/mcp/servers              # Add new MCP server
DELETE /api/mcp/servers/:name        # Remove MCP server
POST   /api/mcp/servers/:name/enable # Enable for current agent
POST   /api/mcp/servers/:name/disable# Disable for current agent
GET    /api/mcp/servers/:name/tools  # List tools from server
```

**5.2 Frontend UI**

Update templates/JS:
- MCP server management tab in settings (`settings.tmpl`)
- Enable/disable MCP servers per agent
- View available tools from each server
- Add MCP server form (command, args, env)
- `internal/web/static/js/modules/mcp.js` - MCP frontend logic

### Phase 6: Advanced Features (Optional)

**6.1 Resource Support**
- Implement `resources/list` and `resources/read`
- Expose resources as context to LLM

**6.2 Prompts Support**
- Implement `prompts/list` and `prompts/get`
- Allow prompt templates from MCP servers

**6.3 Sampling Support**
- Allow MCP servers to request LLM completions
- Careful security considerations

**6.4 SSE Transport**
- Implement Server-Sent Events transport
- Support remote MCP servers

## Detailed Implementation Steps

### Step 1: MCP Protocol Implementation
1. Create `internal/mcp/protocol.go` with MCP message types
2. Create `internal/mcp/client.go` with JSON-RPC 2.0 client
3. Create `internal/mcp/transport/stdio.go` for process communication
4. Write tests for basic MCP communication

### Step 2: MCP Server Management
1. Create `internal/mcp/server.go` for process lifecycle
2. Implement server start/stop/restart
3. Handle server crashes and restarts
4. Implement tool discovery (`tools/list`)

### Step 3: Adapter Layer
1. Create `internal/mcp/adapter.go`
2. Convert MCP tools to OpenAI function definitions
3. Route tool calls through MCP client
4. Handle errors and timeouts

### Step 4: Configuration
1. Add MCP types to `internal/types/types.go`
2. Create MCP registry file format
3. Implement config loading/saving
4. Per-agent MCP server enablement

### Step 5: Integration
1. Update `Agent` struct with MCP field
2. Load MCP servers on agent switch
3. Start/stop MCP servers with agent lifecycle
4. Add MCP tools to LLM tool list

### Step 6: HTTP API
1. Create MCP HTTP handlers
2. List/add/remove/enable/disable endpoints
3. Tool discovery endpoint

### Step 7: Frontend
1. Add MCP tab to settings page
2. MCP server list UI
3. Add server form
4. Enable/disable per agent
5. View tools from each server

### Step 8: Testing & Polish
1. Test with official MCP servers (filesystem, git, etc.)
2. Handle edge cases (crashes, timeouts)
3. Add logging and debugging
4. Documentation

## Package Dependencies

**Required Go packages:**
```bash
go get github.com/google/uuid              # For request IDs
go get github.com/sourcegraph/jsonrpc2     # JSON-RPC 2.0 (or implement custom)
```

## File Structure After Implementation

```
ori-agent/
├── internal/
│   ├── mcp/
│   │   ├── protocol.go      # MCP types
│   │   ├── client.go        # JSON-RPC client
│   │   ├── server.go        # Server process management
│   │   ├── adapter.go       # MCP → pluginapi.Tool adapter
│   │   ├── registry.go      # Server registry
│   │   ├── config.go        # Configuration management
│   │   └── transport/
│   │       ├── stdio.go     # Stdio transport
│   │       └── sse.go       # SSE transport
│   ├── mcphttp/
│   │   └── handlers.go      # MCP HTTP API
│   ├── llm/
│   │   ├── provider.go      # Provider interface (exists)
│   │   ├── types.go         # Common types
│   │   ├── openai/
│   │   │   └── provider.go  # OpenAI implementation
│   │   ├── claude/
│   │   │   └── provider.go  # Claude implementation
│   │   ├── ollama/
│   │   │   └── provider.go  # Ollama implementation
│   │   └── registry.go      # Provider registry
│   ├── agent/
│   │   └── agent.go         # Add MCPServers field
│   ├── types/
│   │   └── types.go         # Add MCP config types
│   └── web/
│       ├── templates/
│       │   └── settings.tmpl    # Add MCP tab
│       └── static/js/modules/
│           └── mcp.js           # MCP frontend
├── mcp_registry.json        # Global MCP server registry
└── agents/{name}/
    └── mcp_servers.json     # Per-agent MCP configuration
```

## Integration Strategy - Dual System Approach

**1. Keep existing plugin system** - For native Go plugins with tight integration
**2. Add MCP system** - For ecosystem compatibility and external tools
**3. Unified interface** - Both appear as tools to the agent/LLM
**4. User choice** - Users can use plugins, MCP, or both

**Benefits:**
- Backward compatible - existing setup continues working
- Forward compatible - access to MCP ecosystem
- Flexible - use the right tool for the job
- Gradual adoption - can migrate plugins to MCP if desired
- Multi-provider - Works with OpenAI, Claude, Ollama, Gemini, etc.

## Security Considerations

1. **Process Isolation**: MCP servers run as separate processes
2. **Argument Validation**: Validate server commands and args
3. **Path Restrictions**: Limit filesystem access per server config
4. **Per-Agent Enablement**: Fine-grained control over which agents can use which servers
5. **Timeout Enforcement**: Prevent runaway server processes
6. **Resource Limits**: Monitor memory/CPU usage

## Testing Strategy

1. **Unit Tests**: Test protocol encoding/decoding
2. **Integration Tests**: Test with official MCP reference servers
3. **E2E Tests**: Test full workflow through HTTP API
4. **Manual Testing**: Test with real-world MCP servers

## Migration Path

**Phase 0** (Recommended): Complete provider abstraction
**Phase 1** (Core): Native Go plugins + OpenAI only
**Phase 2** (Multi-Provider): Add Claude, Ollama providers
**Phase 3** (MCP Basic): Add MCP support, tools only
**Phase 4** (MCP Advanced): Add resources, prompts
**Phase 5** (MCP Full): Add sampling, SSE transport
**Phase 6** (Optional): Migrate some plugins to MCP if beneficial

## Recommended Implementation Order

1. ✅ **Keep existing OpenAI setup** (works now)
2. **Implement provider abstraction fully** (enables multi-provider)
3. **Add Claude provider** (most requested alternative)
4. **Add Ollama provider** (local/offline support)
5. **Implement MCP support** (works with all providers automatically)
6. **Add other providers** (Gemini, etc.) as needed

## Key Insight

**MCP is a tool protocol, not an LLM protocol.** The LLM just needs to understand function calling (which most modern LLMs do). This means MCP will work with ANY provider that supports function/tool calling:

- ✅ OpenAI (GPT-4, GPT-5, etc.)
- ✅ Claude (via Anthropic API)
- ✅ Ollama (local models)
- ✅ Gemini (Google)
- ✅ Any provider that supports function/tool calling

## Next Steps

Choose your implementation path:

1. **Option A - Provider First**: Implement provider abstraction, then MCP (recommended)
2. **Option B - MCP First**: Implement MCP with OpenAI only, add providers later (faster to MCP)
3. **Option C - Parallel**: Implement provider abstraction AND MCP together (comprehensive)

The architecture is designed to support any of these paths while maintaining backward compatibility and enabling future extensibility.
