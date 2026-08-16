package chathttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

	preview, ok := resp["action_preview"].(map[string]any)
	if !ok {
		t.Fatalf("expected action_preview object, got %T", resp["action_preview"])
	}
	if mode, _ := preview["route_mode"].(string); mode != string(UtilityRouteDirect) {
		t.Fatalf("expected route_mode %q, got %q", UtilityRouteDirect, mode)
	}
	// Exactly one. A preview carrying several steps is the shape that let
	// multi-agent work be approved from a chat bubble (FR-20).
	steps, ok := preview["steps"].([]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("expected exactly one previewed action, got %#v", preview["steps"])
	}
}

// A preview ID must not look like a durable Plan ID. The old implementation
// used the same "plan_" prefix workspaceplan uses, so an ID in a log or a
// support question was ambiguous between a record that survives and one that
// does not.
func TestActionPreviewIDCannotBeMistakenForAPlan(t *testing.T) {
	preview, err := directToolPreview("run it", &DirectToolCommand{ToolName: "get_time"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if strings.HasPrefix(preview.ID, "plan_") {
		t.Errorf("preview id %q uses the durable Plan prefix", preview.ID)
	}
	if !strings.HasPrefix(preview.ID, "preview_") {
		t.Errorf("preview id %q is not identifiable as a preview", preview.ID)
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
