package cliagent

import (
	"fmt"
	"os/exec"
	"sync"
)

// CLIAgentRegistry manages available CLI agent backends.
type CLIAgentRegistry struct {
	adapters map[string]Adapter
	mu       sync.RWMutex
}

// NewRegistry creates a CLIAgentRegistry and auto-detects installed CLIs.
// Pass nil for adapters to use default auto-detection; pass explicit adapters
// for testing.
func NewRegistry(adapters ...Adapter) *CLIAgentRegistry {
	r := &CLIAgentRegistry{
		adapters: make(map[string]Adapter),
	}
	for _, a := range adapters {
		r.adapters[a.Backend()] = a
	}
	return r
}

// AutoDetect probes the system for installed CLIs and registers their adapters.
func (r *CLIAgentRegistry) AutoDetect() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, err := exec.LookPath("claude"); err == nil {
		r.adapters[BackendClaude] = newClaudeAdapterFromPath("claude")
	}
	if _, err := exec.LookPath("codex"); err == nil {
		r.adapters[BackendCodex] = newCodexAdapterFromPath("codex")
	}
	if _, err := exec.LookPath("gemini"); err == nil {
		r.adapters[BackendGemini] = newGeminiAdapterFromPath("gemini")
	}
}

// Register adds or replaces an adapter in the registry.
func (r *CLIAgentRegistry) Register(adapter Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[adapter.Backend()] = adapter
}

// Get returns the adapter for the given backend name.
func (r *CLIAgentRegistry) Get(backend string) (Adapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	a, ok := r.adapters[backend]
	if !ok {
		return nil, fmt.Errorf("cli agent backend %q not registered", backend)
	}
	return a, nil
}

// IsAvailable checks whether a specific backend is registered and ready.
func (r *CLIAgentRegistry) IsAvailable(backend string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	a, ok := r.adapters[backend]
	if !ok {
		return false
	}
	return a.IsAvailable()
}

// List returns information about all registered CLI agent backends.
func (r *CLIAgentRegistry) List() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()

	infos := make([]Info, 0, len(r.adapters))
	for _, a := range r.adapters {
		infos = append(infos, Info{
			Backend:      a.Backend(),
			Available:    a.IsAvailable(),
			Models:       a.AvailableModels(),
			Capabilities: a.Capabilities(),
		})
	}
	return infos
}

// newClaudeAdapterFromPath creates a ClaudeCLIAdapter for auto-detection.
// The invoker must be set or the adapter can list models but never execute
// a step (ExecuteStep rejects a nil invoker).
func newClaudeAdapterFromPath(cliPath string) *ClaudeCLIAdapter {
	resolved, err := exec.LookPath(cliPath)
	if err != nil {
		resolved = cliPath
	}
	return &ClaudeCLIAdapter{cliPath: resolved, invoker: NewCLIInvoker()}
}

// newCodexAdapterFromPath creates a CodexCLIAdapter for auto-detection.
func newCodexAdapterFromPath(cliPath string) *CodexCLIAdapter {
	resolved, err := exec.LookPath(cliPath)
	if err != nil {
		resolved = cliPath
	}
	return &CodexCLIAdapter{cliPath: resolved, invoker: NewCLIInvoker()}
}

// newGeminiAdapterFromPath creates a GeminiCLIAdapter for auto-detection.
func newGeminiAdapterFromPath(cliPath string) *GeminiCLIAdapter {
	resolved, err := exec.LookPath(cliPath)
	if err != nil {
		resolved = cliPath
	}
	return &GeminiCLIAdapter{cliPath: resolved, invoker: NewCLIInvoker()}
}
