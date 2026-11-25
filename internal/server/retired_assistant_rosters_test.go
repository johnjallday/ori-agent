package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// retiredAssistantFixture is a data-dir store holding the retired assistant's
// marked record (as an existing profile does) plus an ordinary agent.
func retiredAssistantFixture(t *testing.T) store.Store {
	t.Helper()
	dir := t.TempDir()
	marked := filepath.Join(dir, "agents", "Ask Ori")
	if err := os.MkdirAll(marked, 0o750); err != nil {
		t.Fatal(err)
	}
	record := `{"role":"orchestrator","Settings":{"model":"sonnet"},"metadata":{"tags":["` + systemassistant.ProtectedMarker + `"]}}`
	if err := os.WriteFile(filepath.Join(marked, "agent_settings.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFileStore(filepath.Join(dir, "agents.json"), types.Settings{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAgent("Scout", &store.CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestDetachRetiredAssistantFromRosters(t *testing.T) {
	st := retiredAssistantFixture(t)
	workspaces := workspace.NewInMemoryStore()

	led := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Old Desk", Agents: []string{"Ask Ori", "Scout"}})
	if err := led.SetEntryAgentName("Ask Ori"); err != nil {
		t.Fatal(err)
	}
	only := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Old Blank", Agents: []string{"Ask Ori"}})
	if err := only.SetEntryAgentName("Ask Ori"); err != nil {
		t.Fatal(err)
	}
	untouched := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Garage", Agents: []string{"Scout"}})
	if err := untouched.SetEntryAgentName("Scout"); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []*workspace.Workspace{led, only, untouched} {
		if err := workspaces.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	untouchedVersion := mustGetWorkspace(t, workspaces, untouched.ID).Version

	if got := detachRetiredAssistantFromRosters(st, workspaces); got != 2 {
		t.Fatalf("detached from %d workspaces, want 2", got)
	}

	after := mustGetWorkspace(t, workspaces, led.ID)
	if len(after.AgentInstances) != 1 || after.AgentInstances[0].Name != "Scout" || after.EntryAgentName() != "Scout" {
		t.Fatalf("Old Desk after detach: entry=%q instances=%+v", after.EntryAgentName(), after.AgentInstances)
	}
	blank := mustGetWorkspace(t, workspaces, only.ID)
	if len(blank.AgentInstances) != 0 || blank.EntryAgentName() != "" {
		t.Fatalf("Old Blank should be honestly unstaffed: entry=%q instances=%+v", blank.EntryAgentName(), blank.AgentInstances)
	}
	if mustGetWorkspace(t, workspaces, untouched.ID).Version != untouchedVersion {
		t.Fatal("a workspace that never named the retired assistant was rewritten")
	}

	// Idempotent: a second start finds nothing to do.
	if got := detachRetiredAssistantFromRosters(st, workspaces); got != 0 {
		t.Fatalf("second pass detached %d, want 0", got)
	}
}

// Once a person owns an agent with the retired assistant's name, the name is
// theirs: references to it are never detached.
func TestDetachRetiredAssistantLeavesAUsersSameNameAgentAlone(t *testing.T) {
	st := lookupFileStoreWithUserAskOri(t)
	workspaces := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Front Desk", Agents: []string{"Ask Ori"}})
	if err := ws.SetEntryAgentName("Ask Ori"); err != nil {
		t.Fatal(err)
	}
	if err := workspaces.Save(ws); err != nil {
		t.Fatal(err)
	}
	if got := detachRetiredAssistantFromRosters(st, workspaces); got != 0 {
		t.Fatalf("detached a user's own Ask Ori from %d workspaces", got)
	}
	if after := mustGetWorkspace(t, workspaces, ws.ID); after.EntryAgentName() != "Ask Ori" {
		t.Fatalf("entry = %q, want the user's Ask Ori kept", after.EntryAgentName())
	}
}

func lookupFileStoreWithUserAskOri(t *testing.T) store.Store {
	t.Helper()
	st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAgent("Ask Ori", &store.CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	return st
}

func mustGetWorkspace(t *testing.T, st workspace.Store, id string) *workspace.Workspace {
	t.Helper()
	ws, err := st.Get(id)
	if err != nil || ws == nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	return ws
}
