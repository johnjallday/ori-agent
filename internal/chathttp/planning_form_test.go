package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestChatHandler_WorkspaceManagerTravelRequest_ReturnsPlanningForm(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	sessionStore := session.NewHybridStoreWithDB(db, 10)
	now := time.Now()
	if err := sessionStore.CreateWorkspace(ctx, &session.Workspace{
		ID:        "workspace-spain",
		Name:      "Spain",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to create session workspace: %v", err)
	}

	h := NewHandler(newPreflightStore("Spain Manager", &agent.Agent{Type: "general"}), nil)
	h.SetSessionStore(sessionStore)
	h.workspaceStore = &preflightWorkspaceStore{
		workspaces: map[string]*workspace.Workspace{
			"workspace-spain": {
				ID:   "workspace-spain",
				Name: "Spain",
			},
		},
	}

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
	if title, _ := form["title"].(string); title != "Collect trip details before specialist handoff" {
		t.Fatalf("expected specialist-first planning form title, got %q", title)
	}
	if subtitle, _ := form["subtitle"].(string); !strings.Contains(subtitle, "recommend the right travel specialist") {
		t.Fatalf("expected specialist guidance in subtitle, got %q", subtitle)
	}
	if submitLabel, _ := form["submit_label"].(string); submitLabel != "Review Intake And Choose Next Agent" {
		t.Fatalf("expected specialist-focused submit label, got %q", submitLabel)
	}
	if submitInstructions, _ := form["submit_instructions"].(string); !strings.Contains(submitInstructions, "recommend the right specialist handoff first") {
		t.Fatalf("expected specialist-first submit instructions, got %q", submitInstructions)
	}
	questions, ok := form["questions"].([]any)
	if !ok || len(questions) < 4 {
		t.Fatalf("expected planning form questions, got %#v", form["questions"])
	}
	if responseText, _ := resp["response"].(string); responseText == "" {
		t.Fatal("expected non-empty assistant response")
	} else if !strings.Contains(responseText, "recommend the right specialist") {
		t.Fatalf("expected specialist-first response text, got %q", responseText)
	}
}

func TestMaybeBuildWorkspacePlanningFormResponse_SkipsPlanningSubmissionPrompt(t *testing.T) {
	resp := maybeBuildWorkspacePlanningFormResponse(
		&resolvedChatAgent{Agent: &agent.Agent{}, WorkspaceTools: &WorkspaceToolProvider{}},
		"Structured planning form submission:\n{\"form_id\":\"travel_intake\"}",
		normalizedChatRouteContext{WorkspaceID: "workspace-spain"},
		nil,
		nil,
	)
	if resp != nil {
		t.Fatalf("expected no planning form response for a structured submission prompt, got %#v", resp)
	}
}

func TestMaybeBuildWorkspacePlanningFormResponse_SkipsAfterPriorPlanningSubmission(t *testing.T) {
	resp := maybeBuildWorkspacePlanningFormResponse(
		&resolvedChatAgent{
			Agent: &agent.Agent{
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("let's plan a trip to Spain"),
					openai.AssistantMessage("Complete the planning step below."),
					openai.UserMessage("Structured planning form submission:\n{\"form_id\":\"travel_intake\"}"),
				},
			},
			WorkspaceTools: &WorkspaceToolProvider{},
		},
		"2 people, flights are booked, include Lisbon too",
		normalizedChatRouteContext{WorkspaceID: "workspace-spain"},
		nil,
		nil,
	)
	if resp != nil {
		t.Fatalf("expected no planning form response after prior structured submission, got %#v", resp)
	}
}

func TestMaybeBuildWorkspacePlanningFormResponse_AllowsFreshPlanningRequestAfterPriorSubmission(t *testing.T) {
	resp := maybeBuildWorkspacePlanningFormResponse(
		&resolvedChatAgent{
			Agent: &agent.Agent{
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("let's plan a trip to Spain"),
					openai.AssistantMessage("Complete the planning step below."),
					openai.UserMessage("Structured planning form submission:\n{\"form_id\":\"travel_intake\"}"),
					openai.AssistantMessage("Thanks. I have enough to continue."),
				},
			},
			WorkspaceTools: &WorkspaceToolProvider{},
		},
		"let's plan a trip to Italy instead",
		normalizedChatRouteContext{WorkspaceID: "workspace-italy"},
		nil,
		nil,
	)
	if resp == nil || resp.Form == nil {
		t.Fatalf("expected planning form response for fresh planning request, got %#v", resp)
	}
}

