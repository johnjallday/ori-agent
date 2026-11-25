package sessionhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		ws, err := h.workspaceTaskStore.Get(wsID)
		if err != nil {
			t.Fatal(err)
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
