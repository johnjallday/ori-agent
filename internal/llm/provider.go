package llm

import "context"

// Provider defines the interface for all LLM providers
type Provider interface {
	// Chat sends a message and returns a complete response
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// StreamChat sends a message and streams the response
	StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error)

	// Name returns the provider name (e.g., "openai", "claude", "ollama")
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
	SupportsTools bool

	// SupportsStreaming indicates if the provider supports streaming responses
	SupportsStreaming bool

	// SupportsSystemPrompt indicates if the provider supports system prompts
	SupportsSystemPrompt bool

	// SupportsTemperature indicates if the provider supports temperature parameter
	SupportsTemperature bool

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
	Options map[string]interface{}
}
