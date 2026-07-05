package llm

import "context"

// Provider defines the interface for all LLM providers
type Provider interface {
	// Chat sends a message and returns a complete response
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// StreamChat sends a message and streams the response
	StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error)

	// Name returns the provider name (e.g., "openai", "codex", "claude_code", "claude", "gemini", "ollama", "lmstudio", "mlx_lm")
	Name() string

	// Type returns the provider type (cloud, local, hybrid)
	Type() ProviderType

	// Capabilities returns the provider's capabilities
	Capabilities() ProviderCapabilities

	// ValidateConfig validates the provider configuration
	ValidateConfig(config ProviderConfig) error

	// DefaultModels returns a list of available models for this provider
	DefaultModels() []string
}

// ModelPresenceChecker allows providers to report whether a model is currently available.
// Local providers use this to infer provider ownership from a model identifier and to
// validate configured system models against the running local server.
type ModelPresenceChecker interface {
	HasModel(modelName string) bool
}

// StructuredOutputProvider supports structured output requests with a JSON schema.
type StructuredOutputProvider interface {
	ChatWithStructuredOutput(ctx context.Context, req StructuredOutputRequest) (*ChatResponse, error)
}

// ModelContextWindowResolver reports a model-specific context window (in tokens)
// when a provider can resolve one (e.g. from per-model config). Return 0 to defer
// to Capabilities().MaxContextWindow. Prefer ResolveModelContextWindow, which
// applies that fallback.
type ModelContextWindowResolver interface {
	ModelContextWindow(model string) int
}

// ResolveModelContextWindow returns the best-known context window for a model:
// a provider's per-model override (when it implements ModelContextWindowResolver
// and returns a positive value), otherwise its Capabilities().MaxContextWindow.
func ResolveModelContextWindow(provider Provider, model string) int {
	if provider == nil {
		return 0
	}
	if r, ok := provider.(ModelContextWindowResolver); ok {
		if n := r.ModelContextWindow(model); n > 0 {
			return n
		}
	}
	return provider.Capabilities().MaxContextWindow
}

// StreamReader provides an interface for reading streamed responses
type StreamReader interface {
	// Next returns the next chunk of the response
	Next() (*StreamChunk, error)

	// Close closes the stream
	Close() error
}

// StreamChunk represents a chunk of streamed response
type StreamChunk struct {
	Content  string
	ToolCall *ToolCall
	Done     bool

	// Usage carries token usage and is populated only on the final (Done)
	// chunk by providers that report it (e.g. Ollama's eval counters). Zero on
	// intermediate chunks.
	Usage Usage
}

// ProviderType categorizes providers
type ProviderType string

const (
	// ProviderTypeCloud for cloud-based providers (OpenAI, Claude, Gemini)
	ProviderTypeCloud ProviderType = "cloud"

	// ProviderTypeLocal for local/self-hosted providers (Ollama, LocalAI)
	ProviderTypeLocal ProviderType = "local"

	// ProviderTypeHybrid for providers that can be both cloud and local
	ProviderTypeHybrid ProviderType = "hybrid"
)

// ProviderCapabilities describes what a provider supports
type ProviderCapabilities struct {
	// SupportsTools indicates if the provider supports function/tool calling
	// via ori-agent's internal tool loop (tool defs in, tool calls out).
	SupportsTools bool

	// SupportsNativeMCP indicates the provider runs its own MCP loop (e.g. a CLI
	// agent given ChatRequest.MCPServers connects to those servers and executes
	// tool calls itself). This is independent of SupportsTools: native-MCP
	// providers keep SupportsTools=false because they do not round-trip tool
	// calls through ori-agent. Defaults false (only the CLI providers set it).
	SupportsNativeMCP bool

	// SupportsStreaming indicates if the provider supports streaming responses
	SupportsStreaming bool

	// SupportsSystemPrompt indicates if the provider supports system prompts
	SupportsSystemPrompt bool

	// SupportsTemperature indicates if the provider supports temperature parameter
	SupportsTemperature bool

	// SupportsStructuredOutput indicates the provider enforces
	// ChatRequest.ResponseSchema via runtime-constrained decoding on the plain
	// Chat path (Ollama "format", OpenAI-compatible "response_format"). Providers
	// without it ignore ResponseSchema.
	SupportsStructuredOutput bool

	// RequiresAPIKey indicates if an API key is required
	RequiresAPIKey bool

	// SupportsCustomEndpoint indicates if custom base URLs are supported
	SupportsCustomEndpoint bool

	// MaxContextWindow is the maximum context window size in tokens
	MaxContextWindow int

	// SupportedFormats lists supported content formats (text, image, audio, etc.)
	SupportedFormats []string
}

// ProviderConfig holds provider-specific configuration
type ProviderConfig struct {
	// Common fields
	APIKey      string
	BaseURL     string // For Ollama, LocalAI, custom endpoints
	Model       string
	Temperature float64
	MaxTokens   int

	// Provider-specific options (stored as map for flexibility)
	Options map[string]any
}