func TestMaybeBuildWorkspacePlanningFormResponse_UsesWorkspaceBootstrapDates(t *testing.T) {
	wsStore := &preflightWorkspaceStore{
		workspaces: map[string]*workspace.Workspace{
			"workspace-portugal": {
				ID:   "workspace-portugal",
				Name: "Portugal",
				SharedData: map[string]any{
					"workspace_bootstrap": map[string]any{
						"goal":    "Plan 5/11 Lisbon arrival, 5/14 Porto transfer, 5/18 depart Portugal",
						"context": "May trip with a relaxed pace",
					},
				},
			},
		},
	}

	resp := maybeBuildWorkspacePlanningFormResponse(
		&resolvedChatAgent{Agent: &agent.Agent{}, WorkspaceTools: &WorkspaceToolProvider{}},
		"plan a trip in Lisbon",
		normalizedChatRouteContext{WorkspaceID: "workspace-portugal"},
		wsStore,
		nil,
	)
	if resp == nil || resp.Form == nil {
		t.Fatalf("expected planning form response, got %#v", resp)
	}

	if strings.Contains(resp.Form.Summary, "not clearly detected") {
		t.Fatalf("expected workspace bootstrap dates to avoid missing-dates summary, got %q", resp.Form.Summary)
	}
	if !strings.Contains(resp.Form.Summary, "workspace context") {
		t.Fatalf("expected summary to mention existing workspace context, got %q", resp.Form.Summary)
	}
	if !strings.Contains(resp.Form.Summary, "5/11 Lisbon arrival") {
		t.Fatalf("expected summary to include detected route details, got %q", resp.Form.Summary)
	}

	if len(resp.Form.Questions) == 0 {
		t.Fatalf("expected planning form questions")
	}
	dateQuestion := resp.Form.Questions[0]
	if dateQuestion.Required {
		t.Fatalf("expected date confirmation question to be optional, got %#v", dateQuestion)
	}
	if dateQuestion.Label != "Confirm travel dates and route" {
		t.Fatalf("expected confirm label, got %q", dateQuestion.Label)
	}
}

func TestMaybeBuildWorkspacePlanningFormResponse_UsesTripIntakeNoteDates(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	sessionStore := session.NewHybridStoreWithDB(db, 10)
	now := time.Now()
	if err := sessionStore.CreateWorkspace(ctx, &session.Workspace{
		ID:        "workspace-spain",
		Name:      "Spain",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to create session workspace: %v", err)
	}
	if err := sessionStore.CreateNote(ctx, &session.WorkspaceNote{
		ID:          "spain-trip-intake",
		WorkspaceID: "workspace-spain",
		Name:        "Spain Trip Intake",
		Content: `# Spain Trip Intake

Original request:
help me plan my trip 5/11 Lisbon Arrival 5/14 San Sebastian Arrival 5/17 Madrid Arrival 5/23 Leave Spain

Collect trip details before specialist handoff:
- Are flights already booked?: Choose one
- Are hotels already booked?: Choose one
- What pace do you want?: Choose one
- What budget level fits best?: Choose one
`,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to create trip intake note: %v", err)
	}

	wsStore := &preflightWorkspaceStore{
		workspaces: map[string]*workspace.Workspace{
			"workspace-spain": {
				ID:   "workspace-spain",
				Name: "Spain",
			},
		},
	}

	resp := maybeBuildWorkspacePlanningFormResponse(
		&resolvedChatAgent{Agent: &agent.Agent{}, WorkspaceTools: &WorkspaceToolProvider{}},
		"plan my trip",
		normalizedChatRouteContext{WorkspaceID: "workspace-spain"},
		wsStore,
		sessionStore,
	)
	if resp == nil || resp.Form == nil {
		t.Fatalf("expected planning form response, got %#v", resp)
	}
	if strings.Contains(resp.Form.Summary, "not clearly detected") {
		t.Fatalf("expected trip intake note dates to avoid missing-dates summary, got %q", resp.Form.Summary)
	}
	if !strings.Contains(resp.Form.Summary, `workspace note "Spain Trip Intake"`) {
		t.Fatalf("expected summary to mention Spain Trip Intake note, got %q", resp.Form.Summary)
	}
	if !strings.Contains(resp.Form.Summary, "5/11 Lisbon Arrival") {
		t.Fatalf("expected summary to reuse trip intake note dates, got %q", resp.Form.Summary)
	}
	if len(resp.Form.Questions) == 0 {
		t.Fatalf("expected planning form questions")
	}
	if resp.Form.Questions[0].Required {
		t.Fatalf("expected date field to be optional when trip intake note has dates, got %#v", resp.Form.Questions[0])
	}
	if !strings.Contains(resp.Form.Questions[0].HelpText, `workspace note "Spain Trip Intake"`) {
		t.Fatalf("expected date question help text to mention the trip intake note, got %q", resp.Form.Questions[0].HelpText)
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
