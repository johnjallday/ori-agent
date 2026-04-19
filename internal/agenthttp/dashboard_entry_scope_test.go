package agenthttp

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestDashboardListAgents_AnnotatesWorkspaceEntryAgents verifies that agents
// designated as a workspace entry agent are returned with scope="workspace"
// and a workspace_id, so the UI can choose to group or hide them.
func TestDashboardListAgents_AnnotatesWorkspaceEntryAgents(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agent store with two agents
	st, err := store.NewFileStore(filepath.Join(tmpDir, "agents_index.json"), types.Settings{
		Model:       "gpt-4o-mini",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	if err := st.CreateAgent("Regular Agent", &store.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("CreateAgent regular failed: %v", err)
	}
	if err := st.CreateAgent("Workspace Manager", &store.CreateAgentConfig{Type: "orchestration"}); err != nil {
		t.Fatalf("CreateAgent workspace manager failed: %v", err)
	}

	// Create workspace store with a workspace that designates "Workspace Manager" as entry
	wsPath := filepath.Join(tmpDir, "workspaces")
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatalf("mkdir workspaces failed: %v", err)
	}
	wsStore, err := workspace.NewFileStore(wsPath)
	if err != nil {
		t.Fatalf("NewFileStore workspace failed: %v", err)
	}

	ws := &workspace.Workspace{
		ID:   "ws-1",
		Name: "Test Workspace",
		AgentInstances: []workspace.AgentInstance{
			{ID: "inst-1", Name: "Workspace Manager", EntryPoint: true},
		},
		SharedData: map[string]interface{}{
			"entry_agent_name": "Workspace Manager",
		},
	}
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("Save workspace failed: %v", err)
	}

	// Create handler with both stores wired
	h := NewDashboardHandler(st)
	h.SetWorkspaceStore(wsStore)

	// Request the agents list
	req := httptest.NewRequest("GET", "/api/agents/dashboard/list", nil)
	rr := httptest.NewRecorder()
	h.ListAgentsWithStats(rr, req)

	if rr.Code != 200 {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	var body struct {
		Agents []AgentListItem `json:"agents"`
		Total  int             `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if len(body.Agents) != 2 {
		t.Fatalf("expected 2 agents to be returned, got %d", len(body.Agents))
	}

	byName := make(map[string]AgentListItem, len(body.Agents))
	for _, ag := range body.Agents {
		byName[ag.Name] = ag
	}

	regular, ok := byName["Regular Agent"]
	if !ok {
		t.Fatalf("expected 'Regular Agent' in response")
	}
	if regular.Scope != "" {
		t.Errorf("expected Regular Agent to have empty scope, got %q", regular.Scope)
	}

	entry, ok := byName["Workspace Manager"]
	if !ok {
		t.Fatalf("expected 'Workspace Manager' in response")
	}
	if entry.Scope != "workspace" {
		t.Errorf("expected Workspace Manager scope='workspace', got %q", entry.Scope)
	}
	if entry.WorkspaceID != "ws-1" {
		t.Errorf("expected Workspace Manager workspace_id='ws-1', got %q", entry.WorkspaceID)
	}
}

// TestDashboardListAgents_NoWorkspaceStore_ListsAllAgents verifies that when
// the workspace store isn't wired, the handler lists all agents (no filter).
func TestDashboardListAgents_NoWorkspaceStore_ListsAllAgents(t *testing.T) {
	tmpDir := t.TempDir()

	st, err := store.NewFileStore(filepath.Join(tmpDir, "agents_index.json"), types.Settings{
		Model:       "gpt-4o-mini",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	if err := st.CreateAgent("Alpha", &store.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}
	if err := st.CreateAgent("Beta Manager", &store.CreateAgentConfig{Type: "orchestration"}); err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	h := NewDashboardHandler(st)
	// Deliberately do not call SetWorkspaceStore

	req := httptest.NewRequest("GET", "/api/agents/dashboard/list", nil)
	rr := httptest.NewRecorder()
	h.ListAgentsWithStats(rr, req)

	var body struct {
		Agents []AgentListItem `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if len(body.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(body.Agents))
	}
}
