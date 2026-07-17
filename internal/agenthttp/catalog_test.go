package agenthttp

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// fakeModelCategoryReader is a minimal ModelCategoryReader for tests, avoiding
// a dependency on the on-disk store.ModelCategoryStore implementation.
type fakeModelCategoryReader struct {
	assignments map[string][]string
}

func (f *fakeModelCategoryReader) GetAllModelAssignments() map[string][]string {
	return f.assignments
}

func catalogTestHandler(t *testing.T) *Handler {
	t.Helper()
	tmpDir := t.TempDir()
	st, err := store.NewFileStore(filepath.Join(tmpDir, "agents_index.json"), types.Settings{Model: "default-model", Temperature: 1.0})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return New(st)
}

func TestCatalogHandlerReturnsSixEntries(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/agents/catalog", nil)
	rr := httptest.NewRecorder()
	CatalogHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "\"entries\"") {
		t.Fatalf("expected entries key in response, got %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Commander") {
		t.Fatalf("expected Commander entry in response, got %s", rr.Body.String())
	}
}

func TestCatalogHandlerRejectsNonGet(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/agents/catalog", nil)
	rr := httptest.NewRecorder()
	CatalogHandler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestCatalogCreateAppliesPresetsAndResolvesModel(t *testing.T) {
	h := catalogTestHandler(t)
	h.SetModelCategoryStore(&fakeModelCategoryReader{
		assignments: map[string][]string{"claude-3-haiku-20240307": {"cat_default_tool_calling"}},
	})

	body := `{"name":"Reaper Specialist","catalog_role":"specialist","domain":"reaper"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	ag, ok := h.State.GetAgent("Reaper Specialist")
	if !ok || ag == nil {
		t.Fatal("expected agent to be created")
	}
	if ag.Role != types.RoleSpecialist {
		t.Errorf("expected role specialist, got %q", ag.Role)
	}
	if ag.Settings.Model != "claude-3-haiku-20240307" {
		t.Errorf("expected resolved model, got %q", ag.Settings.Model)
	}
	if !strings.Contains(ag.Settings.SystemPrompt, "reaper") {
		t.Errorf("expected domain to be woven into system prompt, got %q", ag.Settings.SystemPrompt)
	}
	if ag.Metadata == nil || ag.Metadata.RoutingProfile == nil {
		t.Fatal("expected routing profile to be set")
	}
	if len(ag.Metadata.RoutingProfile.Domains) != 1 || ag.Metadata.RoutingProfile.Domains[0] != "reaper" {
		t.Errorf("expected domains=[reaper], got %v", ag.Metadata.RoutingProfile.Domains)
	}
	if ag.Evolution == nil || ag.Evolution.Stage != types.AgentStageSpark || ag.Evolution.Level != 0 {
		t.Errorf("expected fresh evolution at spark/0, got %+v", ag.Evolution)
	}
}

func TestCatalogCreateFallsBackToDefaultModelWhenCategoryUnconfigured(t *testing.T) {
	h := catalogTestHandler(t)
	// No model-category store wired at all.

	body := `{"name":"Commander One","catalog_role":"orchestrator"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "model_category_fallback") {
		t.Fatalf("expected fallback notice, got %s", rr.Body.String())
	}

	ag, ok := h.State.GetAgent("Commander One")
	if !ok || ag == nil {
		t.Fatal("expected agent to be created")
	}
	if ag.Settings.Model != "default-model" {
		t.Errorf("expected fallback to store default model, got %q", ag.Settings.Model)
	}
	if ag.Role != types.RoleOrchestrator {
		t.Errorf("expected role orchestrator, got %q", ag.Role)
	}
}

func TestCatalogCreateUnknownRoleRejected(t *testing.T) {
	h := catalogTestHandler(t)
	body := `{"name":"Bad","catalog_role":"wizard"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown catalog role, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCatalogNameReservedForEndpoint(t *testing.T) {
	h := catalogTestHandler(t)
	body := `{"name":"catalog","catalog_role":"researcher"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for reserved name 'catalog', got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateAgentExpertModeMetadataOnly(t *testing.T) {
	h := catalogTestHandler(t)
	if err := h.State.CreateAgent("Worker", &store.CreateAgentConfig{
		Model:        "gpt-4o-mini",
		SystemPrompt: "original prompt",
		Role:         types.RoleResearcher,
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	body := `{"expert_mode":true}`
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/Worker", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	ag, ok := h.State.GetAgent("Worker")
	if !ok || ag == nil {
		t.Fatal("agent missing after update")
	}
	if ag.Metadata == nil || ag.Metadata.ExpertMode == nil || !*ag.Metadata.ExpertMode {
		t.Fatalf("expected expert_mode=true, got %+v", ag.Metadata)
	}
	// Metadata-only: model and prompt untouched.
	if ag.Settings.Model != "gpt-4o-mini" {
		t.Errorf("expert-mode update changed model to %q", ag.Settings.Model)
	}
	if ag.Settings.SystemPrompt != "original prompt" {
		t.Errorf("expert-mode update changed system prompt to %q", ag.Settings.SystemPrompt)
	}
	if ag.Role != types.RoleResearcher {
		t.Errorf("expert-mode update changed role to %q", ag.Role)
	}
}

func TestPlainCreateUnaffectedByCatalogChanges(t *testing.T) {
	h := catalogTestHandler(t)
	body := `{"name":"Plain Agent","model":"gpt-4o-mini","system_prompt":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	ag, ok := h.State.GetAgent("Plain Agent")
	if !ok || ag == nil {
		t.Fatal("expected agent to be created")
	}
	if ag.Settings.Model != "gpt-4o-mini" {
		t.Errorf("expected explicit model, got %q", ag.Settings.Model)
	}
	if ag.Settings.SystemPrompt != "hello" {
		t.Errorf("expected explicit system prompt, got %q", ag.Settings.SystemPrompt)
	}
	// Plain (non-catalog) creation defaults to RoleGeneral.
	if ag.Role != types.RoleGeneral {
		t.Errorf("expected default role general, got %q", ag.Role)
	}
}
