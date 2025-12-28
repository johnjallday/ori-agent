package llm

import (
	"net/http"
	"time"
)

// Common timeout constants for LLM providers
const (
	// DefaultCloudTimeout is the default timeout for cloud-based providers (OpenAI, Claude)
	DefaultCloudTimeout = 10 * time.Minute

	// DefaultLocalTimeout is the default timeout for local providers (Ollama)
	DefaultLocalTimeout = 5 * time.Minute

	// DefaultModelFetchTimeout is the timeout for fetching available models
	DefaultModelFetchTimeout = 5 * time.Second
)

// NewHTTPClient creates an HTTP client with the specified timeout.
// This centralizes HTTP client creation for all providers.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
	}
}

// CloudProviderCapabilities returns common capabilities for cloud-based LLM providers.
// Individual providers can override specific fields as needed.
func CloudProviderCapabilities(maxContextWindow int) ProviderCapabilities {
	return ProviderCapabilities{
		SupportsTools:          true,
		SupportsStreaming:      true,
		SupportsSystemPrompt:   true,
		SupportsTemperature:    true,
		RequiresAPIKey:         true,
		SupportsCustomEndpoint: false,
		MaxContextWindow:       maxContextWindow,
		SupportedFormats:       []string{"text"},
	}
}

// LocalProviderCapabilities returns common capabilities for local LLM providers.
func LocalProviderCapabilities(maxContextWindow int) ProviderCapabilities {
	return ProviderCapabilities{
		SupportsTools:          true,
		SupportsStreaming:      true,
		SupportsSystemPrompt:   true,
		SupportsTemperature:    true,
		RequiresAPIKey:         false,
		SupportsCustomEndpoint: true,
		MaxContextWindow:       maxContextWindow,
		SupportedFormats:       []string{"text"},
	}
}
