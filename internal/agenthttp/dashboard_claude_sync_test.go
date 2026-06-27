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
	t.Helper()
	st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	registry := cliagent.NewRegistry(dashboardCLIStubAdapter{
		backend: cliagent.BackendClaude,
		models:  []string{"opus"},
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
