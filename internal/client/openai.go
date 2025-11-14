package client

import (
	"net/http"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/johnjallday/ori-agent/internal/agent"
)

// Factory provides methods for creating OpenAI clients
type Factory struct {
	defaultClient openai.Client
	httpClient    *http.Client
}

// NewFactory creates a new client factory with a default client
func NewFactory(apiKey string) *Factory {
	// HTTP client with sane timeouts for OpenAI calls
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	var defaultClient openai.Client
	if apiKey != "" {
		defaultClient = openai.NewClient(
			option.WithAPIKey(apiKey),
			option.WithHTTPClient(httpClient),
		)
	}

	return &Factory{
		defaultClient: defaultClient,
		httpClient:    httpClient,
	}
}

// GetDefault returns the default OpenAI client
func (f *Factory) GetDefault() openai.Client {
	return f.defaultClient
}

// GetForAgent returns an OpenAI client using the agent's API key if provided,
// otherwise returns the default client
func (f *Factory) GetForAgent(ag *agent.Agent) openai.Client {
	if ag.Settings.APIKey != "" {
		return openai.NewClient(
			option.WithAPIKey(ag.Settings.APIKey),
			option.WithHTTPClient(f.httpClient),
		)
	}
	return f.defaultClient
}

// GetForAPIKey creates a new client with the specified API key
func (f *Factory) GetForAPIKey(apiKey string) openai.Client {
	return openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(f.httpClient),
	)
}

// UpdateDefaultClient updates the default client with a new API key
func (f *Factory) UpdateDefaultClient(apiKey string) {
	if apiKey != "" {
		f.defaultClient = openai.NewClient(
			option.WithAPIKey(apiKey),
			option.WithHTTPClient(f.httpClient),
		)
	}
}
