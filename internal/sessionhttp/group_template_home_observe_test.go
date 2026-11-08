package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func observeGroupHome(t *testing.T, handler *Handler, request map[string]any) (int, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ObserveGroupTemplateHome(response, httptest.NewRequest(http.MethodPost, "/api/workspaces/group-templates/observe", bytes.NewBufferString(groupTemplateHomeBody(t, request))))
	var body struct {
		Result map[string]any `json:"group_template"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return response.Code, body.Result
}

func TestGroupTemplateHomeExactTeamObservationNeverExecutesUnfinishedRoles(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "home_only_partial"}[blocked], func(t *testing.T) {
			h, workspaces, id, revision, cleanup := newGroupTemplateHomeHandler(t)
			defer cleanup()
			fixture := newBuildFixtureOn(t, h, false)
			if err := h.agentStore.UpdateAgent("Scout", func(profile *agent.Agent) error {
				profile.Settings.Provider, profile.Settings.Model = "scripted", "scripted-1"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			wireReviewedGroupStaffing(t, h, workspaces)
			if blocked {
				h.SetAssistantWorkspaceRoleStaffer(func(context.Context, string, []RoleStaffingFill) error { return setupjourney.ErrInvalid })
			}
			request := map[string]any{"group_template_id": id, "revision": revision, "name": "Exact collection", "role_staffing": []groupHomeRoleChoice{{RoleID: "home-lead", Mode: "assign", Name: "Scout"}}}
			code, body := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, request))
			if code != http.StatusOK {
				t.Fatal("review failed", code)
			}
			request["group_review_token"], request["idempotency_key"] = body["group_template_review"].(map[string]any)["review_token"], "observed-exact-team"
			if code, _ := observeGroupHome(t, h, request); code != http.StatusNotFound {
				t.Fatal("unaccepted observation", code)
			}
			code, body = postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, request))
			if code != http.StatusOK {
				t.Fatal("Home commit failed", code)
			}
			result := body["group_template"].(map[string]any)
			wantState, wantAgents := "staffed", 1
			if blocked {
				wantState, wantAgents = "needs_review", 0
			}
			if result["home_staffing"].(map[string]any)["state"] != wantState {
				t.Fatal("wrong exact team outcome")
			}
			homeID := result["home_workspace_id"].(string)
			before, _ := workspaces.Get(homeID)
			beforeJSON, _ := json.Marshal(before)
			profiles := len(h.agentStore.ListAgents())
			h.SetAssistantWorkspaceRoleStaffer(func(context.Context, string, []RoleStaffingFill) error {
				t.Fatal("observation resumed a role")
				return nil
			})
			h.SetGroupTemplateCatalog(func(string) ([]GroupTemplateCatalogEntry, bool, error) {
				t.Fatal("observation resolved unavailable provider")
				return nil, false, nil
			})
			for range 3 {
				code, observed := observeGroupHome(t, h, request)
				if code != http.StatusOK || observed["home_workspace_id"] != homeID || observed["state"] != "home_ready" || observed["home_staffing"] != nil {
					t.Fatal("observation fabricated role outcomes or lost exact Home", code, observed)
				}
			}
			for _, roles := range []any{nil, []groupHomeRoleChoice{{RoleID: "home-lead", Mode: "create", Name: "Scout"}}, []groupHomeRoleChoice{{RoleID: "home-lead", Mode: "assign", Name: "Luna"}}} {
				changed := make(map[string]any, len(request))
				for key, value := range request {
					changed[key] = value
				}
				if roles == nil {
					delete(changed, "role_staffing")
				} else {
					changed["role_staffing"] = roles
				}
				if code, _ := observeGroupHome(t, h, changed); code == http.StatusOK {
					t.Fatal("changed team looked up original operation")
				}
			}
			after, _ := workspaces.Get(homeID)
			afterJSON, _ := json.Marshal(after)
			if !bytes.Equal(beforeJSON, afterJSON) || len(after.GetAgentInstances()) != wantAgents || len(h.agentStore.ListAgents()) != profiles || fixture.provider.calls != 0 {
				t.Fatal("read-only observation mutated, staffed or inferred")
			}
		})
	}
}

func TestGroupTemplateHome_ObservationNeverCreatesResumesOrAdoptsAReplacement(t *testing.T) {
	handler, store, id, revision, cleanup := newGroupTemplateHomeHandler(t)
	defer cleanup()
	receipts := grouprequirements.NewMemoryStore()
	owner := func(context.Context) (string, error) { return "local", nil }
	handler.SetGroupRequirementService(grouprequirements.NewService(store, receipts), owner)
	selection := map[string]any{"group_template_id": id, "revision": revision, "name": "Original Home"}
	code, body := postGroupTemplateHome(t, handler, false, groupTemplateHomeBody(t, selection))
	if code != http.StatusOK {
		t.Fatalf("review = %d %v", code, body)
	}
	review := body["group_template_review"].(map[string]any)
	request := map[string]any{"group_template_id": id, "revision": revision, "name": "Original Home", "group_review_token": review["review_token"], "idempotency_key": "exact-original-operation"}
	if code, outcome := observeGroupHome(t, handler, request); code != http.StatusNotFound || outcome["state"] != "outcome_unknown" {
		t.Fatalf("unaccepted observation = %d %v", code, outcome)
	}
	if ids, err := store.List(); err != nil || len(ids) != 0 {
		t.Fatalf("unaccepted observation created resources: %v, %v", ids, err)
	}
	code, body = postGroupTemplateHome(t, handler, true, groupTemplateHomeBody(t, request))
	result, _ := body["group_template"].(map[string]any)
	if code != http.StatusOK || result["layout_subject_new"] != true {
		t.Fatalf("commit = %d %v", code, body)
	}
	homeID := result["home_workspace_id"].(string)
	before, err := store.Get(homeID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a reconstructed owner and an unavailable catalog. Neither is a
	// reason for a read to replay confirmation or execute staffing/setup.
	handler.SetGroupRequirementService(grouprequirements.NewService(store, receipts), owner)
	handler.SetGroupTemplateCatalog(func(string) ([]GroupTemplateCatalogEntry, bool, error) {
		panic("observation rebuilt the current template catalog")
	})
	handler.SetProjectTemplateResolver(func(string, string) (projecttemplates.Template, error) {
		panic("observation resolved a creation template")
	})
	for i := 0; i < 3; i++ {
		code, outcome := observeGroupHome(t, handler, request)
		if code != http.StatusOK || outcome["state"] != "home_ready" || outcome["home_workspace_id"] != homeID || outcome["layout_subject_new"] != true {
			t.Fatalf("accepted observation = %d %v", code, outcome)
		}
	}
	after, err := store.Get(homeID)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if !bytes.Equal(beforeJSON, afterJSON) || len(after.GetAgentInstances()) != 0 {
		t.Fatal("observing the Home changed its canonical data or staffed agents")
	}
	for _, change := range []map[string]any{{"name": "Changed"}, {"idempotency_key": "another-operation"}, {"group_review_token": "another-review"}, {"revision": "changed"}} {
		changed := make(map[string]any, len(request))
		for key, value := range request {
			changed[key] = value
		}
		for key, value := range change {
			changed[key] = value
		}
		if code, _ := observeGroupHome(t, handler, changed); code == http.StatusOK {
			t.Fatalf("changed observation adopted original result: %v", change)
		}
	}
	handler.currentUserID = func(context.Context) (string, error) { return "foreign-owner", nil }
	if code, _ := observeGroupHome(t, handler, request); code == http.StatusOK {
		t.Fatal("foreign owner observed the original operation")
	}
	handler.currentUserID = owner
	operation, err := receipts.GetOperation(t.Context(), "local", grouprequirements.OperationPrepareHome, "exact-original-operation")
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.groupRequirements.Mark(t.Context(), operation, grouprequirements.OperationReconcileRequired); err != nil {
		t.Fatal(err)
	}
	partial, err := receipts.GetOperation(t.Context(), "local", grouprequirements.OperationPrepareHome, "exact-original-operation")
	if err != nil {
		t.Fatal(err)
	}
	if code, outcome := observeGroupHome(t, handler, request); code != http.StatusAccepted || outcome["state"] != "reconcile_required" || outcome["layout_subject_new"] != nil {
		t.Fatalf("partial observation = %d %v", code, outcome)
	}
	unchanged, err := receipts.GetOperation(t.Context(), "local", grouprequirements.OperationPrepareHome, "exact-original-operation")
	if err != nil || !reflect.DeepEqual(partial, unchanged) {
		t.Fatalf("read resumed partial operation: %#v, %v", unchanged, err)
	}
	if err := handler.groupRequirements.Mark(t.Context(), operation, grouprequirements.OperationSucceeded); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(homeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.GroupTemplate = nil
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if code, outcome := observeGroupHome(t, handler, request); code != http.StatusOK || outcome["layout_subject_new"] != false {
		t.Fatalf("bare Home ID became creation proof: %d %v", code, outcome)
	}
	if err := store.Update(homeID, func(home *workspace.Workspace) error { home.Status = workspace.StatusTrashed; return nil }); err != nil {
		t.Fatal(err)
	}
	if code, outcome := observeGroupHome(t, handler, request); code != http.StatusConflict || outcome["state"] != "resource_unavailable" {
		t.Fatalf("trashed Home observation = %d %v", code, outcome)
	}
	if ids, err := store.List(); err != nil || len(ids) != 1 {
		t.Fatalf("observation recreated a trashed Home: %v, %v", ids, err)
	}
}
