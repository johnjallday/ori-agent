package runtimecapability

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Registry is the compiled allowlist of runtime adapters. It is populated by
// server wiring, never by a manifest or browser request.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Adapter)}
}

func (r *Registry) Register(adapter Adapter) error {
	if r == nil {
		return errors.New("runtime adapter registry is not initialized")
	}
	if adapter == nil {
		return errors.New("cannot register a nil runtime adapter")
	}
	id := workspace.NormalizeRuntimeAdapterID(adapter.ID())
	if id == "" {
		return errors.New("cannot register a runtime adapter with an invalid id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[id]; exists {
		return fmt.Errorf("runtime adapter %q is already registered", id)
	}
	r.adapters[id] = adapter
	return nil
}

// Replace swaps an already-reserved adapter ID for its compiled platform
// implementation. It cannot introduce a new key; authoring parity remains
// fixed by initial registration.
func (r *Registry) Replace(adapter Adapter) error {
	if r == nil || adapter == nil {
		return errors.New("runtime adapter registry or replacement is nil")
	}
	id := workspace.NormalizeRuntimeAdapterID(adapter.ID())
	if id == "" {
		return errors.New("cannot replace a runtime adapter with an invalid id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[id]; !exists {
		return fmt.Errorf("runtime adapter %q is not reserved", id)
	}
	r.adapters[id] = adapter
	return nil
}

func (r *Registry) Lookup(id string) (Adapter, bool) {
	if r == nil {
		return nil, false
	}
	id = workspace.NormalizeRuntimeAdapterID(id)
	if id == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[id]
	return adapter, ok
}

func (r *Registry) Unregister(id string) {
	if r == nil {
		return
	}
	id = workspace.NormalizeRuntimeAdapterID(id)
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.adapters, id)
}

func (r *Registry) UnregisterPlugin(pluginID string) {
	if r == nil {
		return
	}
	pluginID = workspace.NormalizeCapabilityID(pluginID)
	if pluginID == "" {
		return
	}
	prefix := "plugin:" + pluginID + ":"
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.adapters {
		if strings.HasPrefix(id, prefix) {
			delete(r.adapters, id)
		}
	}
}

func (r *Registry) IDs() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.adapters))
	for id := range r.adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// NewBuiltinRegistry returns the compiled runtime adapters shipped by this
// build. Domain providers are contributed by trusted installed plugins.
func NewBuiltinRegistry() (*Registry, error) {
	return NewRegistry(), nil
}
