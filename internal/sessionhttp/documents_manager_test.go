package sessionhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readBlankManagerPlan(t *testing.T, h *Handler, workspaceName, purpose string) templateAgentPlan {
	t.Helper()
	body := fmt.Sprintf(`{"workspace_name":%q,"blank":true,"guided_purpose":%q}`, workspaceName, purpose)
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces/template-agent-plan", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleWorkspaces(w, r)
	var plan templateAgentPlan
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &plan) != nil || len(plan.Agents) != 1 || plan.Revision == "" {
		t.Fatalf("plan: %d %s", w.Code, w.Body)
	}
	return plan
}

func documentsCreationMaterial(t *testing.T, h *Handler, name, path string) (map[string]any, templateAgentPlan) {
	t.Helper()
	plan := readBlankManagerPlan(t, h, name, documentsPurpose)
	return map[string]any{"name": name, "anchor_path": path, "blank": true, "guided_purpose": documentsPurpose, "team_intent": map[string]any{"version": 1, "mode": "staffed", "plan_revision": plan.Revision}, "role_staffing": []map[string]string{{"role_id": "manager", "mode": "create", "name": plan.EntryAgentName}}}, plan
}

func TestDocumentsManagerCreate_ReviewedIdentityAndConfigurationSurviveReadback(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	for i, name := range []string{"My shelves", "My shelves II", "Edited shelves"} {
		anchor := canonicalAnchorTestDir(t)
		material, plan := documentsCreationMaterial(t, h, name, anchor)
		holder, prompt := plan.EntryAgentName, plan.Agents[0].SystemPrompt
		if holder != []string{"Documents Manager", "Documents Manager 2", "Documents Manager 3"}[i] {
			t.Fatalf("unexpected proposal %q", holder)
		}
		if i == 2 {
			holder, prompt = "Archivist", "Help me summarize my letters."
			material["role_staffing"] = []map[string]string{{"role_id": "manager", "mode": "create", "name": holder, "system_prompt": prompt, "provider": "anthropic", "model": "reviewed-model"}}
		}
		id, digest := reviewCreationOperation(t, h, material)
		w, result := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if w.Code != http.StatusCreated {
			t.Fatalf("confirm: %d %s", w.Code, w.Body)
		}
		wsID := result["folder"].(map[string]any)["id"].(string)
		replay, observed := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if replay.Code != http.StatusCreated || observed["folder"].(map[string]any)["id"] != wsID || len(h.agentStore.ListAgents()) != i+1 {
			t.Fatalf("retry did not retain one manager for %s: %d %s", wsID, replay.Code, replay.Body)
		}
		ws, err := h.workspaceTaskStore.Get(wsID)
		if err != nil {
			t.Fatal(err)
		}
		// Anchoring already supplies a scoped read-only filesystem binding. The
		// manager adds no tools of its own and must not broaden that binding.
		if len(ws.MCPBindings) != 1 || ws.MCPBindings[0].Alias != "anchor-folder" || ws.MCPBindings[0].DefaultSideEffect != "read" || len(ws.DirectoryReferences) != 1 || ws.DirectoryReferences[0].Path != anchor {
			t.Fatalf("unexpected anchor grant: %#v / %#v", ws.MCPBindings, ws.DirectoryReferences)
		}
		roster := h.buildWorkspaceRoster(ws)
		if ws.EntryAgentName() != holder || len(ws.AgentInstances) != 1 || len(roster.Roles) != 1 || roster.Roles[0].RoleID != "manager" || roster.Roles[0].Label != "Documents Manager" || roster.Roles[0].Agent == nil || roster.Roles[0].Agent.Name != holder {
			t.Fatalf("unexpected persisted team: %#v", roster)
		}
		profile, ok := h.agentStore.GetAgent(holder)
		if !ok || !profile.WorkspaceManager.ManagesWorkspace(wsID) || profile.Settings.SystemPrompt != prompt || (i == 2 && (profile.Settings.Provider != "anthropic" || profile.Settings.Model != "reviewed-model")) {
			t.Fatalf("%q did not retain its reviewed settings or provenance", holder)
		}
	}
}

func TestDocumentsManager_AnchorPermissionLossRefusesTheSameOperation(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires an unprivileged POSIX filesystem")
	}
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	parent := canonicalAnchorTestDir(t)
	anchor := filepath.Join(parent, "letters")
	if err := os.Mkdir(anchor, 0750); err != nil {
		t.Fatal(err)
	}
	material, plan := documentsCreationMaterial(t, h, "Shelves", anchor)
	id, digest := reviewCreationOperation(t, h, material)
	if err := os.Chmod(parent, 0); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chmod(parent, 0750); err != nil {
			t.Error(err)
		}
	}()
	for range 2 {
		w, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("permission refusal: %d %s", w.Code, w.Body)
		}
		assertCreationOperationState(t, h, id, "refused")
	}
	assertAgentAbsent(t, h, plan.EntryAgentName)
	if rows, err := h.store.ListWorkspaces(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("refusal left workspaces: %v %v", rows, err)
	}
}

func TestDocumentsManager_FailedPersistenceRollsBackProfileAndDoesNotRetry(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	material, plan := documentsCreationMaterial(t, h, "Shelves", canonicalAnchorTestDir(t))
	workingStore := h.store
	h.store = failingWorkspaceCreateStore{HybridStore: workingStore, err: errors.New("simulated persistence failure")}
	id, digest := reviewCreationOperation(t, h, material)
	for range 2 {
		w, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if w.Code != http.StatusConflict {
			t.Fatalf("failed write: %d %s", w.Code, w.Body)
		}
		assertCreationOperationState(t, h, id, "reconciliation_required")
		assertAgentAbsent(t, h, plan.EntryAgentName)
		// Even with persistence restored, a retry observes the accepted failure.
		h.store = workingStore
	}
	if rows, err := h.store.ListWorkspaces(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("failed operation created a workspace: %v %v", rows, err)
	}
}

func TestDocumentsManagerPlan_OnlyExplicitPurposeChangesManagerDefaults(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	plain := readBlankManagerPlan(t, h, "Documents", "")
	guided := readBlankManagerPlan(t, h, "Documents", documentsPurpose)
	renamed := readBlankManagerPlan(t, h, "Reviewed shelves", documentsPurpose)
	if plain.EntryAgentName != "Documents Manager" || guided.EntryAgentName != plain.EntryAgentName || plain.Revision == guided.Revision || renamed.Revision != guided.Revision {
		t.Fatalf("purpose not isolated from display name: %#v %#v %#v", plain, guided, renamed)
	}
	a := guided.Agents[0]
	if a.RoleID != plain.Agents[0].RoleID || a.RoleLabel != "Documents Manager" || plain.Agents[0].RoleLabel != "Manager" || a.Action != "create" || a.SystemPrompt == plain.Agents[0].SystemPrompt || !strings.Contains(a.SystemPrompt, "only with this workspace") {
		t.Fatalf("unexpected guided manager: %#v", a)
	}
	if len(h.agentStore.ListAgents()) != 0 {
		t.Fatal("preview created a profile")
	}
	for _, mode := range []string{"staffed", "agentless"} {
		body := map[string]any{"name": "Documents", "blank": true, "guided_purpose": documentsPurpose, "team_intent": map[string]any{"version": 1, "mode": mode, "plan_revision": plain.Revision}, "role_staffing": []any{}}
		id, digest := reviewCreationOperation(t, h, body)
		w, result := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if w.Code != http.StatusConflict || result["code"] != "team_readiness" {
			t.Fatalf("%s accepted ordinary revision: %d %s", mode, w.Code, w.Body)
		}
	}
}
