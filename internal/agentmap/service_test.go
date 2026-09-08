package agentmap

import (
	"context"
	"errors"
	"testing"
)

// recordingStore captures what reached storage, so a test can assert that a
// refused patch never got there at all.
type recordingStore struct {
	layout  Layout
	applied []Patch
	err     error
}

func (s *recordingStore) Load(context.Context, string) (Layout, error) {
	return s.layout, s.err
}

func (s *recordingStore) Apply(_ context.Context, _ string, patch Patch) (Result, error) {
	s.applied = append(s.applied, patch)
	if s.err != nil {
		return Result{}, s.err
	}
	return Result{Layout: s.layout}, nil
}

type stubLookup struct{ known map[string]bool }

func (l stubLookup) Exists(name string) bool { return l.known[name] }

func TestServiceRefusesAnAnchorForAnAgentThatDoesNotExist(t *testing.T) {
	store := &recordingStore{layout: NewLayout()}
	service := NewService(store, stubLookup{known: map[string]bool{"Atlas": true}})

	_, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}, "Ghost": {X: 2, Y: 2}}),
	}})
	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("error = %v, want ErrAgentNotFound", err)
	}
	// Refused before storage was touched, so no revision was consumed and no
	// part of the patch landed.
	if len(store.applied) != 0 {
		t.Fatalf("storage saw %d patches, want 0", len(store.applied))
	}
}

func TestServiceAppliesAPatchForKnownAgents(t *testing.T) {
	store := &recordingStore{layout: NewLayout()}
	service := NewService(store, stubLookup{known: map[string]bool{"Atlas": true}})

	if _, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{" Atlas ": {X: 1, Y: 1}}),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(store.applied) != 1 {
		t.Fatalf("storage saw %d patches, want 1", len(store.applied))
	}
	// Storage receives the normalized patch, not the raw one.
	if _, ok := store.applied[0].Operations[0].Positions["Atlas"]; !ok {
		t.Fatalf("stored positions = %v, want the trimmed name", store.applied[0].Operations[0].Positions)
	}
}

// Reset names no agent, so it needs no lookup and cannot be refused by one.
func TestResetNeedsNoAgentLookup(t *testing.T) {
	store := &recordingStore{layout: NewLayout()}
	service := NewService(store, stubLookup{known: map[string]bool{}})

	if _, err := service.Reset(context.Background(), "local"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if len(store.applied) != 1 || store.applied[0].Operations[0].Kind != OpReset {
		t.Fatalf("applied = %+v, want a single reset", store.applied)
	}
}

func TestServiceWithoutStorageIsUnavailableRatherThanPanicking(t *testing.T) {
	service := NewService(nil, stubLookup{})
	if _, err := service.Load(context.Background(), "local"); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("Load error = %v, want ErrServiceUnavailable", err)
	}
	if _, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{Reset()}}); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("Apply error = %v, want ErrServiceUnavailable", err)
	}
	if _, err := service.Reset(context.Background(), "local"); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("Reset error = %v, want ErrServiceUnavailable", err)
	}
}

// Validation happens before the lookup, so a malformed patch is reported as
// malformed rather than as a missing agent.
func TestServiceValidatesBeforeItLooksAgentsUp(t *testing.T) {
	store := &recordingStore{layout: NewLayout()}
	service := NewService(store, stubLookup{known: map[string]bool{}})

	_, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: MaxCoordinate * 4, Y: 0}}),
	}})
	if !errors.Is(err, ErrInvalidCoordinate) {
		t.Fatalf("error = %v, want ErrInvalidCoordinate", err)
	}
}

func TestAgentListerLookupMatchesCaseInsensitively(t *testing.T) {
	lookup := AgentListerLookup{List: func() []string { return []string{"Atlas", " Beacon "} }}

	for _, name := range []string{"Atlas", "atlas", "ATLAS", "Beacon", "beacon"} {
		if !lookup.Exists(name) {
			t.Fatalf("Exists(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "   ", "Ghost"} {
		if lookup.Exists(name) {
			t.Fatalf("Exists(%q) = true, want false", name)
		}
	}

	// A nil list function reports nothing exists rather than panicking.
	if (AgentListerLookup{}).Exists("Atlas") {
		t.Fatal("an unwired lookup should report that nothing exists")
	}
}
