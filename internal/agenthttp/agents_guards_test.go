package agenthttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// guardTestHandler builds an agent handler wired to a workspace store, plus the
// named agents and workspaces (each workspace attaches the given agent names as
// instances, first = entry).
func guardTestHandler(t *testing.T, agents []string, wsAgents map[string][]string) *Handler {
	t.Helper()
	tmpDir := t.TempDir()

	st, err := store.NewFileStore(filepath.Join(tmpDir, "agents_index.json"), types.Settings{Model: "gpt-4o-mini", Temperature: 1.0})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	for _, name := range agents {
		if err := st.CreateAgent(name, &store.CreateAgentConfig{}); err != nil {
			t.Fatalf("CreateAgent %s: %v", name, err)
		}
	}

	wsPath := filepath.Join(tmpDir, "workspaces")
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	wsStore, err := workspace.NewFileStore(wsPath)
	if err != nil {
		t.Fatalf("workspace NewFileStore: %v", err)
	}
	for wsID, names := range wsAgents {
		insts := make([]workspace.AgentInstance, 0, len(names))
		for i, n := range names {
			insts = append(insts, workspace.AgentInstance{ID: wsID + "-" + n, Name: n, EntryPoint: i == 0})
		}
		shared := map[string]any{}
		if len(names) > 0 {
			shared["entry_agent_name"] = names[0]
		}
		if err := wsStore.Save(&workspace.Workspace{ID: wsID, Name: wsID, AgentInstances: insts, SharedData: shared}); err != nil {
			t.Fatalf("save ws %s: %v", wsID, err)
		}
	}

	h := New(st)
	h.SetWorkspaceStore(wsStore)
	return h
}

// TestSharedEditRequiresConfirmation verifies a definition attached to >1
// workspace rejects a shared-field edit without confirmation and accepts it with
// confirm_shared_edit=true (PRD FR9).
func TestSharedEditRequiresConfirmation(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Shared"},
		map[string][]string{"ws-a": {"Shared"}, "ws-b": {"Shared"}},
	)

	// Without confirmation → 409.
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/Shared", strings.NewReader(`{"system_prompt":"new"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 without confirmation, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "shared_agent_edit_requires_confirmation") {
		t.Errorf("expected shared-edit error code, got %s", rr.Body.String())
	}

	// With confirmation → 200.
	req = httptest.NewRequest(http.MethodPatch, "/api/agents/Shared", strings.NewReader(`{"system_prompt":"new","confirm_shared_edit":true}`))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with confirmation, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestSharedEditSingleWorkspaceNoConfirmation verifies a definition attached to
// exactly one workspace does not require confirmation.
func TestSharedEditSingleWorkspaceNoConfirmation(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Solo"},
		map[string][]string{"ws-a": {"Solo"}},
	)
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/Solo", strings.NewReader(`{"system_prompt":"new"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (single-workspace, no confirmation needed), got %d body=%s", rr.Code, rr.Body.String())
	}
}

type recordingCarrier struct {
	calls  []string
	result CarriedEdit
}

func (c *recordingCarrier) CarryEdit(agentName string) (CarriedEdit, error) {
	c.calls = append(c.calls, agentName)
	return c.result, nil
}

// D10: a saved model or prompt edit is carried to the copies Ori keeps in step,
// and the response says where it went. Other edits carry nothing.
func TestSharedEditIsCarriedToTheTrackedCopies(t *testing.T) {
	h := guardTestHandler(t, []string{"Shared"}, map[string][]string{"ws-a": {"Shared"}, "ws-b": {"Shared"}})
	carrier := &recordingCarrier{result: CarriedEdit{
		Updated:    []workspace.WorkspaceRef{{ID: "ws-a", Name: "Song A"}},
		Customised: []workspace.WorkspaceRef{{ID: "ws-b", Name: "Song B"}},
	}}
	h.SetEditCarrier(carrier)

	req := httptest.NewRequest(http.MethodPatch, "/api/agents/Shared", strings.NewReader(`{"model":"terra","confirm_shared_edit":true}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || len(carrier.calls) != 1 || carrier.calls[0] != "Shared" {
		t.Fatalf("model edit: %d %s calls=%v", rr.Code, rr.Body.String(), carrier.calls)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"carried":{"updated":[{"id":"ws-a"`) || !strings.Contains(body, `"customised":[{"id":"ws-b"`) {
		t.Fatalf("response does not say where the edit went: %s", body)
	}

	// A favourite or a description is not part of what is carried.
	req = httptest.NewRequest(http.MethodPatch, "/api/agents/Shared", strings.NewReader(`{"favorite":true}`))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || len(carrier.calls) != 1 || strings.Contains(rr.Body.String(), "carried") {
		t.Fatalf("favourite edit carried: %d %s calls=%v", rr.Code, rr.Body.String(), carrier.calls)
	}
	// A refused edit carries nothing.
	req = httptest.NewRequest(http.MethodPatch, "/api/agents/Shared", strings.NewReader(`{"system_prompt":"new"}`))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict || len(carrier.calls) != 1 {
		t.Fatalf("unconfirmed edit carried: %d calls=%v", rr.Code, carrier.calls)
	}
}

type stepReader map[string]bool

func (s stepReader) InStep(string) map[string]bool { return s }

// A copy Ori keeps in step is not listed as a customisation; any other copy is.
func TestDetailOriginDropsCopiesKeptInStep(t *testing.T) {
	h := NewDashboardHandler(nil)
	origin := &store.AgentOrigin{Source: store.SourceRoster, CustomisedIn: []store.WorkspaceRef{
		{ID: "song-a", Name: "Song A"}, {ID: "song-b", Name: "Song B"}, {ID: "studio", Name: "Studio"},
	}}
	if got := h.withoutCopiesInStep("Shared", origin); len(got.CustomisedIn) != 3 {
		t.Fatalf("unwired = %+v", got)
	}
	h.SetCopyStepReader(stepReader{"song-a": true})
	got := h.withoutCopiesInStep("Shared", origin)
	if len(got.CustomisedIn) != 2 || got.CustomisedIn[0].ID != "song-b" || got.CustomisedIn[1].ID != "studio" {
		t.Fatalf("filtered = %+v", got.CustomisedIn)
	}
	if len(origin.CustomisedIn) != 3 {
		t.Fatal("the filter changed the store's origin")
	}
}

// TestRenameAttachedDefinitionBlocked verifies renaming an attached definition
// is rejected (PRD FR10).
func TestRenameAttachedDefinitionBlocked(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Attached"},
		map[string][]string{"ws-a": {"Attached"}},
	)
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/Attached", strings.NewReader(`{"name":"Renamed"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 renaming attached definition, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "attached_agent_rename_blocked") {
		t.Errorf("expected rename-blocked error code, got %s", rr.Body.String())
	}
}

// TestDeleteAttachedDefinitionBlocked verifies delete is blocked while attached
// and allowed once unattached (PRD FR11).
func TestDeleteAttachedDefinitionBlocked(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Attached", "Loose"},
		map[string][]string{"ws-a": {"Attached"}},
	)

	// Attached → 409.
	req := httptest.NewRequest(http.MethodDelete, "/api/agents?name=Attached", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 deleting attached definition, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "attached_agent_delete_blocked") {
		t.Errorf("expected delete-blocked error code, got %s", rr.Body.String())
	}

	// Unattached → 200.
	req = httptest.NewRequest(http.MethodDelete, "/api/agents?name=Loose", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting unattached definition, got %d body=%s", rr.Code, rr.Body.String())
	}
}
