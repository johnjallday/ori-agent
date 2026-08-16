package orchestrationhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Approving a dynamic agent must not also run the work (FR-59, FR-60).
//
// It used to: approving the last outstanding agent request resumed the pending
// plan and executed a multi-agent workflow. One click meant both "this agent
// may exist" and "run everything", with no durable Plan behind the second —
// no version, no content hash, nothing an audit could later explain. Those are
// two decisions and only the first is being made here.
func TestApprovingADynamicAgentDoesNotResumeExecution(t *testing.T) {
	ws := &workspace.Workspace{
		ID: "ws-1",
		PendingPlan: &types.PendingPlan{
			ID: "pending-1",
		},
		DynamicAgentRequests: []types.DynamicAgentRequest{
			{
				ID:     "req-1",
				PlanID: "pending-1",
				Status: types.DynamicAgentStatusPending,
			},
		},
	}

	handler := &DynamicAgentHandler{
		workspaceStore: &autoTaskWorkspaceStore{
			workspaces: map[string]*workspace.Workspace{"ws-1": ws},
		},
		// No orchestrator: if the handler still tried to resume, this would be
		// the nil it dereferenced.
		orchestrator: nil,
	}

	body, _ := json.Marshal(map[string]any{
		"workspace_id": "ws-1",
		"request_id":   "req-1",
		"approve":      true,
		"approved_by":  "jj",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/dynamic-agents/approve",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.DynamicAgentApprovalHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp["resume_result"] != nil {
		t.Errorf("approving an agent resumed execution: %#v", resp["resume_result"])
	}
	// And it says what remains to be done, rather than leaving the user to
	// wonder why nothing ran.
	if requiresPlan, _ := resp["requires_plan"].(bool); !requiresPlan {
		t.Error("the response does not tell the user a plan is still needed")
	}
	if reason, _ := resp["plan_reason"].(string); reason == "" {
		t.Error("the response gives no reason a plan is needed")
	}
}

// Denying still works and clears the pending plan.
func TestDenyingADynamicAgentClearsThePendingPlan(t *testing.T) {
	ws := &workspace.Workspace{
		ID:          "ws-1",
		PendingPlan: &types.PendingPlan{ID: "pending-1"},
		DynamicAgentRequests: []types.DynamicAgentRequest{
			{ID: "req-1", PlanID: "pending-1", Status: types.DynamicAgentStatusPending},
		},
	}

	handler := &DynamicAgentHandler{
		workspaceStore: &autoTaskWorkspaceStore{
			workspaces: map[string]*workspace.Workspace{"ws-1": ws},
		},
	}

	body, _ := json.Marshal(map[string]any{
		"workspace_id": "ws-1",
		"request_id":   "req-1",
		"approve":      false,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/dynamic-agents/approve",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.DynamicAgentApprovalHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if ws.PendingPlan != nil {
		t.Error("denying an agent left the pending plan in place")
	}
}
