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
