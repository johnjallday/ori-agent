package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// workspaceRefEntry mirrors workspace.WorkspaceRef for JSON decoding in tests.
type workspaceRefEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	EntryPoint bool   `json:"entry_point"`
}

// agentListEntry mirrors the inline AgentInfo struct returned by ServeHTTP so
// tests can decode the response without exporting the internal type.
type agentListEntry struct {
	Name           string              `json:"name"`
	Type           string              `json:"type"`
	Source         string              `json:"source"`
	Scope          string              `json:"scope,omitempty"`
	WorkspaceID    string              `json:"workspace_id,omitempty"`
	WorkspaceCount int                 `json:"workspace_count"`
	Workspaces     []workspaceRefEntry `json:"workspaces,omitempty"`
	Status         string              `json:"status,omitempty"`
}

// TestListAgents_AnnotatesWorkspaceMembership verifies that GET /api/agents
// annotates every referenced definition — entry agent AND specialists — with
// workspace_count and the attached workspaces, and that the entry agent's
// workspace ref carries entry_point=true. The Agents page uses this to group
// definitions by their attached workspaces (PRD FR1/FR2).
func TestListAgents_AnnotatesWorkspaceMembership(t *testing.T) {
	tmpDir := t.TempDir()

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
	if err := st.CreateAgent("Specialist", &store.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("CreateAgent specialist failed: %v", err)
	}

	wsPath := filepath.Join(tmpDir, "workspaces")
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatalf("mkdir workspaces failed: %v", err)
	}
	wsStore, err := workspace.NewFileStore(wsPath)
	if err != nil {
		t.Fatalf("NewFileStore workspace failed: %v", err)
	}

	ws := &workspace.Workspace{
		ID:   "ws-agents-1",
		Name: "Test Workspace",
		AgentInstances: []workspace.AgentInstance{
			{ID: "inst-1", Name: "Workspace Manager", EntryPoint: true},
			{ID: "inst-2", Name: "Specialist"},
		},
		SharedData: map[string]any{
			"entry_agent_name": "Workspace Manager",
		},
	}
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("Save workspace failed: %v", err)
	}

	h := New(st)
	h.SetWorkspaceStore(wsStore)

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		Agents []agentListEntry `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response failed: %v body=%s", err, rr.Body.String())
	}

	byName := make(map[string]agentListEntry, len(body.Agents))
	for _, ag := range body.Agents {
		byName[ag.Name] = ag
	}

	regular, ok := byName["Regular Agent"]
	if !ok {
		t.Fatalf("expected 'Regular Agent' in response, got %+v", body.Agents)
	}
	if regular.WorkspaceCount != 0 {
		t.Errorf("expected Regular Agent workspace_count=0, got %d", regular.WorkspaceCount)
	}
	if len(regular.Workspaces) != 0 {
		t.Errorf("expected Regular Agent to have no workspaces, got %+v", regular.Workspaces)
	}
	if regular.Status != string(types.AgentStatusActive) {
		t.Errorf("expected Regular Agent status=%q, got %q", types.AgentStatusActive, regular.Status)
	}

	entry, ok := byName["Workspace Manager"]
	if !ok {
		t.Fatalf("expected 'Workspace Manager' in response, got %+v", body.Agents)
	}
	if entry.WorkspaceCount != 1 || len(entry.Workspaces) != 1 {
		t.Fatalf("expected Workspace Manager attached to 1 workspace, got count=%d refs=%+v", entry.WorkspaceCount, entry.Workspaces)
	}
	if entry.Workspaces[0].ID != "ws-agents-1" || !entry.Workspaces[0].EntryPoint {
		t.Errorf("expected Workspace Manager ref {ws-agents-1, entry_point:true}, got %+v", entry.Workspaces[0])
	}
	if entry.WorkspaceID != "ws-agents-1" {
		t.Errorf("expected Workspace Manager workspace_id='ws-agents-1', got %q", entry.WorkspaceID)
	}

	// The specialist is a non-entry roster member — it must ALSO be annotated
	// with membership (this is the behavior change: specialists are no longer
	// loose, unscoped agents), but its ref is not an entry point.
	spec, ok := byName["Specialist"]
	if !ok {
		t.Fatalf("expected 'Specialist' in response, got %+v", body.Agents)
	}
	if spec.WorkspaceCount != 1 || len(spec.Workspaces) != 1 {
		t.Fatalf("expected Specialist attached to 1 workspace, got count=%d refs=%+v", spec.WorkspaceCount, spec.Workspaces)
	}
	if spec.Workspaces[0].ID != "ws-agents-1" || spec.Workspaces[0].EntryPoint {
		t.Errorf("expected Specialist ref {ws-agents-1, entry_point:false}, got %+v", spec.Workspaces[0])
	}
	if spec.WorkspaceID != "" {
		t.Errorf("expected Specialist to have empty workspace_id (not an entry agent), got %q", spec.WorkspaceID)
	}
}

// TestListAgents_NoWorkspaceStore_NoMembership verifies that when the workspace
// store isn't wired, agents are returned without any membership annotation.
func TestListAgents_NoWorkspaceStore_NoMembership(t *testing.T) {
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

	h := New(st)
	// Deliberately do not call SetWorkspaceStore.

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		Agents []agentListEntry `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if len(body.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(body.Agents))
	}
	if body.Agents[0].WorkspaceCount != 0 || len(body.Agents[0].Workspaces) != 0 {
		t.Errorf("expected no membership when workspace store is unwired, got count=%d refs=%+v",
			body.Agents[0].WorkspaceCount, body.Agents[0].Workspaces)
	}
}
