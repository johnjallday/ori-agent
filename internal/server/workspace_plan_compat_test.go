package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Retiring the first-party "new Plan" UI does not retire the Plan API. An
// external client keeps creating, reading and reviewing Plans, and creating one
// never depends on the workspace's planning setting: the setting only shapes
// artifacts and what the old UI offered. These tests pin that contract so a
// later change to the first-party surface cannot quietly start gating it.

func planAPICall(t *testing.T, handler http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	var rec *httptest.ResponseRecorder
	if body == "" {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	} else {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

func setPlanningEnabledForTest(t *testing.T, handler http.Handler, workspaceID string, enabled bool) {
	t.Helper()
	body := `{"planning":{"enabled":false}}`
	if enabled {
		body = `{"planning":{"enabled":true}}`
	}
	status, resp := planAPICall(t, handler, http.MethodPatch, "/api/workspaces/"+workspaceID+"/settings", body)
	if status != http.StatusOK {
		t.Fatalf("patch settings status = %d (%v)", status, resp)
	}
	settings, _ := resp["settings"].(map[string]any)
	planning, _ := settings["planning"].(map[string]any)
	if got, _ := planning["enabled"].(bool); got != enabled {
		t.Fatalf("planning.enabled = %v after patch, want %v (%v)", planning["enabled"], enabled, resp)
	}
}

func TestWorkspacePlanAPICreatesWhetherOrNotPlanningIsEnabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "planning disabled"
		if enabled {
			name = "planning enabled"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			handler := newRoutesTestHandler(t)
			workspaceID := createWorkspaceForTest(t, handler, "Compat Workspace")
			setPlanningEnabledForTest(t, handler, workspaceID, enabled)

			planID := createPlanForTest(t, handler, workspaceID, "Plan the release")

			status, plan := planAPICall(t, handler, http.MethodGet, "/api/workspaces/"+workspaceID+"/plans/"+planID, "")
			if status != http.StatusOK {
				t.Fatalf("read plan status = %d (%v)", status, plan)
			}
			if got := plan["status"]; got != "draft" {
				t.Fatalf("a new plan is a draft, got status %v", got)
			}

			// A setting that is off still leaves the existing record reachable
			// from the collection, with nothing materialized behind it.
			status, list := planAPICall(t, handler, http.MethodGet, "/api/workspaces/"+workspaceID+"/plans?scope=all", "")
			if status != http.StatusOK {
				t.Fatalf("list status = %d (%v)", status, list)
			}
			plans, _ := list["plans"].([]any)
			if len(plans) != 1 {
				t.Fatalf("collection holds %d plan(s), want 1: %v", len(plans), list)
			}
		})
	}
}

// The chrome links to the Plans list only for a workspace that has a plan, and
// learns that from a bounded presence read. Every way a plan can exist after the
// upgrade must count: made over the API with no ticket yet, in History, or in a
// workspace whose planning setting is off. An empty workspace must not.
func TestWorkspacePlanPresenceCountsEveryRecordTheWorkspaceHas(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handler := newRoutesTestHandler(t)

	presence := func(workspaceID string) map[string]any {
		t.Helper()
		status, body := planAPICall(t, handler, http.MethodGet, "/api/workspaces/"+workspaceID+"/plan-presence", "")
		if status != http.StatusOK {
			t.Fatalf("presence status = %d (%v)", status, body)
		}
		return body
	}

	empty := createWorkspaceForTest(t, handler, "Presence Empty")
	if got := presence(empty); got["has_plans"] != false || got["active"] != float64(0) || got["history"] != float64(0) {
		t.Fatalf("an empty workspace reports %v", got)
	}

	// A plan made over the API after the upgrade, with no ticket and planning off.
	apiOnly := createWorkspaceForTest(t, handler, "Presence API")
	setPlanningEnabledForTest(t, handler, apiOnly, false)
	planID := createPlanForTest(t, handler, apiOnly, "Created by an external client")
	if got := presence(apiOnly); got["has_plans"] != true || got["active"] != float64(1) {
		t.Fatalf("an API-created plan with no ticket reports %v", got)
	}

	// Archived, it is History, and still a record the workspace has.
	status, body := planAPICall(t, handler, http.MethodPost,
		"/api/workspaces/"+apiOnly+"/plans/"+planID+"/archive", `{"reason":"done"}`)
	if status != http.StatusOK {
		t.Fatalf("archive status = %d (%v)", status, body)
	}
	if got := presence(apiOnly); got["has_plans"] != true || got["active"] != float64(0) || got["history"] != float64(1) {
		t.Fatalf("a history-only workspace reports %v", got)
	}

	// Another workspace's plans never count here.
	if got := presence(empty); got["has_plans"] != false {
		t.Fatalf("the empty workspace now reports %v", got)
	}

	// Presence is a read: it does not accept writes.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if status, _ := planAPICall(t, handler, method, "/api/workspaces/"+apiOnly+"/plan-presence", ""); status != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, status)
		}
	}
}

// Opening or reading a Plan advances nothing. Starting execution and creating
// tickets stay behind approval, over HTTP exactly as in the service.
func TestWorkspacePlanAPIDraftPlanRefusesExecutionAndMaterialization(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handler := newRoutesTestHandler(t)
	workspaceID := createWorkspaceForTest(t, handler, "Compat Gates Workspace")
	planID := createPlanForTest(t, handler, workspaceID, "Plan the release")
	base := "/api/workspaces/" + workspaceID + "/plans/" + planID

	// Reads and review disclosures first: none of them may change the Plan.
	for _, path := range []string{base, base + "/versions", base + "/activity"} {
		if status, resp := planAPICall(t, handler, http.MethodGet, path, ""); status != http.StatusOK {
			t.Fatalf("GET %s status = %d (%v)", path, status, resp)
		}
	}

	for _, tc := range []struct{ name, path, body string }{
		{"start", base + "/execution", `{"action":"start"}`},
		{"materialize", base + "/materialize", `{}`},
	} {
		status, resp := planAPICall(t, handler, http.MethodPost, tc.path, tc.body)
		if status < 400 || status >= 500 {
			t.Fatalf("%s on a draft status = %d, want a 4xx refusal (%v)", tc.name, status, resp)
		}
	}

	status, plan := planAPICall(t, handler, http.MethodGet, base, "")
	if status != http.StatusOK || plan["status"] != "draft" {
		t.Fatalf("plan must still be an untouched draft, got %d status=%v", status, plan["status"])
	}
}
