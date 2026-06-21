package workspace

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

func nativeMCPTestMux(handler *HTTPHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/native-mcp", handler.GetNativeMCPSettings)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/native-mcp", handler.UpdateNativeMCPWorkspace)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/agents/{name}/native-mcp", handler.UpdateNativeMCPAgent)
	return mux
}

func TestNativeMCPHTTP_ToggleAndRead(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)
	mux := nativeMCPTestMux(handler)

	ws := &Workspace{
		ID:             "ws-nm",
		Name:           "NativeMCP",
		Status:         StatusActive,
		AgentInstances: []AgentInstance{{ID: "i1", Name: "reaper", NodeID: "reaper-node-1"}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.SaveWorkspaceAgent(ws.ID, "reaper", &agent.Agent{
		Type:     "orchestration",
		Settings: types.Settings{Model: "gpt-5.5", Provider: "codex"},
	}); err != nil {
		t.Fatalf("save agent: %v", err)
	}

	patch := func(path string, enabled bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]bool{"enabled": enabled})
		req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// Enable workspace-level opt-in.
	if rec := patch("/api/workspaces/ws-nm/native-mcp", true); rec.Code != http.StatusOK {
		t.Fatalf("workspace patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	reloaded, _ := store.Get("ws-nm")
	if !reloaded.AllowNativeMCPCLI {
		t.Fatal("workspace flag not persisted")
	}

	// Enable agent-level opt-in.
	if rec := patch("/api/workspaces/ws-nm/agents/reaper/native-mcp", true); rec.Code != http.StatusOK {
		t.Fatalf("agent patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	ag, ok, _ := store.GetWorkspaceAgent("ws-nm", "reaper")
	if !ok || !ag.Settings.IsNativeMCPToolsAllowed() {
		t.Fatal("agent flag not persisted")
	}

	// GET reflects both, and flags the CLI provider.
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-nm/native-mcp", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp nativeMCPSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.WorkspaceEnabled {
		t.Error("workspace_enabled should be true")
	}
	if len(resp.Agents) != 1 {
		t.Fatalf("agents=%+v, want 1", resp.Agents)
	}
	a := resp.Agents[0]
	if a.Name != "reaper" || a.Provider != "codex" || !a.IsCLIProvider || !a.Enabled {
		t.Errorf("agent status wrong: %+v", a)
	}

	// Disabling the agent flips it back off.
	if rec := patch("/api/workspaces/ws-nm/agents/reaper/native-mcp", false); rec.Code != http.StatusOK {
		t.Fatalf("agent disable status=%d", rec.Code)
	}
	ag, _, _ = store.GetWorkspaceAgent("ws-nm", "reaper")
	if ag.Settings.IsNativeMCPToolsAllowed() {
		t.Error("agent flag should be disabled")
	}
}

func TestNativeMCPHTTP_UnknownWorkspace(t *testing.T) {
	handler := NewHTTPHandler(NewInMemoryStore(), nil, nil)
	mux := nativeMCPTestMux(handler)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/nope/native-mcp", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}
