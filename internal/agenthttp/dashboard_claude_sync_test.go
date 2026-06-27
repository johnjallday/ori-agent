package agenthttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/cliagent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newClaudeDetailHandler(t *testing.T) *DashboardHandler {
	return newCLIDetailHandler(t, cliagent.BackendClaude, []string{"opus"})
}

func newCodexDetailHandler(t *testing.T) *DashboardHandler {
	return newCLIDetailHandler(t, cliagent.BackendCodex, []string{"gpt-5.2-codex"})
}

func newCLIDetailHandler(t *testing.T, backend string, models []string) *DashboardHandler {
	t.Helper()
	st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	registry := cliagent.NewRegistry(dashboardCLIStubAdapter{
		backend: backend,
		models:  models,
	})
	dashboard := NewDashboardHandler(st)
	dashboard.SetCLIAgentRegistry(registry)
	return dashboard
}

func TestGetAgentDetail_ClaudeSyncAttached(t *testing.T) {
	dashboard := newClaudeDetailHandler(t)
	dashboard.SetClaudeSyncProvider(func() any {
		return map[string]any{"model": "opus", "mcpServers": []any{}}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/agents/Claude%20Code/detail", nil)
	rr := httptest.NewRecorder()
	dashboard.GetAgentDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GetAgentDetail() status = %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		Provider   string         `json:"provider"`
		ClaudeSync map[string]any `json:"claude_sync"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if body.Provider != cliagent.BackendClaude {
		t.Errorf("provider = %q, want %q", body.Provider, cliagent.BackendClaude)
	}
	if body.ClaudeSync == nil {
		t.Fatal("expected claude_sync to be attached for the Claude Code agent")
	}
	if body.ClaudeSync["model"] != "opus" {
		t.Errorf("claude_sync.model = %v, want opus", body.ClaudeSync["model"])
	}
}

func TestGetAgentDetail_NoClaudeSyncWhenProviderReturnsNil(t *testing.T) {
	dashboard := newClaudeDetailHandler(t)
	// Provider present but disabled (returns nil) — mirrors the opt-out path.
	dashboard.SetClaudeSyncProvider(func() any { return nil })

	req := httptest.NewRequest(http.MethodGet, "/api/agents/Claude%20Code/detail", nil)
	rr := httptest.NewRecorder()
	dashboard.GetAgentDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GetAgentDetail() status = %d body=%s", rr.Code, rr.Body.String())
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("claude_sync")) {
		t.Errorf("claude_sync must be omitted when provider returns nil: %s", rr.Body.String())
	}
}

func TestGetAgentDetail_NoClaudeSyncWhenProviderUnset(t *testing.T) {
	dashboard := newClaudeDetailHandler(t)
	// No provider wired at all.

	req := httptest.NewRequest(http.MethodGet, "/api/agents/Claude%20Code/detail", nil)
	rr := httptest.NewRecorder()
	dashboard.GetAgentDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GetAgentDetail() status = %d body=%s", rr.Code, rr.Body.String())
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("claude_sync")) {
		t.Errorf("claude_sync must be omitted when no provider is set: %s", rr.Body.String())
	}
}

func TestGetAgentDetail_CodexSyncAttached(t *testing.T) {
	dashboard := newCodexDetailHandler(t)
	dashboard.SetCodexSyncProvider(func() any {
		return map[string]any{"config": map[string]any{"model": "gpt-5.2-codex"}, "mcpServers": []any{}}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/agents/Codex/detail", nil)
	rr := httptest.NewRecorder()
	dashboard.GetAgentDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GetAgentDetail() status = %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		Provider  string         `json:"provider"`
		CodexSync map[string]any `json:"codex_sync"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if body.Provider != cliagent.BackendCodex {
		t.Errorf("provider = %q, want %q", body.Provider, cliagent.BackendCodex)
	}
	if body.CodexSync == nil {
		t.Fatal("expected codex_sync to be attached for the Codex agent")
	}
}

func TestAgentHandler_CodexSyncAttached(t *testing.T) {
	st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	registry := cliagent.NewRegistry(dashboardCLIStubAdapter{
		backend: cliagent.BackendCodex,
		models:  []string{"gpt-5.2-codex"},
	})
	handler := New(st)
	handler.SetCLIAgentRegistry(registry)
	handler.SetCodexSyncProvider(func() any {
		return map[string]any{"config": map[string]any{"model": "gpt-5.2-codex"}}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/agents/Codex", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("ServeHTTP() status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("codex_sync")) {
		t.Fatalf("expected codex_sync in /api/agents/Codex response: %s", rr.Body.String())
	}
}
