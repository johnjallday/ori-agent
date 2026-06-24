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

// agentListEntry mirrors the inline AgentInfo struct returned by ServeHTTP so
// tests can decode the response without exporting the internal type.
type agentListEntry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Source      string `json:"source"`
	Scope       string `json:"scope,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Status      string `json:"status,omitempty"`
}

// TestListAgents_AnnotatesWorkspaceEntryAgents verifies that GET /api/agents
// returns scope="workspace" and the workspace_id for agents that are
// designated entry agents — clients such as the sidebar rely on this to
// hide workspace-scoped agents from global pickers.
func TestListAgents_AnnotatesWorkspaceEntryAgents(t *testing.T) {
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
	if regular.Scope != "" {
		t.Errorf("expected Regular Agent to have empty scope, got %q", regular.Scope)
	}
	if regular.WorkspaceID != "" {
		t.Errorf("expected Regular Agent to have empty workspace_id, got %q", regular.WorkspaceID)
	}
	if regular.Status != string(types.AgentStatusActive) {
		t.Errorf("expected Regular Agent status=%q, got %q", types.AgentStatusActive, regular.Status)
	}

	entry, ok := byName["Workspace Manager"]
	if !ok {
		t.Fatalf("expected 'Workspace Manager' in response, got %+v", body.Agents)
	}
	if entry.Scope != "workspace" {
		t.Errorf("expected Workspace Manager scope='workspace', got %q", entry.Scope)
	}
	if entry.WorkspaceID != "ws-agents-1" {
		t.Errorf("expected Workspace Manager workspace_id='ws-agents-1', got %q", entry.WorkspaceID)
	}
}

// TestListAgents_NoWorkspaceStore_NoScope verifies that when the workspace
// store isn't wired, agents are returned without any scope annotation
// (backwards-compatible behavior).
func TestListAgents_NoWorkspaceStore_NoScope(t *testing.T) {
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
	if body.Agents[0].Scope != "" {
		t.Errorf("expected empty scope when workspace store is unwired, got %q", body.Agents[0].Scope)
	}
}
