package runtimecapability

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Registry is the compiled allowlist of runtime adapters. It is populated by
// server wiring, never by a manifest or browser request.
type Registry struct {
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
	id := workspace.NormalizeRuntimeIdentifier(adapter.ID())
	if id == "" {
		return errors.New("cannot register a runtime adapter with an invalid id")
	}
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
	id := workspace.NormalizeRuntimeIdentifier(adapter.ID())
	if id == "" {
		return errors.New("cannot replace a runtime adapter with an invalid id")
	}
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
	id = workspace.NormalizeRuntimeIdentifier(id)
	if id == "" {
		return nil, false
	}
	adapter, ok := r.adapters[id]
	return adapter, ok
}

func (r *Registry) IDs() []string {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.adapters))
	for id := range r.adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// unavailableAdapter reserves an authorable, compiled adapter ID before its
// platform implementation is wired. It fails closed with bounded safe output;
// a blueprint can never turn an absent implementation into ready behavior.
type unavailableAdapter struct{ id string }

func (a unavailableAdapter) ID() string { return a.id }
func (a unavailableAdapter) EvaluateDurable(context.Context, EvaluationRequest) (DurableResult, error) {
	return DurableResult{
		State:      DurableInProgress,
		ReasonCode: ReasonAdapterUnavailable,
		Summary:    "This runtime requirement is unavailable in this build.",
	}, nil
}

// NewBuiltinRegistry returns every runtime adapter ID this build accepts at
// authoring time. The unavailable reservation fails closed until server wiring
// replaces it with the compiled platform implementation; minimal/test builders
// that omit that wiring therefore remain honest rather than assuming readiness.
func NewBuiltinRegistry() (*Registry, error) {
	registry := NewRegistry()
	for _, adapter := range []Adapter{
		unavailableAdapter{id: "reaper_live_control"},
	} {
		if err := registry.Register(adapter); err != nil {
			return nil, err
		}
	}
	return registry, nil
}
