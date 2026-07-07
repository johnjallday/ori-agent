package agenthttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
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
		if err := st.CreateAgent(name, &store.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
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
