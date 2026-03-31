package chathttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	workflowStep, ok := resp["workflow_step"].(map[string]any)
	if !ok {
		t.Fatalf("expected workflow_step object, got %T", resp["workflow_step"])
	}
	if stepType, _ := workflowStep["step_type"].(string); stepType != string(WorkflowStepAskForm) {
		t.Fatalf("expected ask_form workflow step, got %q", stepType)
	}
	if _, ok := workflowStep["form"].(map[string]any); !ok {
		t.Fatalf("expected workflow step form object, got %T", workflowStep["form"])
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

func TestBuildPromptFromWorkflowResponse_Form(t *testing.T) {
	prompt, err := buildPromptFromWorkflowResponse(&WorkflowUserResponse{
		WorkflowID:   "workflow:test",
		StepID:       "step:travel_intake",
		ResponseType: WorkflowResponseForm,
		Form: &WorkflowFormData{
			FormID:          "travel_intake",
			FormKind:        "travel_intake",
			FormTitle:       "Collect trip details before handoff",
			OriginalRequest: "help me plan a trip to Spain",
			Answers: []WorkflowFormAnswer{
				{
					ID:           "budget",
					Label:        "What budget level fits best?",
					Type:         "select",
					Value:        "mid_range",
					DisplayValue: "Mid-range",
					Required:     true,
				},
			},
			Attachments: []WorkflowFormAttachment{
				{
					ID:                "flight_confirmation",
					Label:             "Attach flight confirmation",
					AttachmentKind:    "flight",
					UploadModalOpened: true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected prompt build to succeed, got %v", err)
	}
	if want := "Structured planning form submission:"; !strings.HasPrefix(prompt, want) {
		t.Fatalf("expected prompt prefix %q, got %q", want, prompt)
	}
	if !bytes.Contains([]byte(prompt), []byte(`"form_id": "travel_intake"`)) {
		t.Fatalf("expected prompt to include form_id, got %q", prompt)
	}
	if !bytes.Contains([]byte(prompt), []byte(`"display_value": "Mid-range"`)) {
		t.Fatalf("expected prompt to include display value, got %q", prompt)
	}
}

func TestBuildPromptFromWorkflowResponse_Choice(t *testing.T) {
	prompt, err := buildPromptFromWorkflowResponse(&WorkflowUserResponse{
		WorkflowID:   "workflow:test",
		StepID:       "step:inline-choice",
		ResponseType: WorkflowResponseChoice,
		ChoiceID:     "choice_3_save_outline",
		ChoiceLabel:  "Save this outline as a note",
		ChoiceNumber: "3",
		Text:         "Please keep it budget friendly.",
	})
	if err != nil {
		t.Fatalf("expected choice prompt build to succeed, got %v", err)
	}
	if want := "Structured workflow choice selection:"; !strings.HasPrefix(prompt, want) {
		t.Fatalf("expected prompt prefix %q, got %q", want, prompt)
	}
	if !bytes.Contains([]byte(prompt), []byte(`"choice_label": "Save this outline as a note"`)) {
		t.Fatalf("expected prompt to include choice_label, got %q", prompt)
	}
	if !bytes.Contains([]byte(prompt), []byte(`"choice_number": "3"`)) {
		t.Fatalf("expected prompt to include choice_number, got %q", prompt)
	}
}
