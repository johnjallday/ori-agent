package sessionhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// TestPutWorkspaceRole_AssistantHomeDispatchesCreateAndAssignToTheExactTarget
// characterizes the HTTP owner's boundary. The handler correctly recognizes a
// prepared Assistant Program Home and passes that exact workspace ID plus the
// normalized one-role request to its injected staffing owner. The production
// conflict observed in the browser therefore sits behind this callback, not in
// route or station detection here.
func TestPutWorkspaceRole_AssistantHomeDispatchesCreateAndAssignToTheExactTarget(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, _, cleanup := newPolicyHandler(t, &template)
	defer cleanup()

	homeID := preparePolicyHome(t, handler)
	for _, tc := range []struct {
		name string
		body string
		mode string
	}{
		{name: "create", body: `{"mode":"create","name":"Home Lead"}`, mode: roleStaffingModeCreate},
		{name: "assign", body: `{"mode":"assign","name":"Saved Home Lead"}`, mode: roleStaffingModeAssign},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calledWorkspace string
			var called []RoleStaffingFill
			handler.SetAssistantWorkspaceRoleStaffer(func(_ context.Context, workspaceID string, fills []RoleStaffingFill) error {
				calledWorkspace = workspaceID
				called = append([]RoleStaffingFill(nil), fills...)
				return nil
			})

			req := httptest.NewRequest(http.MethodPut, "/api/workspaces/"+homeID+"/roles/portfolio_manager", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.SetPathValue("workspaceID", homeID)
			req.SetPathValue("roleID", "portfolio_manager")
			response := httptest.NewRecorder()
			handler.PutWorkspaceRole(response, req)

			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if calledWorkspace != homeID {
				t.Fatalf("callback workspace=%q, want exact Home %q", calledWorkspace, homeID)
			}
			if len(called) != 1 || called[0].RoleID != "portfolio_manager" || called[0].Mode != tc.mode {
				t.Fatalf("callback fills=%#v", called)
			}
		})
	}
}

func TestDeleteWorkspaceRole_AssistantHomeDispatchesToTheExactTarget(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, _, cleanup := newPolicyHandler(t, &template)
	defer cleanup()

	homeID := preparePolicyHome(t, handler)
	var calledWorkspace, calledRole string
	handler.SetAssistantRoleUnstaffer(func(_ context.Context, workspaceID, roleID string) error {
		calledWorkspace = workspaceID
		calledRole = roleID
		return nil
	})

	request := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+homeID+"/roles/portfolio_manager", nil)
	request.SetPathValue("workspaceID", homeID)
	request.SetPathValue("roleID", "portfolio_manager")
	response := httptest.NewRecorder()
	handler.DeleteWorkspaceRole(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if calledWorkspace != homeID || calledRole != "portfolio_manager" {
		t.Fatalf("callback target=%q role=%q, want %q/%q", calledWorkspace, calledRole, homeID, "portfolio_manager")
	}
}
