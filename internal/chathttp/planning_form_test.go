package chathttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
)

func TestChatHandler_WorkspaceManagerTravelRequest_ReturnsPlanningForm(t *testing.T) {
	h := NewHandler(newPreflightStore("Spain Manager", &agent.Agent{Type: "workspace-manager"}), nil)

	body, _ := json.Marshal(map[string]any{
		"question":   "let's plan a trip to Spain",
		"agent_name": "Spain Manager",
		"route_context": map[string]any{
			"workspace_id": "workspace-spain",
			"surface":      "workspace_detail",
			"page_path":    "/workspaces/workspace-spain",
			"origin":       "ask_ori",
		},
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

	form, ok := resp["planning_form"].(map[string]any)
	if !ok {
		t.Fatalf("expected planning_form object, got %T", resp["planning_form"])
	}
	if kind, _ := form["kind"].(string); kind != "travel_intake" {
		t.Fatalf("expected travel_intake planning form, got %q", kind)
	}
	questions, ok := form["questions"].([]any)
	if !ok || len(questions) < 4 {
		t.Fatalf("expected planning form questions, got %#v", form["questions"])
	}
	if responseText, _ := resp["response"].(string); responseText == "" {
		t.Fatal("expected non-empty assistant response")
	}
}

func TestMaybeBuildWorkspacePlanningFormResponse_SkipsPlanningSubmissionPrompt(t *testing.T) {
	resp := maybeBuildWorkspacePlanningFormResponse(
		&resolvedChatAgent{Agent: &agent.Agent{Type: "workspace-manager"}},
		"Structured planning form submission:\n{\"form_id\":\"travel_intake\"}",
		normalizedChatRouteContext{WorkspaceID: "workspace-spain"},
	)
	if resp != nil {
		t.Fatalf("expected no planning form response for a structured submission prompt, got %#v", resp)
	}
}

func TestMaybeBuildWorkspacePlanningFormResponse_SkipsAfterPriorPlanningSubmission(t *testing.T) {
	resp := maybeBuildWorkspacePlanningFormResponse(
		&resolvedChatAgent{
			Agent: &agent.Agent{
				Type: "workspace-manager",
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("let's plan a trip to Spain"),
					openai.AssistantMessage("Complete the planning step below."),
					openai.UserMessage("Structured planning form submission:\n{\"form_id\":\"travel_intake\"}"),
				},
			},
		},
		"2 people, flights are booked, include Lisbon too",
		normalizedChatRouteContext{WorkspaceID: "workspace-spain"},
	)
	if resp != nil {
		t.Fatalf("expected no planning form response after prior structured submission, got %#v", resp)
	}
}
