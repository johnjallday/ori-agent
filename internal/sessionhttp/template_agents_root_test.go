package sessionhttp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Creation paths are unchanged by moving agents into the workspace root: a
// template still seeds the user's own agents (now in <root>/Agents), and
// attaching one to a workspace still writes that workspace's own copy.
func TestSeededTemplateAgentsAreRootAgentsAndAttachAsWorkspaceCopies(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	root := t.TempDir()
	dataDir := t.TempDir()
	system, err := agentstore.NewFileStore(filepath.Join(dataDir, "agents.json"), types.Settings{})
	if err != nil {
		t.Fatalf("system store: %v", err)
	}
	roster, err := agentstore.NewCompositeStore(system, root, filepath.Join(dataDir, "agent_state"), types.Settings{})
	if err != nil {
		t.Fatalf("composite: %v", err)
	}
	handler.SetAgentStore(roster)

	ws := &session.Workspace{ID: "ws-seeded", Name: "Campaign"}
	res := handler.seedTemplateAgents(ws, rosterTemplate(
		projecttemplates.AgentSpec{Name: "Lead", Role: "orchestrator", SystemPrompt: "run it"},
		projecttemplates.AgentSpec{Name: "Writer"},
	))
	if !res.EntrySet || len(res.Warnings) != 0 {
		t.Fatalf("seed result = %+v", res)
	}
	for _, name := range []string{"Lead", "Writer"} {
		if _, err := os.Stat(filepath.Join(root, "Agents", name, "agent_settings.json")); err != nil {
			t.Errorf("%s was not created as a root agent: %v", name, err)
		}
	}

	folders, err := agentworkspace.NewFileStore(root)
	if err != nil {
		t.Fatalf("workspace store: %v", err)
	}
	defer func() { _ = folders.Close() }()
	attached := &agentworkspace.Workspace{ID: ws.ID, Name: ws.Name, AgentInstances: agentworkspace.AgentInstancesFromNames("Lead")}
	if err := agentworkspace.NewAgentSnapshotStore(folders, roster).Save(attached); err != nil {
		t.Fatalf("attach: %v", err)
	}
	copyOfLead, ok, err := folders.GetWorkspaceAgent(ws.ID, "Lead")
	if err != nil || !ok || copyOfLead.Settings.SystemPrompt != "run it" {
		t.Fatalf("attaching did not write the workspace's copy: ok=%v err=%v %+v", ok, err, copyOfLead)
	}
}
