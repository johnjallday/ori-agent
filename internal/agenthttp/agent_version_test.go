package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// TestAgentConfigVersion_StableAndSensitive verifies the concurrency token is
// deterministic, changes when the editable definition changes, and — critically
// — does NOT change when only activity statistics change (RecordMessage bumps
// Statistics.UpdatedAt on every message, which must not trigger false 409s).
func TestAgentConfigVersion_StableAndSensitive(t *testing.T) {
	base := &agent.Agent{
		Type: agent.TypeGeneral,
		Role: types.RoleGeneral,
		Settings: types.Settings{
			Model:        "gpt-4o-mini",
			Temperature:  0.7,
			SystemPrompt: "hello",
		},
		Metadata: &types.AgentMetadata{Description: "d", Tags: []string{"a", "b"}},
	}
	base.InitializeStatistics()

	v1 := agentConfigVersion(base)
	if v1 == "" {
		t.Fatal("expected non-empty version")
	}
	if got := agentConfigVersion(base); got != v1 {
		t.Errorf("version not stable: %q vs %q", v1, got)
	}

	// Activity-only change must not move the token.
	base.Statistics.RecordMessage(100, 0.01)
	if got := agentConfigVersion(base); got != v1 {
		t.Errorf("activity changed version: %q -> %q", v1, got)
	}

	// Config change must move the token.
	base.Settings.SystemPrompt = "goodbye"
	if got := agentConfigVersion(base); got == v1 {
		t.Errorf("system prompt change did not move version (%q)", got)
	}

	if agentConfigVersion(nil) != "" {
		t.Error("nil agent should yield empty version")
	}
}

// versionTestHandlers builds an agent Handler and a DashboardHandler over the
// same store, plus one editable agent with no workspace membership (so the
// shared-edit guard never interferes with the stale-edit assertions).
func versionTestHandlers(t *testing.T, agentName string) (*Handler, *DashboardHandler) {
	t.Helper()
	st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents_index.json"), types.Settings{Model: "gpt-4o-mini", Temperature: 1.0})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := st.CreateAgent(agentName, &store.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return New(st), NewDashboardHandler(st)
}

func fetchAgentVersion(t *testing.T, dash *DashboardHandler, name string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+name+"/detail", nil)
	rr := httptest.NewRecorder()
	dash.GetAgentDetail(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail GET: expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	return resp.Version
}

// TestStaleEditRejected verifies the detail endpoint echoes a version and that
// handleUpdate (PATCH) rejects an update carrying a stale version (409) while
// accepting the current one (PRD FR13).
func TestStaleEditRejected(t *testing.T) {
	h, dash := versionTestHandlers(t, "Solo")

	version := fetchAgentVersion(t, dash, "Solo")
	if version == "" {
		t.Fatal("expected detail to include a version token")
	}

	// Stale version → 409.
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/Solo", strings.NewReader(`{"system_prompt":"v2","expected_version":"deadbeefdeadbeef"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale edit: expected 409, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "stale_agent_edit") {
		t.Errorf("expected stale_agent_edit error, got %s", rr.Body.String())
	}

	// Correct version → 200.
	req = httptest.NewRequest(http.MethodPatch, "/api/agents/Solo", strings.NewReader(`{"system_prompt":"v2","expected_version":"`+version+`"}`))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("current version: expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	// The version must have moved after the successful edit; the old token is now
	// stale.
	newVersion := fetchAgentVersion(t, dash, "Solo")
	if newVersion == version {
		t.Error("version did not change after a config edit")
	}
	req = httptest.NewRequest(http.MethodPatch, "/api/agents/Solo", strings.NewReader(`{"system_prompt":"v3","expected_version":"`+version+`"}`))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("reused old version: expected 409, got %d", rr.Code)
	}
}

// TestUpdateWithoutVersionSkipsCheck verifies back-compat: an update that omits
// expected_version is not subjected to the concurrency check.
func TestUpdateWithoutVersionSkipsCheck(t *testing.T) {
	h, _ := versionTestHandlers(t, "Solo")
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/Solo", strings.NewReader(`{"system_prompt":"no-version"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("no-version update: expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}
