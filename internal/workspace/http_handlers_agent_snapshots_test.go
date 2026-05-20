package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
)

func TestHTTPHandler_ListAgentSnapshots(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:     "ws-snap",
		Name:   "Snap",
		Status: StatusActive,
		Agents: []string{"Manager", "Helper"},
		SharedData: map[string]any{
			"entry_agent_name": "Manager",
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.SaveWorkspaceAgent(ws.ID, "Manager", &agent.Agent{Type: agent.TypeToolCalling}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-snap/agent-snapshots", nil)
	rec := httptest.NewRecorder()
	handler.ListAgentSnapshots(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		WorkspaceID string   `json:"workspace_id"`
		Agents      []string `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WorkspaceID != "ws-snap" {
		t.Fatalf("workspace_id=%q", resp.WorkspaceID)
	}
	if len(resp.Agents) != 1 || resp.Agents[0] != "Manager" {
		t.Fatalf("agents=%v, want [Manager]", resp.Agents)
	}
}
