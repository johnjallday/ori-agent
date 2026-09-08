package agentmap

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Errors the service returns when a layout write points at something the
// current user cannot place on their map.
var (
	// ErrAgentNotFound means the name resolves to no agent — it was never
	// created, or it has been deleted.
	ErrAgentNotFound = errors.New("agent map agent not found")
	// ErrServiceUnavailable means the map service has no agent lookup or no
	// layout storage wired.
	ErrServiceUnavailable = errors.New("agent map service is not configured")
)

// AgentLookup resolves the agents a layout operation refers to.
//
// It is read-only by construction, and that is the point. The Map service holds
// no way to save an agent, so no map operation — a drag, a reset, an undo — has
// any path to change a role, a model, a prompt, a skill binding, or a workspace
// membership. The only agent question this package can ask is "does this agent
// exist?".
type AgentLookup interface {
	Exists(name string) bool
}

// LayoutStore is the persistence seam behind the service, narrow enough for
// tests to substitute.
type LayoutStore interface {
	Load(ctx context.Context, userID string) (Layout, error)
	Apply(ctx context.Context, userID string, patch Patch) (Result, error)
}

// Service applies existence rules above layout storage.
//
// Its whole job is the gap between "this JSON parsed" and "this user may anchor
// that agent": the store validates geometry, and the service validates what.
type Service struct {
	store  LayoutStore
	lookup AgentLookup
}

// NewService builds the map service over layout storage and a read-only agent
// lookup.
func NewService(store LayoutStore, lookup AgentLookup) *Service {
	return &Service{store: store, lookup: lookup}
}

// Load returns the current user's layout.
func (s *Service) Load(ctx context.Context, userID string) (Layout, error) {
	if s == nil || s.store == nil {
		return Layout{}, ErrServiceUnavailable
	}
	return s.store.Load(ctx, userID)
}

// Apply validates every agent a patch refers to, then commits it.
//
// Unknown names are refused before storage is touched, so a rejected reference
// never consumes a revision or leaves half a layout moved.
func (s *Service) Apply(ctx context.Context, userID string, patch Patch) (Result, error) {
	if s == nil || s.store == nil {
		return Result{}, ErrServiceUnavailable
	}
	normalized, err := NormalizePatch(patch)
	if err != nil {
		return Result{}, err
	}
	if err := s.authorizeAgents(normalized); err != nil {
		return Result{}, err
	}
	return s.store.Apply(ctx, userID, normalized)
}

// Reset clears the user's custom anchors so deterministic automatic placement
// takes over. It refers to no agent record and changes none: it is purely the
// removal of this user's own map coordinates (FR-72).
func (s *Service) Reset(ctx context.Context, userID string) (Result, error) {
	if s == nil || s.store == nil {
		return Result{}, ErrServiceUnavailable
	}
	return s.store.Apply(ctx, userID, Patch{Operations: []Operation{Reset()}})
}

// authorizeAgents checks every agent name a patch mentions against the lookup.
// Reset carries no references and passes trivially.
func (s *Service) authorizeAgents(patch Patch) error {
	checked := map[string]bool{}
	for _, op := range patch.Operations {
		for name := range op.Positions {
			if checked[name] {
				continue
			}
			if s.lookup == nil {
				return ErrServiceUnavailable
			}
			if !s.lookup.Exists(name) {
				return fmt.Errorf("%w: %q", ErrAgentNotFound, name)
			}
			checked[name] = true
		}
	}
	return nil
}

// AgentListerLookup adapts a name lister into the existence check the service
// needs, so a caller with a "list all agents" function does not have to write
// its own case-insensitive membership test.
type AgentListerLookup struct {
	List func() []string
}

// Exists reports whether the named agent is in the current list.
//
// The comparison is case-insensitive, matching how the agent API resolves a
// name elsewhere: a stored anchor for "atlas" must still find "Atlas".
func (l AgentListerLookup) Exists(name string) bool {
	if l.List == nil {
		return false
	}
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return false
	}
	for _, candidate := range l.List() {
		if strings.ToLower(strings.TrimSpace(candidate)) == want {
			return true
		}
	}
	return false
}
