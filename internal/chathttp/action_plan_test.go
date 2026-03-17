package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newActionPlanTestHandler(t *testing.T) *Handler {
	t.Helper()

	st := newPreflightStore("Ori", &agent.Agent{
		Settings: types.Settings{
			Model:       "gpt-5-nano",
			Temperature: 0.3,
		},
		Plugins: map[string]types.LoadedPlugin{},
	})

	return NewHandler(st, nil)
}

func TestChatHandler_PlanBeforeAction_PreviewsUtilityRoute(t *testing.T) {
	h := newActionPlanTestHandler(t)

	body, _ := json.Marshal(map[string]any{
		"question":           "what time is it in tokyo",
		"plan_before_action": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if approved, _ := resp["requires_approval"].(bool); !approved {
		t.Fatalf("expected requires_approval=true, got %v", resp["requires_approval"])
	}
	if approvalType, _ := resp["approval_type"].(string); approvalType != "action_plan" {
		t.Fatalf("expected approval_type=action_plan, got %v", resp["approval_type"])
	}
	if planID, _ := resp["action_plan_id"].(string); planID == "" {
		t.Fatalf("expected non-empty action_plan_id")
	}

	plan, ok := resp["action_plan"].(map[string]any)
	if !ok {
		t.Fatalf("expected action_plan object, got %T", resp["action_plan"])
	}
	if mode, _ := plan["route_mode"].(string); mode != string(UtilityRouteDirect) {
		t.Fatalf("expected route_mode %q, got %q", UtilityRouteDirect, mode)
	}
	steps, ok := plan["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("expected at least one plan step, got %#v", plan["steps"])
	}
}

func TestBuildChatActionPlan_UsesAssistantTerminology(t *testing.T) {
	h := newActionPlanTestHandler(t)

	plan, _ := h.buildChatActionPlan(context.Background(), "say hi", UtilityRouteDecision{}, "", 0)
	if plan == nil {
		t.Fatal("expected action plan")
	}
	if len(plan.Steps) == 0 {
		t.Fatal("expected at least one plan step")
	}
	if got := plan.Steps[0].Title; got != "Ask the Assistant to answer" {
		t.Fatalf("expected Assistant wording, got %q", got)
	}
}

func TestChatHandler_PlanBeforeAction_ApprovedPlanExecutesAndReturnsReceipt(t *testing.T) {
	h := newActionPlanTestHandler(t)

	body, _ := json.Marshal(map[string]any{
		"question":                "what time is it in tokyo",
		"plan_before_action":      true,
		"approved_action_plan_id": "plan_test_approved",
		"multi_agent_mode":        "off",
		"multi_agent_threshold":   3.5,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if _, exists := resp["requires_approval"]; exists {
		t.Fatalf("expected approved flow to execute immediately, got requires_approval=%v", resp["requires_approval"])
	}
	if mode, _ := resp["route_mode"].(string); mode != string(UtilityRouteDirect) {
		t.Fatalf("expected route_mode %q, got %q", UtilityRouteDirect, mode)
	}

	receipts, ok := resp["action_receipts"].([]any)
	if !ok || len(receipts) == 0 {
		t.Fatalf("expected action_receipts in response, got %#v", resp["action_receipts"])
	}
	first, ok := receipts[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first receipt object, got %T", receipts[0])
	}
	if toolName, _ := first["tool_name"].(string); toolName != "time" {
		t.Fatalf("expected first receipt tool_name=time, got %q", toolName)
	}
}
