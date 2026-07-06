package llm

import (
	"net/http"
	"strings"
	"time"
)

// defaultLocalContextWindow is the conservative fallback context window (in
// tokens) for local models when no per-model or provider-level value is
// configured (WS7.30 replaces the previously hardcoded literal).
const defaultLocalContextWindow = 8192

// resolveContextWindows extracts an optional provider-level default context
// window and per-model overrides from provider config Options. Recognized keys:
//
//	"context_window":  number  — provider-level default (0 = unset)
//	"context_windows": object  — {"<model>": number, ...} per-model overrides
//
// Model keys are lower-cased for case-insensitive lookup.
func resolveContextWindows(config ProviderConfig) (defaultWindow int, perModel map[string]int) {
	perModel = map[string]int{}
	if config.Options == nil {
		return 0, perModel
	}
	if v, ok := config.Options["context_window"]; ok {
		defaultWindow = toInt(v)
	}
	if v, ok := config.Options["context_windows"]; ok {
		if m, ok := v.(map[string]any); ok {
			for model, raw := range m {
				if w := toInt(raw); w > 0 {
					perModel[strings.ToLower(strings.TrimSpace(model))] = w
				}
			}
		}
	}
	return defaultWindow, perModel
}

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
		SupportsTools:            true,
		SupportsStreaming:        true,
		SupportsSystemPrompt:     true,
		SupportsTemperature:      true,
		SupportsStructuredOutput: true, // Ollama "format" / OpenAI-compatible "response_format"
		RequiresAPIKey:           false,
		SupportsCustomEndpoint:   true,
		MaxContextWindow:         maxContextWindow,
		SupportedFormats:         []string{"text"},
	}
}
