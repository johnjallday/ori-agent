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

// NewFactory creates a new provider factory
func NewFactory() *Factory {
	return &Factory{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry
func (f *Factory) Register(name string, provider Provider) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers[strings.ToLower(name)] = provider
}

// Unregister removes a provider from the registry
func (f *Factory) Unregister(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.providers, strings.ToLower(name))
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

// HasProvider checks if a provider is registered
func (f *Factory) HasProvider(name string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.providers[strings.ToLower(name)]
	return ok
}

// ListProviders returns information about all registered providers
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

// ProviderInfo contains metadata about a registered provider
type ProviderInfo struct {
	// Name of the provider
	Name string

	// Type of provider (cloud, local, hybrid)
	Type ProviderType

	// Capabilities of the provider
	Capabilities ProviderCapabilities

	// Models available from this provider
	Models []string
}

// ProviderCount returns the number of registered providers
func (f *Factory) ProviderCount() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.providers)
}

// Clear removes all providers from the registry
func (f *Factory) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers = make(map[string]Provider)
}
