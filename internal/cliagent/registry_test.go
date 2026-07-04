package cliagent

import (
	"context"
	"testing"
)

// stubAdapter is a minimal CLIAgentAdapter for testing the registry.
type stubAdapter struct {
	backend   string
	available bool
	models    []string
}

func (s *stubAdapter) Backend() string            { return s.backend }
func (s *stubAdapter) IsAvailable() bool          { return s.available }
func (s *stubAdapter) AvailableModels() []string  { return s.models }
func (s *stubAdapter) Capabilities() Capabilities { return Capabilities{} }
func (s *stubAdapter) ExecuteStep(_ context.Context, _ StepRequest) (*StepResult, error) {
	return nil, nil
}

func TestRegistry_GetAndList(t *testing.T) {
	claude := &stubAdapter{backend: BackendClaude, available: true, models: []string{"opus"}}
	codex := &stubAdapter{backend: BackendCodex, available: true, models: []string{"gpt-5.3-codex"}}

	r := NewRegistry(claude, codex)

	// Get existing
	got, err := r.Get(BackendClaude)
	if err != nil {
		t.Fatalf("Get claude: %v", err)
	}
	if got.Backend() != BackendClaude {
		t.Errorf("expected claude, got %s", got.Backend())
	}

	// Get missing
	_, err = r.Get("gemini")
	if err == nil {
		t.Error("expected error for missing backend")
	}

	// List
	infos := r.List()
	if len(infos) != 2 {
		t.Errorf("expected 2 backends, got %d", len(infos))
	}
}

func TestRegistry_IsAvailable(t *testing.T) {
	available := &stubAdapter{backend: BackendClaude, available: true}
	unavailable := &stubAdapter{backend: BackendCodex, available: false}

	r := NewRegistry(available, unavailable)

	if !r.IsAvailable(BackendClaude) {
		t.Error("claude should be available")
	}
	if r.IsAvailable(BackendCodex) {
		t.Error("codex should not be available")
	}
	if r.IsAvailable("missing") {
		t.Error("missing backend should not be available")
	}
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()

	if len(r.List()) != 0 {
		t.Fatal("empty registry should have no backends")
	}

	r.Register(&stubAdapter{backend: BackendClaude, available: true})
	if len(r.List()) != 1 {
		t.Error("expected 1 backend after register")
	}

	// Replace
	r.Register(&stubAdapter{backend: BackendClaude, available: false})
	if r.IsAvailable(BackendClaude) {
		t.Error("replaced adapter should be unavailable")
	}
}

func TestRegistry_Empty(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get(BackendClaude); err == nil {
		t.Error("expected error from empty registry")
	}
	if len(r.List()) != 0 {
		t.Error("expected empty list")
	}
}
