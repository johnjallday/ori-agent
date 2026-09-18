package workspace

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

type agentSourceFixture struct {
	folders   *FileStore
	allowlist *Allowlist
}

func newAgentSourceFixture(t *testing.T) agentSourceFixture {
	t.Helper()
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	return agentSourceFixture{folders: folders, allowlist: NewAllowlist(filepath.Join(t.TempDir(), "allowlist.json"))}
}

// addWorkspace saves a workspace that references agentName and holds its own
// copy of it, the way attaching an agent leaves a workspace.
func (f agentSourceFixture) addWorkspace(t *testing.T, id, name, agentName, prompt string, trusted bool) {
	t.Helper()
	ws := &Workspace{ID: id, Name: name, AgentInstances: []AgentInstance{{ID: id + "-1", Name: agentName, InstanceNumber: 1}}}
	if err := f.folders.Save(ws); err != nil {
		t.Fatalf("save %s: %v", name, err)
	}
	copyOfAgent := &agent.Agent{Settings: types.Settings{Model: "gpt-4o-mini", SystemPrompt: prompt}}
	if err := f.folders.SaveWorkspaceAgent(id, agentName, copyOfAgent); err != nil {
		t.Fatalf("save copy: %v", err)
	}
	if trusted {
		if err := f.allowlist.Add(id); err != nil {
			t.Fatalf("allowlist: %v", err)
		}
	}
}

func TestTrustedSourceReadsCopiesOfTrustedWorkspacesOnly(t *testing.T) {
	f := newAgentSourceFixture(t)
	f.addWorkspace(t, "ws-a", "Studio", "Scout", "studio scout", true)
	f.addWorkspace(t, "ws-b", "From a zip", "Stranger", "not trusted", false)

	entries := NewTrustedWorkspaceAgentSource(f.folders, f.allowlist).WorkspaceAgents()
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want only the trusted workspace's copy", entries)
	}
	got := entries[0]
	if got.WorkspaceID != "ws-a" || got.WorkspaceName != "Studio" || got.AgentName != "Scout" ||
		got.Agent == nil || got.Agent.Settings.SystemPrompt != "studio scout" {
		t.Errorf("entry = %+v", got)
	}
}

func TestTrustedSourceSkipsTrashedWorkspaces(t *testing.T) {
	f := newAgentSourceFixture(t)
	f.addWorkspace(t, "ws-a", "Old", "Scout", "old scout", true)
	if _, _, err := f.folders.Trash("ws-a"); err != nil {
		t.Skipf("trash is unavailable here: %v", err)
	}
	if entries := NewTrustedWorkspaceAgentSource(f.folders, f.allowlist).WorkspaceAgents(); len(entries) != 0 {
		t.Errorf("a trashed workspace contributed %+v", entries)
	}
}

func TestTrustedSourceWithoutAnAllowlistTrustsNothing(t *testing.T) {
	f := newAgentSourceFixture(t)
	f.addWorkspace(t, "ws-a", "Studio", "Scout", "studio scout", true)
	if entries := NewTrustedWorkspaceAgentSource(f.folders, nil).WorkspaceAgents(); len(entries) != 0 {
		t.Errorf("a nil allowlist contributed %+v", entries)
	}
}
