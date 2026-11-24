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
