# 🌐 Multi-Provider Strategy: Supporting Ollama & Beyond

This document extends the Claude Integration Plan to support **Ollama**, **local models**, and **future providers** (Gemini, Cohere, Mistral, etc.).

## Table of Contents
- [Why This Changes the Approach](#why-this-changes-the-approach)
- [Updated Recommendation](#updated-recommendation)
- [Provider Abstraction Layer (Extended)](#provider-abstraction-layer-extended)
- [Provider-Specific Considerations](#provider-specific-considerations)
- [Implementation Roadmap](#implementation-roadmap)
- [Architecture for Future Providers](#architecture-for-future-providers)

---

## Why This Changes the Approach

### Planning for Ollama Changes Everything

**Ollama Characteristics:**
- ✅ **Local/Self-Hosted**: Runs on user's machine or private server
- ✅ **No API Key Required**: Authentication is optional/different
- ✅ **Custom Endpoint**: User-configurable base URL (e.g., `http://localhost:11434`)
- ✅ **Multiple Models**: Supports many open-source models (Llama, Mistral, Gemma, etc.)
- ✅ **Different API Format**: OpenAI-compatible API but with differences
- ⚠️ **Tool Calling Varies**: Not all models support function calling
- ⚠️ **No Built-in Streaming**: Different streaming implementation

### Why Approach 1 (Abstraction) Becomes **Essential**

| Aspect | Approach 2 (Dual Client) | Approach 1 (Abstraction) |
|--------|-------------------------|-------------------------|
| **Adding Ollama** | Requires another if/else branch | Just add new provider |
| **Code Duplication** | High (3+ separate handlers) | None (shared interface) |
| **Maintenance** | Nightmare with 3+ providers | Easy (isolated providers) |
| **Testing** | Must test all paths | Test provider independently |
| **Future Providers** | Gets worse with each addition | Scales effortlessly |

### The Verdict

**If you plan to support Ollama (or any future providers), Approach 1 is the ONLY viable option.**

Approach 2 becomes unmaintainable with 3+ providers. You'd have:
```go
switch provider {
case "openai":
    return h.handleOpenAIChat(...)
case "claude":
    return h.handleClaudeChat(...)
case "ollama":
    return h.handleOllamaChat(...)
case "gemini":
    return h.handleGeminiChat(...)
case "cohere":
    return h.handleCohereChat(...)
// ... this list keeps growing
}
```

---

## Updated Recommendation

### **Strongly Recommended: Provider Abstraction Layer**

**Immediate Benefits:**
- Support OpenAI, Claude, Ollama from day one
- Easy to add Gemini, Cohere, Mistral later
- Clean, maintainable codebase
- Future-proof architecture

**Investment:**
- **Week 1-2**: Build abstraction layer (one-time cost)
- **Week 3**: Add OpenAI provider (refactor existing)
- **Week 4**: Add Claude provider
- **Week 5**: Add Ollama provider
- **Future**: Each new provider takes ~1-2 days

---

## Provider Abstraction Layer (Extended)

### Updated Architecture

```
┌─────────────────────────────────────────────────────┐
│              Chat Handler (Provider-Agnostic)       │
└────────────────────────┬────────────────────────────┘
                         │
              ┌──────────▼──────────┐
              │   Provider Registry │
              │   (Factory Pattern) │
              └──────────┬──────────┘
                         │
         ┌───────────────┼───────────────┬──────────────┐
         │               │               │              │
    ┌────▼────┐    ┌────▼────┐    ┌────▼────┐   ┌────▼────┐
    │ OpenAI  │    │ Claude  │    │ Ollama  │   │ Future  │
    │Provider │    │Provider │    │Provider │   │Provider │
    └─────────┘    └─────────┘    └─────────┘   └─────────┘
         │               │               │              │
    ┌────▼────┐    ┌────▼────┐    ┌────▼────┐   ┌────▼────┐
    │OpenAI   │    │Anthropic│    │Local    │   │ Other   │
    │  SDK    │    │  SDK    │    │HTTP API │   │  SDKs   │
    └─────────┘    └─────────┘    └─────────┘   └─────────┘
```

### Enhanced Provider Interface

**File:** `internal/llm/provider.go`

```go
package llm

import "context"

// Provider defines the interface for all LLM providers
type Provider interface {
    // Core functionality
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error)

    // Metadata
    Name() string
    Type() ProviderType // "cloud", "local", "hybrid"

    // Capabilities
    Capabilities() ProviderCapabilities

    // Configuration
    ValidateConfig(config ProviderConfig) error
    DefaultModels() []string
}

// ProviderType categorizes providers
type ProviderType string

const (
    ProviderTypeCloud  ProviderType = "cloud"  // OpenAI, Claude, Gemini
    ProviderTypeLocal  ProviderType = "local"  // Ollama, LocalAI
    ProviderTypeHybrid ProviderType = "hybrid" // Can be both
)

// ProviderCapabilities describes what a provider supports
type ProviderCapabilities struct {
    SupportsTools        bool
    SupportsStreaming    bool
    SupportsSystemPrompt bool
    SupportsTemperature  bool
    RequiresAPIKey       bool
    SupportsCustomEndpoint bool
    MaxContextWindow     int
    SupportedFormats     []string // "text", "image", "audio", etc.
}

// ProviderConfig holds provider-specific configuration
type ProviderConfig struct {
    // Common fields
    APIKey      string
    BaseURL     string // For Ollama, LocalAI, custom endpoints
    Model       string
    Temperature float64
    MaxTokens   int

    // Provider-specific options
    Options map[string]interface{}
}
```

### Ollama Provider Implementation

**File:** `internal/llm/ollama_provider.go`

```go
package llm

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
)

type OllamaProvider struct {
    baseURL    string
    httpClient *http.Client
    config     ProviderConfig
}

func NewOllamaProvider(config ProviderConfig) *OllamaProvider {
    baseURL := config.BaseURL
    if baseURL == "" {
        baseURL = "http://localhost:11434" // Default Ollama endpoint
    }

    return &OllamaProvider{
        baseURL:    baseURL,
        httpClient: &http.Client{Timeout: 120 * time.Second}, // Local models can be slow
        config:     config,
    }
}

func (p *OllamaProvider) Name() string {
    return "ollama"
}

func (p *OllamaProvider) Type() ProviderType {
    return ProviderTypeLocal
}

func (p *OllamaProvider) Capabilities() ProviderCapabilities {
    return ProviderCapabilities{
        SupportsTools:          true,  // Some models support it
        SupportsStreaming:      true,
        SupportsSystemPrompt:   true,
        SupportsTemperature:    true,
        RequiresAPIKey:         false, // Ollama doesn't require API key
        SupportsCustomEndpoint: true,
        MaxContextWindow:       8192,  // Varies by model
        SupportedFormats:       []string{"text"},
    }
}

func (p *OllamaProvider) DefaultModels() []string {
    return []string{
        "llama3.2:latest",
        "llama3.1:latest",
        "mistral:latest",
        "gemma2:latest",
        "qwen2.5:latest",
        "codellama:latest",
    }
}

func (p *OllamaProvider) ValidateConfig(config ProviderConfig) error {
    // Check if Ollama is reachable
    resp, err := p.httpClient.Get(p.baseURL + "/api/tags")
    if err != nil {
        return fmt.Errorf("ollama not reachable at %s: %w", p.baseURL, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        return fmt.Errorf("ollama returned status %d", resp.StatusCode)
    }

    return nil
}

func (p *OllamaProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
    // Build Ollama API request
    ollamaReq := map[string]interface{}{
        "model": req.Model,
        "messages": convertMessagesToOllamaFormat(req.Messages, req.SystemPrompt),
        "stream": false,
        "options": map[string]interface{}{
            "temperature": req.Temperature,
        },
    }

    // Add tools if supported
    if len(req.Tools) > 0 {
        ollamaReq["tools"] = convertToolsToOllamaFormat(req.Tools)
    }

    // Make HTTP request
    reqBody, _ := json.Marshal(ollamaReq)
    httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(reqBody))
    if err != nil {
        return nil, err
    }
    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := p.httpClient.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("ollama request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("ollama error %d: %s", resp.StatusCode, string(body))
    }

    // Parse response
    var ollamaResp struct {
        Message struct {
            Role    string `json:"role"`
            Content string `json:"content"`
            ToolCalls []struct {
                Function struct {
                    Name      string                 `json:"name"`
                    Arguments map[string]interface{} `json:"arguments"`
                } `json:"function"`
            } `json:"tool_calls,omitempty"`
        } `json:"message"`
        Done      bool  `json:"done"`
        TotalDuration int64 `json:"total_duration,omitempty"`
    }

    if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
        return nil, fmt.Errorf("failed to decode ollama response: %w", err)
    }

    // Convert to standard response
    response := &ChatResponse{
        Content: ollamaResp.Message.Content,
        Model:   req.Model,
        Usage: Usage{
            // Ollama doesn't return token counts in all responses
            // We could estimate or leave as 0
        },
    }

    // Convert tool calls
    for _, tc := range ollamaResp.Message.ToolCalls {
        argsJSON, _ := json.Marshal(tc.Function.Arguments)
        response.ToolCalls = append(response.ToolCalls, ToolCall{
            Name:      tc.Function.Name,
            Arguments: string(argsJSON),
        })
    }

    return response, nil
}

func (p *OllamaProvider) StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error) {
    // Implement streaming for Ollama
    // Similar to Chat but with stream: true and SSE parsing
    return nil, fmt.Errorf("streaming not yet implemented for Ollama")
}

func convertMessagesToOllamaFormat(messages []Message, systemPrompt string) []map[string]string {
    ollamaMessages := []map[string]string{}

    // Add system prompt if provided
    if systemPrompt != "" {
        ollamaMessages = append(ollamaMessages, map[string]string{
            "role":    "system",
            "content": systemPrompt,
        })
    }

    // Convert messages
    for _, msg := range messages {
        ollamaMessages = append(ollamaMessages, map[string]string{
            "role":    msg.Role,
            "content": msg.Content,
        })
    }

    return ollamaMessages
}

func convertToolsToOllamaFormat(tools []Tool) []map[string]interface{} {
    // Ollama uses OpenAI-compatible tool format
    ollamaTools := []map[string]interface{}{}

    for _, tool := range tools {
        ollamaTools = append(ollamaTools, map[string]interface{}{
            "type": "function",
            "function": map[string]interface{}{
                "name":        tool.Name,
                "description": tool.Description,
                "parameters":  tool.Parameters,
            },
        })
    }

    return ollamaTools
}
```

### Enhanced Factory with Provider Registry

**File:** `internal/llm/factory.go`

```go
package llm

import (
    "fmt"
    "strings"
    "sync"
)

// Factory manages provider instances with a registry pattern
type Factory struct {
    providers map[string]Provider
    mu        sync.RWMutex
}

// NewFactory creates a factory with all available providers
func NewFactory(configs map[string]ProviderConfig) *Factory {
    f := &Factory{
        providers: make(map[string]Provider),
    }

    // Register OpenAI if configured
    if cfg, ok := configs["openai"]; ok && cfg.APIKey != "" {
        f.Register("openai", NewOpenAIProvider(cfg))
    }

    // Register Claude if configured
    if cfg, ok := configs["claude"]; ok && cfg.APIKey != "" {
        f.Register("claude", NewClaudeProvider(cfg))
    }

    // Register Ollama if configured (no API key required)
    if cfg, ok := configs["ollama"]; ok {
        f.Register("ollama", NewOllamaProvider(cfg))
    }

    return f
}

// Register adds a provider to the registry
func (f *Factory) Register(name string, provider Provider) {
    f.mu.Lock()
    defer f.mu.Unlock()
    f.providers[strings.ToLower(name)] = provider
}

// GetProvider returns a provider by name
func (f *Factory) GetProvider(name string) (Provider, error) {
    f.mu.RLock()
    defer f.mu.RUnlock()

    provider, ok := f.providers[strings.ToLower(name)]
    if !ok {
        return nil, fmt.Errorf("provider %q not found or not configured", name)
    }

    return provider, nil
}

// ListProviders returns all registered provider names
func (f *Factory) ListProviders() []ProviderInfo {
    f.mu.RLock()
    defer f.mu.RUnlock()

    var providers []ProviderInfo
    for name, provider := range f.providers {
        providers = append(providers, ProviderInfo{
            Name:         name,
            Type:         provider.Type(),
            Capabilities: provider.Capabilities(),
            Models:       provider.DefaultModels(),
        })
    }

    return providers
}

// ProviderInfo contains metadata about a provider
type ProviderInfo struct {
    Name         string
    Type         ProviderType
    Capabilities ProviderCapabilities
    Models       []string
}

// GetForAgent returns a provider configured for the given agent
func (f *Factory) GetForAgent(agent *types.Agent) (Provider, error) {
    providerName := agent.Settings.Provider
    if providerName == "" {
        providerName = "openai" // default
    }

    return f.GetProvider(providerName)
}
```

---

## Provider-Specific Considerations

### OpenAI
**Characteristics:**
- ✅ Cloud-based
- ✅ Requires API key
- ✅ Strong tool calling support
- ✅ Streaming support
- 💰 Paid service

**Configuration:**
```json
{
  "provider": "openai",
  "api_key": "sk-...",
  "model": "gpt-4o"
}
```

---

### Claude (Anthropic)
**Characteristics:**
- ✅ Cloud-based
- ✅ Requires API key
- ✅ Excellent tool calling
- ✅ Streaming support
- ⚠️ Requires `max_tokens` parameter
- 💰 Paid service

**Configuration:**
```json
{
  "provider": "claude",
  "claude_api_key": "sk-ant-...",
  "model": "claude-3-5-sonnet-20241022",
  "max_tokens": 4096
}
```

---

### Ollama
**Characteristics:**
- ✅ Local/Self-hosted
- ✅ No API key required
- ✅ Multiple open-source models
- ⚠️ Tool calling depends on model
- ⚠️ Slower inference (CPU/GPU dependent)
- ⚠️ Requires local installation
- 💰 Free (compute costs only)

**Configuration:**
```json
{
  "provider": "ollama",
  "base_url": "http://localhost:11434",
  "model": "llama3.2:latest"
}
```

**Setup Required:**
```bash
# Install Ollama
curl -fsSL https://ollama.ai/install.sh | sh

# Pull a model
ollama pull llama3.2

# Verify it's running
curl http://localhost:11434/api/tags
```

---

### Future Providers

#### Google Gemini
**Characteristics:**
- ✅ Cloud-based
- ✅ Requires API key
- ✅ Multimodal (text, image, video)
- ✅ Free tier available

**Implementation:**
```go
type GeminiProvider struct {
    client *genai.Client
}

func (p *GeminiProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
    // Use Google Generative AI SDK
}
```

#### Cohere
**Characteristics:**
- ✅ Cloud-based
- ✅ Enterprise focus
- ✅ Good for embeddings

#### Mistral AI
**Characteristics:**
- ✅ Cloud & local options
- ✅ European alternative
- ✅ Open-source models

#### LocalAI
**Characteristics:**
- ✅ Self-hosted
- ✅ OpenAI-compatible API
- ✅ Multiple model backends

---

## Updated Settings Structure

### Enhanced Settings Type

**File:** `internal/types/types.go`

```go
type Settings struct {
    // Provider selection
    Provider string `json:"provider"` // "openai", "claude", "ollama", "gemini", etc.

    // Model configuration
    Model       string  `json:"model"`
    Temperature float64 `json:"temperature"`
    MaxTokens   int     `json:"max_tokens,omitempty"`

    // API keys (provider-specific)
    APIKey       string `json:"api_key,omitempty"`        // OpenAI
    ClaudeAPIKey string `json:"claude_api_key,omitempty"` // Claude
    GeminiAPIKey string `json:"gemini_api_key,omitempty"` // Gemini

    // Endpoint configuration (for local/custom providers)
    BaseURL string `json:"base_url,omitempty"` // Ollama, LocalAI, custom endpoints

    // Common settings
    SystemPrompt string `json:"system_prompt,omitempty"`

    // Provider-specific options
    ProviderOptions map[string]interface{} `json:"provider_options,omitempty"`
}
```

---

## UI Updates for Multi-Provider Support

### Enhanced Provider Selector

**File:** `internal/web/templates/sidebar.tmpl`

```html
<!-- Provider Selection -->
<div class="form-group">
    <label for="provider-select">
        <i class="bi bi-cloud"></i> Provider
    </label>
    <select class="form-control" id="provider-select">
        <optgroup label="Cloud Providers">
            <option value="openai">OpenAI</option>
            <option value="claude">Anthropic Claude</option>
            <option value="gemini">Google Gemini</option>
        </optgroup>
        <optgroup label="Local/Self-Hosted">
            <option value="ollama">Ollama</option>
            <option value="localai">LocalAI</option>
        </optgroup>
    </select>
</div>

<!-- Provider Type Badge -->
<div class="provider-info mb-3">
    <span class="badge badge-info" id="provider-type-badge">Cloud</span>
    <span class="badge badge-success" id="provider-status-badge">Connected</span>
</div>

<!-- OpenAI Configuration -->
<div id="openai-config" class="provider-config">
    <div class="form-group">
        <label>OpenAI Model</label>
        <select class="form-control" id="openai-model">
            <option value="gpt-4o">GPT-4o</option>
            <option value="gpt-4o-mini">GPT-4o Mini</option>
            <option value="gpt-4-turbo">GPT-4 Turbo</option>
        </select>
    </div>
    <div class="form-group">
        <label>API Key</label>
        <input type="password" class="form-control" id="openai-api-key">
    </div>
</div>

<!-- Claude Configuration -->
<div id="claude-config" class="provider-config" style="display:none;">
    <div class="form-group">
        <label>Claude Model</label>
        <select class="form-control" id="claude-model">
            <option value="claude-3-5-sonnet-20241022">Claude 3.5 Sonnet</option>
            <option value="claude-3-opus-20240229">Claude 3 Opus</option>
        </select>
    </div>
    <div class="form-group">
        <label>API Key</label>
        <input type="password" class="form-control" id="claude-api-key">
    </div>
</div>

<!-- Ollama Configuration -->
<div id="ollama-config" class="provider-config" style="display:none;">
    <div class="alert alert-info">
        <i class="bi bi-info-circle"></i>
        <strong>Local Model:</strong> Requires Ollama installed locally.
        <a href="https://ollama.ai" target="_blank">Download Ollama</a>
    </div>

    <div class="form-group">
        <label>Base URL</label>
        <input type="text" class="form-control" id="ollama-base-url"
               value="http://localhost:11434" placeholder="http://localhost:11434">
        <small class="form-text text-muted">Default: http://localhost:11434</small>
    </div>

    <div class="form-group">
        <label>Model</label>
        <select class="form-control" id="ollama-model">
            <option value="llama3.2:latest">Llama 3.2</option>
            <option value="llama3.1:latest">Llama 3.1</option>
            <option value="mistral:latest">Mistral</option>
            <option value="gemma2:latest">Gemma 2</option>
            <option value="qwen2.5:latest">Qwen 2.5</option>
        </select>
    </div>

    <button class="btn btn-sm btn-outline-primary" id="test-ollama-connection">
        <i class="bi bi-link"></i> Test Connection
    </button>

    <button class="btn btn-sm btn-outline-info" id="pull-ollama-model">
        <i class="bi bi-download"></i> Pull Model
    </button>
</div>
```

### JavaScript for Dynamic Provider UI

**File:** `internal/web/static/js/app.js`

```javascript
// Provider switching logic
const providerConfigs = {
    openai: { type: 'cloud', requiresKey: true },
    claude: { type: 'cloud', requiresKey: true },
    gemini: { type: 'cloud', requiresKey: true },
    ollama: { type: 'local', requiresKey: false },
    localai: { type: 'local', requiresKey: false }
};

document.getElementById('provider-select').addEventListener('change', function(e) {
    const provider = e.target.value;
    const config = providerConfigs[provider];

    // Update badges
    const typeBadge = document.getElementById('provider-type-badge');
    typeBadge.textContent = config.type === 'cloud' ? 'Cloud' : 'Local';
    typeBadge.className = `badge ${config.type === 'cloud' ? 'badge-info' : 'badge-warning'}`;

    // Hide all provider configs
    document.querySelectorAll('.provider-config').forEach(el => {
        el.style.display = 'none';
    });

    // Show selected provider config
    document.getElementById(`${provider}-config`).style.display = 'block';
});

// Test Ollama connection
document.getElementById('test-ollama-connection')?.addEventListener('click', async function() {
    const baseUrl = document.getElementById('ollama-base-url').value;

    try {
        const response = await fetch(`${baseUrl}/api/tags`);
        if (response.ok) {
            showNotification('Ollama connection successful!', 'success');
            updateProviderStatus('connected');
        } else {
            showNotification('Failed to connect to Ollama', 'error');
            updateProviderStatus('disconnected');
        }
    } catch (error) {
        showNotification(`Error: ${error.message}`, 'error');
        updateProviderStatus('disconnected');
    }
});

// Pull Ollama model
document.getElementById('pull-ollama-model')?.addEventListener('click', async function() {
    const baseUrl = document.getElementById('ollama-base-url').value;
    const model = document.getElementById('ollama-model').value;

    showNotification(`Pulling model: ${model}...`, 'info');

    try {
        const response = await fetch(`${baseUrl}/api/pull`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: model })
        });

        if (response.ok) {
            showNotification(`Model ${model} pulled successfully!`, 'success');
        } else {
            showNotification('Failed to pull model', 'error');
        }
    } catch (error) {
        showNotification(`Error: ${error.message}`, 'error');
    }
});

function updateProviderStatus(status) {
    const badge = document.getElementById('provider-status-badge');
    if (status === 'connected') {
        badge.textContent = 'Connected';
        badge.className = 'badge badge-success';
    } else {
        badge.textContent = 'Disconnected';
        badge.className = 'badge badge-danger';
    }
}
```

---

## Implementation Roadmap

### Phase 1: Foundation (Weeks 1-2)
**Goal:** Build provider abstraction layer

- [ ] Day 1-2: Design provider interface
- [ ] Day 3-4: Implement base types and capabilities
- [ ] Day 5-6: Create factory and registry
- [ ] Day 7-8: Build test framework
- [ ] Day 9-10: Documentation

**Deliverable:** Provider interface ready for implementations

---

### Phase 2: OpenAI Migration (Week 3)
**Goal:** Refactor existing OpenAI code into provider pattern

- [ ] Day 1-2: Implement OpenAI provider
- [ ] Day 3-4: Migrate chat handler to use provider interface
- [ ] Day 5: Test and validate (should work identically)

**Deliverable:** OpenAI working through new abstraction

---

### Phase 3: Claude Integration (Week 4)
**Goal:** Add Claude support

- [ ] Day 1-2: Implement Claude provider
- [ ] Day 3: Test tool calling
- [ ] Day 4: Update UI for provider selection
- [ ] Day 5: Integration testing

**Deliverable:** OpenAI + Claude working

---

### Phase 4: Ollama Support (Week 5)
**Goal:** Add local model support

- [ ] Day 1: Implement Ollama provider
- [ ] Day 2: Add Ollama-specific UI (base URL, model pulling)
- [ ] Day 3: Test with different models (Llama, Mistral, etc.)
- [ ] Day 4: Add connection testing
- [ ] Day 5: Documentation and user guide

**Deliverable:** OpenAI + Claude + Ollama working

---

### Phase 5: Polish & Future-Proofing (Week 6)
**Goal:** Production-ready multi-provider system

- [ ] Day 1: Add provider health checks
- [ ] Day 2: Add provider metrics/analytics
- [ ] Day 3: Improve error handling
- [ ] Day 4: Performance optimization
- [ ] Day 5: Final testing and deployment

**Deliverable:** Production-ready system

---

### Future Phases (Post-Launch)

**Phase 6: Additional Providers** (1-2 days each)
- Gemini (Google)
- Cohere
- Mistral AI
- LocalAI
- HuggingFace Inference

**Phase 7: Advanced Features**
- Provider cost tracking
- Automatic failover
- Load balancing across providers
- Provider performance analytics

---

## Architecture for Future Providers

### Plugin-Like Provider System

Providers become "plugins" themselves:

```go
// Provider registry discovers providers at runtime
type ProviderRegistry struct {
    providers map[string]ProviderConstructor
}

type ProviderConstructor func(config ProviderConfig) (Provider, error)

// Register new providers
registry.Register("openai", NewOpenAIProvider)
registry.Register("claude", NewClaudeProvider)
registry.Register("ollama", NewOllamaProvider)
registry.Register("gemini", NewGeminiProvider) // Future
```

This allows:
- ✅ Easy addition of new providers
- ✅ Third-party provider extensions
- ✅ Hot-swapping providers
- ✅ A/B testing different providers

---

## Cost & Performance Considerations

### Provider Comparison

| Provider | Type | Cost | Speed | Tool Support | Privacy |
|----------|------|------|-------|--------------|---------|
| **OpenAI GPT-4o** | Cloud | $$$ | Fast | ⭐⭐⭐⭐⭐ | Medium |
| **OpenAI GPT-4o-mini** | Cloud | $ | Very Fast | ⭐⭐⭐⭐⭐ | Medium |
| **Claude 3.5 Sonnet** | Cloud | $$$ | Fast | ⭐⭐⭐⭐⭐ | Medium |
| **Claude 3 Haiku** | Cloud | $ | Very Fast | ⭐⭐⭐⭐ | Medium |
| **Ollama (Llama 3.2)** | Local | Free* | Slow† | ⭐⭐⭐ | High |
| **Ollama (Mistral)** | Local | Free* | Slow† | ⭐⭐⭐⭐ | High |
| **Gemini Pro** | Cloud | $$ | Fast | ⭐⭐⭐⭐ | Medium |

*Free but requires local compute (GPU recommended)
†Speed depends on hardware (GPU vs CPU)

### When to Use Each Provider

**OpenAI:**
- Best for: Production apps, complex reasoning, tool calling
- Use when: Budget allows, need reliability

**Claude:**
- Best for: Long context, coding tasks, detailed analysis
- Use when: Need strong tool calling, enterprise features

**Ollama:**
- Best for: Privacy-sensitive use cases, offline usage, experimentation
- Use when: Data privacy is critical, no internet, free tier needed

**Gemini:**
- Best for: Multimodal tasks, Google ecosystem integration
- Use when: Need image/video processing

---

## Migration Strategy

### Backward Compatibility

**Existing configs without provider field:**
```json
{
  "model": "gpt-4o",
  "api_key": "sk-..."
}
```

**Auto-migrated to:**
```json
{
  "provider": "openai",
  "model": "gpt-4o",
  "api_key": "sk-..."
}
```

### User Migration Guide

**For OpenAI users:** No changes needed (defaults to OpenAI)

**For Claude users:**
1. Set `provider: "claude"`
2. Add `claude_api_key`
3. Choose Claude model

**For Ollama users:**
1. Install Ollama: `curl -fsSL https://ollama.ai/install.sh | sh`
2. Pull model: `ollama pull llama3.2`
3. Set `provider: "ollama"`
4. Choose model from list

---

## Testing Strategy

### Provider-Specific Tests

```go
// Test each provider independently
func TestOpenAIProvider(t *testing.T) {
    provider := llm.NewOpenAIProvider(config)
    testProviderBasics(t, provider)
    testProviderTools(t, provider)
}

func TestClaudeProvider(t *testing.T) {
    provider := llm.NewClaudeProvider(config)
    testProviderBasics(t, provider)
    testProviderTools(t, provider)
}

func TestOllamaProvider(t *testing.T) {
    if !ollamaAvailable() {
        t.Skip("Ollama not available")
    }
    provider := llm.NewOllamaProvider(config)
    testProviderBasics(t, provider)
}

// Shared test suite for all providers
func testProviderBasics(t *testing.T, provider llm.Provider) {
    // Test basic chat
    // Test message formatting
    // Test error handling
}

func testProviderTools(t *testing.T, provider llm.Provider) {
    if !provider.Capabilities().SupportsTools {
        t.Skip("Provider doesn't support tools")
    }
    // Test tool calling
    // Test tool response handling
}
```

---

## Final Recommendation

### **Definitely Use Provider Abstraction (Approach 1)**

**Why:**
1. ✅ You plan to support Ollama (local models)
2. ✅ You want to support multiple providers
3. ✅ You want a maintainable, scalable codebase
4. ✅ Each new provider takes 1-2 days instead of weeks
5. ✅ Users can switch providers easily
6. ✅ Future-proof for any new LLM that emerges

**Investment:**
- **5-6 weeks** to build abstraction + OpenAI + Claude + Ollama
- **1-2 days** per additional provider after that

**Alternative (Approach 2) would require:**
- **Refactoring everything** when adding Ollama
- **Massive code duplication** with 3+ providers
- **Maintenance nightmare** long-term

### The Choice is Clear

If you're adding Ollama support, **Approach 1 is not optional—it's essential.**

---

**Last Updated:** 2025-10-21
**Status:** Planning Phase
**Recommendation:** Provider Abstraction Layer (Approach 1)
**Timeline:** 5-6 weeks for full implementation
