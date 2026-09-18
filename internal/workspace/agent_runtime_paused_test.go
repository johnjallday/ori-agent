package workspace

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// A pause is stored in the agent's definition ("paused": true) rather than in
// its runtime state, and the store turns it back into Status == disabled on
// load. The runtime resolver must keep refusing such an agent after a restart,
// without knowing anything about where the pause is stored.
func TestResolverRefusesAnAgentPausedBeforeARestart(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "agents.json")
	first, err := store.NewFileStore(index, types.Settings{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := first.CreateAgent("Scout", &store.CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	ag, _ := first.GetAgent("Scout")
	ag.Status = types.AgentStatusDisabled
	if err := first.SetAgent("Scout", ag); err != nil {
		t.Fatalf("pause: %v", err)
	}

	restarted, err := store.NewFileStore(index, types.Settings{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	resolver := NewAgentRuntimeResolver(restarted, nil, nil, nil)
	if _, err := resolver.ResolveAgentForWorkspace("Scout", "", ""); !errors.Is(err, ErrAgentPaused) {
		t.Fatalf("resolve paused agent: err = %v, want ErrAgentPaused", err)
	}
}
