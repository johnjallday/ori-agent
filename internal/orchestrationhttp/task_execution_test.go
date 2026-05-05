package orchestrationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestResponseNeedsUserInput(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     bool
	}{
		{
			name: "clarification request",
			response: `I've received your task.
However, I need clarification to complete this task:
1. Which location should I check?
2. What format do you need?`,
			want: true,
		},
		{
			name: "direct answer",
			response: `Seoul weather summary:
- Temperature: 11C
- Precipitation: 10%
- Wind: 8 km/h`,
			want: false,
		},
		{
			name: "filesystem clarification disguised as answer",
			response: `It seems that the directory "/Users/jjdev/Documents" is empty or not accessible at the moment, as every attempt to list its contents results in an error.

Could you please confirm if the "DNM" folder is located somewhere else or provide additional directions?`,
			want: true,
		},
		{
			name:     "empty output",
			response: "   ",
			want:     true,
		},
		{
			name: "option menu",
			response: `Recommended next steps (choose one):
- Option A: Retry fetch now
- Option B: Provide raw HTML or a snapshot
- Option C: Use an alternative data source`,
			want: true,
		},
		{
			name: "asks what to do next after failed public source lookup",
			response: `I attempted to fetch a current pollen count for New York City using public sources, but I’m not seeing any live results available in this session.

What would you like me to do next?
- I can keep trying with more sources.
- If you have a preferred source, tell me and I’ll pull from that.

Please tell me how you'd like to proceed.`,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := responseNeedsUserInput(tt.response)
			if got != tt.want {
				t.Fatalf("responseNeedsUserInput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassifyToolAccessBlockedResponse_RobotsNetworkBlocked(t *testing.T) {
	result := `A NYC pollen data fetch attempt was made from AccuWeather, but the fetch was blocked by robots.txt / network restrictions.
No HTML content is available locally to parse.`

	blockedErr := classifyToolAccessBlockedResponse(result)
	if blockedErr == nil {
		t.Fatal("expected robots/network blocked response to be classified as blocked")
	}
	if blockedErr.ReasonCode != "tool_access_unavailable" {
		t.Fatalf("expected tool_access_unavailable reason, got %q", blockedErr.ReasonCode)
	}
}

func TestExtractClarificationWorkflowStep_ParsesOptionMenu(t *testing.T) {
	result := `Recommended next steps (choose one):
- Option A: Retry fetch now
- Option B: Provide raw HTML or a snapshot
- Option C: Use an alternative data source
- Option D: Draft with placeholders now`

	step := extractClarificationWorkflowStep(result)
	if step == nil {
		t.Fatal("expected workflow step")
	}
	if step.StepType != "ask_choice" {
		t.Fatalf("expected ask_choice step, got %q", step.StepType)
	}
	if len(step.Choices) != 4 {
		t.Fatalf("expected 4 choices, got %d: %#v", len(step.Choices), step.Choices)
	}
	if step.Choices[0].Number != "A" || step.Choices[0].Label != "Retry fetch now" {
		t.Fatalf("unexpected first choice: %#v", step.Choices[0])
	}
}

func TestExtractClarificationWorkflowStep_MarksRecommendedOption(t *testing.T) {
	result := `Recommended next steps (choose one):
- Option A: Retry fetch now
- Option B: Provide raw HTML or a snapshot

If you'd like, I can start with Option A by default.`

	step := extractClarificationWorkflowStep(result)
	if step == nil || len(step.Choices) != 2 {
		t.Fatalf("expected 2 choices, got %#v", step)
	}
	if !step.Choices[0].Recommended {
		t.Fatalf("expected first choice to be recommended: %#v", step.Choices[0])
	}
	if step.Choices[1].Recommended {
		t.Fatalf("did not expect second choice to be recommended: %#v", step.Choices[1])
	}
}

func TestClassifyInvalidTaskCompletionResponse_StatusSummaryForPublicInfo(t *testing.T) {
	task := workspace.Task{Description: "check pollen count in NYC"}
	result := `Here’s the current status for Task 026ba8ef:
- Status: in_progress
- What happened so far: fetch was blocked.
- What you’ll likely want next: retry.`

	blockedErr := classifyInvalidTaskCompletionResponse(task, result)
	if blockedErr == nil {
		t.Fatal("expected status summary to be classified as invalid completion")
	}
	if blockedErr.ReasonCode != "invalid_status_summary" {
		t.Fatalf("expected invalid_status_summary, got %q", blockedErr.ReasonCode)
	}
}

func TestClassifyInvalidTaskCompletionResponse_PlaceholderTable(t *testing.T) {
	task := workspace.Task{Description: "check pollen count in NYC"}
	result := `# NYC Pollen
| Allergen | Level |
|---|---|
| Tree Pollen | TBD |
| Grass Pollen | TBD |`

	blockedErr := classifyInvalidTaskCompletionResponse(task, result)
	if blockedErr == nil {
		t.Fatal("expected placeholder table to be classified as invalid completion")
	}
	if blockedErr.ReasonCode != "placeholder_result" {
		t.Fatalf("expected placeholder_result, got %q", blockedErr.ReasonCode)
	}
}

func TestClassifyInvalidTaskCompletionResponse_RawToolResultsForPublicInfo(t *testing.T) {
	task := workspace.Task{Description: "check pollen count in NYC"}
	result := `Tool Results:
- web_fetch: {"url":"https://www.pollen.com/forecast/current/pollen/73344?dnsz=1","title":"Current Pollen Allergy Forecast for Austin, TX (73344) | Pollen.com"}`

	blockedErr := classifyInvalidTaskCompletionResponse(task, result)
	if blockedErr == nil {
		t.Fatal("expected raw tool result to be classified as invalid completion")
	}
	if blockedErr.ReasonCode != "tool_only_result" {
		t.Fatalf("expected tool_only_result, got %q", blockedErr.ReasonCode)
	}
}

func TestClassifyInvalidTaskCompletionResponse_EmptyWebSearchResultsForPublicInfo(t *testing.T) {
	task := workspace.Task{Description: "check pollen count in NYC"}
	result := `Tool Results:
- web_search: {"query":"site:pollen.com New York pollen count","results":[],"source":"duckduckgo.com"}`

	blockedErr := classifyInvalidTaskCompletionResponse(task, result)
	if blockedErr == nil {
		t.Fatal("expected empty web search result to be classified as invalid completion")
	}
	if blockedErr.ReasonCode != "empty_web_search_results" {
		t.Fatalf("expected empty_web_search_results, got %q", blockedErr.ReasonCode)
	}
	if !strings.Contains(blockedErr.Question, "broader search") {
		t.Fatalf("expected broader-search question, got %q", blockedErr.Question)
	}
}

func TestClassifyInvalidTaskCompletionResponse_LocationMismatchForNYC(t *testing.T) {
	task := workspace.Task{Description: "check pollen count in NYC"}
	result := `Current Pollen Allergy Forecast for Austin, TX (73344) | Pollen.com
No locations found
Current Allergy Report for Austin, TX`

	blockedErr := classifyInvalidTaskCompletionResponse(task, result)
	if blockedErr == nil {
		t.Fatal("expected wrong-location result to be classified as invalid completion")
	}
	if blockedErr.ReasonCode != "location_mismatch" {
		t.Fatalf("expected location_mismatch, got %q", blockedErr.ReasonCode)
	}
}

func TestClassifyInvalidTaskCompletionResponse_ZipMismatch(t *testing.T) {
	task := workspace.Task{Description: "check pollen count for 10021"}
	result := `Current Pollen Allergy Forecast for Austin, TX (73344) | Pollen.com`

	blockedErr := classifyInvalidTaskCompletionResponse(task, result)
	if blockedErr == nil {
		t.Fatal("expected zip mismatch to be classified as invalid completion")
	}
	if blockedErr.ReasonCode != "location_mismatch" {
		t.Fatalf("expected location_mismatch, got %q", blockedErr.ReasonCode)
	}
}

func TestMarkTaskBlockedSetsWaitingForChoice(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Pollen"})
	ws.ID = "workspace-waiting-choice"
	task := workspace.Task{
		ID:          "task-waiting-choice",
		WorkspaceID: ws.ID,
		Description: "check pollen count in NYC",
		To:          "Ori",
		Status:      workspace.TaskStatusInProgress,
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}
	taskRef, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	handler := &TaskHandler{workspaceStore: store}
	blockedErr := &workspace.TaskBlockedError{
		ReasonCode: "needs_user_confirmation",
		Reason:     "Choose a source fallback.",
		Question:   "Which source should Ori try next?",
		WorkflowStep: &workspace.TaskBlockedWorkflowStep{
			StepType: "ask_choice",
			Choices: []workspace.TaskBlockedChoice{
				{ID: "retry", Label: "Retry fetch", Number: "A"},
				{ID: "alternate", Label: "Use alternate source", Number: "B"},
			},
		},
	}

	if err := handler.markTaskBlocked(ws, taskRef, blockedErr, true, nil); err != nil {
		t.Fatalf("markTaskBlocked failed: %v", err)
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	savedTask, err := savedWS.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to reload task: %v", err)
	}
	if savedTask.Status != workspace.TaskStatusWaitingForChoice {
		t.Fatalf("expected waiting_for_choice status, got %q", savedTask.Status)
	}
	humanLoop, ok := savedTask.Context["human_loop"].(map[string]any)
	if !ok {
		t.Fatalf("expected human_loop map, got %T", savedTask.Context["human_loop"])
	}
	if humanLoop["state"] != "waiting_for_choice" {
		t.Fatalf("expected waiting human loop state, got %#v", humanLoop["state"])
	}
	if humanLoop["workflow_step"] == nil {
		t.Fatal("expected workflow_step to be persisted")
	}
	workflowStep, ok := humanLoop["workflow_step"].(*workspace.TaskBlockedWorkflowStep)
	if !ok {
		t.Fatalf("expected workflow_step type, got %T", humanLoop["workflow_step"])
	}
	if len(workflowStep.Choices) != 3 {
		t.Fatalf("expected let Ori decide choice to be added, got %#v", workflowStep.Choices)
	}
	if workflowStep.Choices[2].ID != "ori_decide" {
		t.Fatalf("expected final choice to let Ori decide, got %#v", workflowStep.Choices[2])
	}
}

func TestResolveTaskExecutionAttempts(t *testing.T) {
	tests := []struct {
		name string
		task *workspace.Task
		want int
	}{
		{
			name: "default attempts",
			task: &workspace.Task{},
			want: defaultTaskExecutionAttempts,
		},
		{
			name: "string override",
			task: &workspace.Task{
				Context: map[string]any{"max_attempts": "4"},
			},
			want: 4,
		},
		{
			name: "float override",
			task: &workspace.Task{
				Context: map[string]any{"retry_attempts": 2.0},
			},
			want: 2,
		},
		{
			name: "clamped to max",
			task: &workspace.Task{
				Context: map[string]any{"execution_max_attempts": 20},
			},
			want: maxTaskExecutionAttempts,
		},
		{
			name: "invalid keeps default",
			task: &workspace.Task{
				Context: map[string]any{"max_attempts": "invalid"},
			},
			want: defaultTaskExecutionAttempts,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveTaskExecutionAttempts(tt.task)
			if got != tt.want {
				t.Fatalf("resolveTaskExecutionAttempts() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestExtractClarificationQuestion(t *testing.T) {
	response := "I need clarification.\nWhich city should I check weather for?\nPlease confirm."
	got := extractClarificationQuestion(response)
	if got != "Which city should I check weather for?" {
		t.Fatalf("extractClarificationQuestion() = %q, want %q", got, "Which city should I check weather for?")
	}
}

func TestExtractClarificationWorkflowStep_NumberedOptions(t *testing.T) {
	response := "I can continue in two ways:\n1. Save this as a note in your Spain workspace\n2. Drill into a specific day or activity"

	got := extractClarificationWorkflowStep(response)
	if got == nil {
		t.Fatalf("expected workflow step, got nil")
	}
	if got.StepType != "ask_choice" {
		t.Fatalf("expected ask_choice step, got %q", got.StepType)
	}
	if len(got.Choices) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(got.Choices))
	}
	if got.Choices[0].Label != "Save this as a note in your Spain workspace" {
		t.Fatalf("unexpected first choice label %q", got.Choices[0].Label)
	}
}

func TestExtractClarificationWorkflowStep_InlineQuestion(t *testing.T) {
	response := "Want me to save this as a note in your **spain** workspace, or drill into any specific day/activity?"

	got := extractClarificationWorkflowStep(response)
	if got == nil {
		t.Fatalf("expected workflow step, got nil")
	}
	if len(got.Choices) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(got.Choices))
	}
	if got.Choices[0].Label != "save this as a note in your spain workspace" {
		t.Fatalf("unexpected first choice label %q", got.Choices[0].Label)
	}
	if got.Choices[1].Label != "drill into any specific day/activity" {
		t.Fatalf("unexpected second choice label %q", got.Choices[1].Label)
	}
}

func TestExtractClarificationWorkflowStep_FormQuestions(t *testing.T) {
	response := "Is this going in a specific room (bathroom, kitchen, living room)? Freestanding or wall-mounted? Do you have tools already (saw, drill, square)?"

	got := extractClarificationWorkflowStep(response)
	if got == nil {
		t.Fatalf("expected workflow step, got nil")
	}
	if got.StepType != "ask_form" {
		t.Fatalf("expected ask_form step, got %q", got.StepType)
	}
	if len(got.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(got.Fields))
	}
	if got.Fields[0].Label != "Room" {
		t.Fatalf("unexpected first field label %q", got.Fields[0].Label)
	}
	if got.Fields[1].Type != "select" {
		t.Fatalf("expected second field to be select, got %q", got.Fields[1].Type)
	}
	if len(got.Fields[1].Options) != 2 {
		t.Fatalf("expected 2 mounting options, got %d", len(got.Fields[1].Options))
	}
	if got.Fields[2].Type != "textarea" {
		t.Fatalf("expected tools field to use textarea, got %q", got.Fields[2].Type)
	}
}

func TestExtractClarificationWorkflowStep_QuestionBlocksWithLetterOptions(t *testing.T) {
	response := `I can see you have shelf dimension notes in this workspace.
Let me make sure I understand the full scope before we build a plan.

A few quick questions:

1. **What's the goal of this project?**
   A. Build shelving units from scratch (cut your own lumber)
   B. Assemble pre-cut pieces
   C. Modify/repair existing shelves
   D. Something else

2. **How many shelf units are you building?**
   A. 1 unit
   B. 2 units (matches your "x2 sets" note)
   C. More than 2`

	got := extractClarificationWorkflowStep(response)
	if got == nil {
		t.Fatalf("expected workflow step, got nil")
	}
	if got.StepType != "ask_form" {
		t.Fatalf("expected ask_form step, got %q", got.StepType)
	}
	if len(got.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(got.Fields))
	}
	if got.Fields[0].Description != "What's the goal of this project?" {
		t.Fatalf("unexpected first field description %q", got.Fields[0].Description)
	}
	if got.Fields[0].Type != "select" {
		t.Fatalf("expected first field to be select, got %q", got.Fields[0].Type)
	}
	if len(got.Fields[0].Options) != 4 {
		t.Fatalf("expected 4 options for first field, got %d", len(got.Fields[0].Options))
	}
	if got.Fields[0].Options[1].Label != "Assemble pre-cut pieces" {
		t.Fatalf("unexpected second option label %q", got.Fields[0].Options[1].Label)
	}
	if len(got.Fields[1].Options) != 3 {
		t.Fatalf("expected 3 options for second field, got %d", len(got.Fields[1].Options))
	}
	if got.Fields[1].Options[1].Label != "2 units" {
		t.Fatalf("unexpected second field option %q", got.Fields[1].Options[1].Label)
	}
	if got.Fields[1].Options[1].Description != `matches your "x2 sets" note.` {
		t.Fatalf("unexpected second field option description %q", got.Fields[1].Options[1].Description)
	}
	if got.Fields[1].Evidence != `matches your "x2 sets" note.` {
		t.Fatalf("unexpected question evidence %q", got.Fields[1].Evidence)
	}
}

func TestApplyIterationContext_RequiresFilesystemVerificationAfterUnverifiedListing(t *testing.T) {
	task := &workspace.Task{
		Description: "Get list of files in DNM folder",
		Context:     map[string]any{},
	}

	history := []map[string]any{
		{
			"attempt": 1,
			"outcome": "unverified",
			"summary": "Task returned a filesystem listing answer without successful filesystem verification",
		},
	}

	applyIterationContext(task, 2, 3, history)

	guidance, _ := task.Context["execution_retry_guidance"].(string)
	if !strings.Contains(guidance, "must use a filesystem tool to verify the folder contents before answering") {
		t.Fatalf("expected retry guidance to require filesystem verification, got %q", guidance)
	}
	if requireVerification, _ := task.Context["execution_require_filesystem_verification"].(bool); !requireVerification {
		t.Fatalf("expected execution_require_filesystem_verification to be true")
	}
	requiredTools, ok := task.Context["execution_required_filesystem_tools"].([]string)
	if !ok || len(requiredTools) == 0 {
		t.Fatalf("expected execution_required_filesystem_tools to be populated, got %#v", task.Context["execution_required_filesystem_tools"])
	}
}

func TestApplyIterationContext_RequiresDirectFileListAfterIncompleteListing(t *testing.T) {
	task := &workspace.Task{
		Description: "Get list of files in DNM folder",
		Context:     map[string]any{},
	}

	history := []map[string]any{
		{
			"attempt": 1,
			"outcome": "incomplete",
			"summary": "Task did not return the requested filesystem file list",
		},
	}

	applyIterationContext(task, 2, 3, history)

	guidance, _ := task.Context["execution_retry_guidance"].(string)
	if !strings.Contains(guidance, "do not ask a follow-up question or offer to provide it later") {
		t.Fatalf("expected retry guidance to require a direct list answer, got %q", guidance)
	}
}

func TestClassifyToolAccessBlockedResponse(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     bool
	}{
		{
			name: "filesystem browsing unavailable",
			response: `I don't have filesystem browsing tools available in this context — only REAPER scripting and LSP code intelligence tools, neither of which can explore a general directory.

To walk you through the directory, I'd need you to either share the directory listing or enable filesystem access.`,
			want: true,
		},
		{
			name:     "weather tool unavailable",
			response: `I don't have access to a weather tool or real-time weather data in this environment. The available tools are limited to REAPER scripting and LSP code intelligence.`,
			want:     true,
		},
		{
			name: "successful answer",
			response: `Directory summary:
- cmd/
- internal/
- README.md`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyToolAccessBlockedResponse(tt.response)
			if (got != nil) != tt.want {
				t.Fatalf("classifyToolAccessBlockedResponse() returned %v, want blocked=%v", got, tt.want)
			}
			if tt.want {
				if got.ReasonCode != "tool_access_unavailable" {
					t.Fatalf("expected reason code tool_access_unavailable, got %q", got.ReasonCode)
				}
				if got.RawResponse != tt.response {
					t.Fatalf("expected raw response to be preserved")
				}
			}
		})
	}
}

type stubWorkspaceTaskExecutor struct {
	result string
	err    error
	calls  atomic.Int32
}

func (s *stubWorkspaceTaskExecutor) ExecuteTask(_ context.Context, _ string, _ workspace.Task) (string, error) {
	s.calls.Add(1)
	return s.result, s.err
}

type stubSequenceTaskExecutor struct {
	results []string
	err     error
	calls   int
}

func (s *stubSequenceTaskExecutor) ExecuteTask(_ context.Context, _ string, task workspace.Task) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	if len(s.results) == 0 {
		return task.Description, nil
	}
	index := s.calls - 1
	if index < 0 {
		index = 0
	}
	if index >= len(s.results) {
		index = len(s.results) - 1
	}
	return s.results[index], nil
}

type stubMappedTaskExecutor struct {
	results map[string]string
	errs    map[string]error
	calls   []string
}

func (s *stubMappedTaskExecutor) ExecuteTask(_ context.Context, _ string, task workspace.Task) (string, error) {
	s.calls = append(s.calls, task.ID)
	if err := s.errs[task.ID]; err != nil {
		return "", err
	}
	if result, ok := s.results[task.ID]; ok {
		return result, nil
	}
	return task.Description, nil
}

type stubCancellableTaskExecutor struct {
	started chan struct{}
}

func (s *stubCancellableTaskExecutor) ExecuteTask(ctx context.Context, _ string, _ workspace.Task) (string, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return "", ctx.Err()
}

type stubEventingTaskExecutor struct {
	result     string
	err        error
	calls      int
	eventBus   *workspace.EventBus
	toolName   string
	toolResult string
	success    bool
}

func (s *stubEventingTaskExecutor) ExecuteTask(_ context.Context, agentName string, task workspace.Task) (string, error) {
	s.calls++
	if s.eventBus != nil && strings.TrimSpace(s.toolName) != "" {
		data := map[string]any{
			"tool_name": s.toolName,
			"success":   s.success,
		}
		if strings.TrimSpace(s.toolResult) != "" {
			data["result_preview"] = s.toolResult
		}
		s.eventBus.Publish(workspace.NewTaskEvent(workspace.EventTaskToolResult, task.WorkspaceID, task.ID, agentName, data))
	}
	return s.result, s.err
}

type stubEventingStep struct {
	result     string
	err        error
	toolName   string
	toolResult string
	success    bool
}

type stubSequenceEventingTaskExecutor struct {
	steps    []stubEventingStep
	calls    int
	eventBus *workspace.EventBus
}

func (s *stubSequenceEventingTaskExecutor) ExecuteTask(_ context.Context, agentName string, task workspace.Task) (string, error) {
	s.calls++
	if len(s.steps) == 0 {
		return "", nil
	}

	index := s.calls - 1
	if index < 0 {
		index = 0
	}
	if index >= len(s.steps) {
		index = len(s.steps) - 1
	}
	step := s.steps[index]

	if s.eventBus != nil && strings.TrimSpace(step.toolName) != "" {
		data := map[string]any{
			"tool_name": step.toolName,
			"success":   step.success,
		}
		if strings.TrimSpace(step.toolResult) != "" {
			data["result_preview"] = step.toolResult
		}
		s.eventBus.Publish(workspace.NewTaskEvent(workspace.EventTaskToolResult, task.WorkspaceID, task.ID, agentName, data))
	}

	return step.result, step.err
}

func TestExecuteTaskIteratively_BlocksWhenToolsAreUnavailable(t *testing.T) {
	stub := &stubWorkspaceTaskExecutor{
		result: `I don't have filesystem browsing tools available in this context — only REAPER scripting and LSP code intelligence tools, neither of which can explore a general directory.

To walk you through the directory, I'd need you to either share the directory listing or enable filesystem access.`,
	}
	handler := &TaskHandler{taskHandler: stub}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:          "task-1",
		WorkspaceID: ws.ID,
		To:          "Ori",
		Description: "walk me through the amr directory",
		Context:     map[string]any{},
	}

	_, err := handler.executeTaskIteratively(context.Background(), ws, &task, task, true)
	blockedErr, ok := workspace.AsTaskBlockedError(err)
	if !ok {
		t.Fatalf("expected TaskBlockedError, got %v", err)
	}
	if blockedErr.ReasonCode != "tool_access_unavailable" {
		t.Fatalf("expected tool_access_unavailable, got %q", blockedErr.ReasonCode)
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("expected a single execution attempt, got %d", stub.calls.Load())
	}

	retryData, ok := task.Context["execution_retry"].(map[string]any)
	if !ok {
		t.Fatalf("expected execution_retry context to be recorded")
	}
	if got := retryData["final_outcome"]; got != "blocked" {
		t.Fatalf("expected final_outcome blocked, got %v", got)
	}
	if got := retryData["attempts_used"]; got != 1 {
		t.Fatalf("expected attempts_used 1, got %v", got)
	}
}

func TestExecuteTaskIteratively_BlocksUnverifiedFilesystemListingResult(t *testing.T) {
	eventBus := workspace.DefaultEventBus()
	stub := &stubEventingTaskExecutor{
		result:   `The "Documents" directory exists, but it does not contain a folder named "DNM".`,
		eventBus: eventBus,
	}
	handler := &TaskHandler{
		taskHandler: stub,
		eventBus:    eventBus,
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:          "task-listing-unverified",
		WorkspaceID: ws.ID,
		To:          "Ori",
		Description: "Give me list of files in DNM folder",
		Context: map[string]any{
			"max_attempts": 1,
		},
	}

	_, err := handler.executeTaskIteratively(context.Background(), ws, &task, task, true)
	blockedErr, ok := workspace.AsTaskBlockedError(err)
	if !ok {
		t.Fatalf("expected TaskBlockedError, got %v", err)
	}
	if blockedErr.ReasonCode != "filesystem_result_unverified" {
		t.Fatalf("expected filesystem_result_unverified, got %q", blockedErr.ReasonCode)
	}
	if stub.calls != 1 {
		t.Fatalf("expected a single execution attempt, got %d", stub.calls)
	}
}

func TestExecuteTaskIteratively_AllowsVerifiedFilesystemListingResult(t *testing.T) {
	eventBus := workspace.DefaultEventBus()
	stub := &stubEventingTaskExecutor{
		result:     "The DNM folder contains:\n- file-a.pdf\n- file-b.pages",
		eventBus:   eventBus,
		toolName:   "list_directory",
		toolResult: "file-a.pdf\nfile-b.pages",
		success:    true,
	}
	handler := &TaskHandler{
		taskHandler: stub,
		eventBus:    eventBus,
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:          "task-listing-verified",
		WorkspaceID: ws.ID,
		To:          "Ori",
		Description: "Give me list of files in DNM folder",
		Context: map[string]any{
			"max_attempts": 1,
		},
	}

	result, err := handler.executeTaskIteratively(context.Background(), ws, &task, task, true)
	if err != nil {
		t.Fatalf("expected verified listing result to succeed, got %v", err)
	}
	if result == "" {
		t.Fatalf("expected a non-empty listing result")
	}
	if stub.calls != 1 {
		t.Fatalf("expected a single execution attempt, got %d", stub.calls)
	}
}

func TestExecuteTaskIteratively_RetriesIncompleteFilesystemListingAnswer(t *testing.T) {
	eventBus := workspace.DefaultEventBus()
	stub := &stubSequenceEventingTaskExecutor{
		eventBus: eventBus,
		steps: []stubEventingStep{
			{
				result:     `The "DNM" folder is located within the "Documents" directory. Would you like to see the contents of the "DNM" folder?`,
				toolName:   "list_directory",
				toolResult: "DNM Publishing Agreement - Don't Kill the Buzz.pdf",
				success:    true,
			},
			{
				result:     "The DNM folder contains:\n1. DNM Publishing Agreement - Don't Kill the Buzz.pdf",
				toolName:   "list_directory",
				toolResult: "DNM Publishing Agreement - Don't Kill the Buzz.pdf",
				success:    true,
			},
		},
	}
	handler := &TaskHandler{
		taskHandler: stub,
		eventBus:    eventBus,
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:          "task-listing-incomplete",
		WorkspaceID: ws.ID,
		To:          "Ori",
		Description: "Get list of files in DNM folder",
		Context: map[string]any{
			"max_attempts": 2,
		},
	}

	result, err := handler.executeTaskIteratively(context.Background(), ws, &task, task, true)
	if err != nil {
		t.Fatalf("expected listing task to succeed after retry, got %v", err)
	}
	if !strings.Contains(result, "Don't Kill the Buzz.pdf") {
		t.Fatalf("expected final result to contain the verified file list, got %q", result)
	}
	if stub.calls != 2 {
		t.Fatalf("expected two execution attempts, got %d", stub.calls)
	}

	retryData, ok := task.Context["execution_retry"].(map[string]any)
	if !ok {
		t.Fatalf("expected execution_retry context to be recorded")
	}
	if got := retryData["final_outcome"]; got != "success" {
		t.Fatalf("expected final_outcome success, got %v", got)
	}
}

func TestExecuteTaskIteratively_RetriesInvalidStructuredOutput(t *testing.T) {
	stub := &stubSequenceTaskExecutor{
		results: []string{
			"not-json",
			`{"summary":"ready","confidence":0.82}`,
		},
	}
	handler := &TaskHandler{taskHandler: stub}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:          "task-structured-retry",
		WorkspaceID: ws.ID,
		To:          "Ori",
		Description: "Return a release summary",
		Context: map[string]any{
			"max_attempts": 2,
		},
		OutputSchema: &workspace.TaskOutputSchema{
			Name:   "release_summary",
			Strict: true,
			Fields: []workspace.TaskOutputField{
				{Name: "summary", Type: "string", Required: true},
				{Name: "confidence", Type: "number", Required: true},
			},
		},
	}

	result, err := handler.executeTaskIteratively(context.Background(), ws, &task, task, true)
	if err != nil {
		t.Fatalf("expected structured retry to succeed, got %v", err)
	}
	if result != `{"summary":"ready","confidence":0.82}` {
		t.Fatalf("unexpected final result %q", result)
	}
	if stub.calls != 2 {
		t.Fatalf("expected two attempts, got %d", stub.calls)
	}
	structuredOutput, ok := task.Context["structured_output"].(map[string]any)
	if !ok {
		t.Fatalf("expected parsed structured output in task context, got %#v", task.Context["structured_output"])
	}
	if got := structuredOutput["summary"]; got != "ready" {
		t.Fatalf("expected summary field to persist, got %v", got)
	}
}

func TestExecuteTaskIteratively_BlocksWhenStructuredOutputRemainsInvalid(t *testing.T) {
	stub := &stubWorkspaceTaskExecutor{result: "still not json"}
	handler := &TaskHandler{taskHandler: stub}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:          "task-structured-blocked",
		WorkspaceID: ws.ID,
		To:          "Ori",
		Description: "Return a release summary",
		Context: map[string]any{
			"max_attempts": 1,
		},
		OutputSchema: &workspace.TaskOutputSchema{
			Fields: []workspace.TaskOutputField{
				{Name: "summary", Type: "string", Required: true},
			},
		},
	}

	_, err := handler.executeTaskIteratively(context.Background(), ws, &task, task, true)
	blockedErr, ok := workspace.AsTaskBlockedError(err)
	if !ok {
		t.Fatalf("expected TaskBlockedError, got %v", err)
	}
	if blockedErr.ReasonCode != "structured_output_invalid" {
		t.Fatalf("expected structured_output_invalid, got %q", blockedErr.ReasonCode)
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("expected a single execution attempt, got %d", stub.calls.Load())
	}
}

func TestExecuteParentTaskSequence_UsesGraphExecutionAndStructuredAggregation(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	parent := workspace.Task{
		ID:                    "parent-graph",
		WorkspaceID:           ws.ID,
		To:                    "Ori",
		Description:           "Execute template",
		OrchestrationMode:     workspace.TaskOrchestrationModeGraph,
		ResultCombinationMode: workspace.TaskResultCombinationStructuredOutput,
	}
	stepSchema := &workspace.TaskOutputSchema{
		Strict: true,
		Fields: []workspace.TaskOutputField{
			{Name: "summary", Type: "string", Required: true},
		},
	}
	steps := []workspace.Task{
		{ID: "step-a", WorkspaceID: ws.ID, To: "Ori", Description: "Collect", ParentTaskID: parent.ID, SubtaskIndex: 1, OutputSchema: stepSchema},
		{ID: "step-b", WorkspaceID: ws.ID, To: "Ori", Description: "Analyze", ParentTaskID: parent.ID, SubtaskIndex: 2, InputTaskIDs: []string{"step-a"}, OutputSchema: stepSchema},
		{ID: "step-c", WorkspaceID: ws.ID, To: "Ori", Description: "Validate", ParentTaskID: parent.ID, SubtaskIndex: 3, InputTaskIDs: []string{"step-a"}, OutputSchema: stepSchema},
		{ID: "step-d", WorkspaceID: ws.ID, To: "Ori", Description: "Decide", ParentTaskID: parent.ID, SubtaskIndex: 4, InputTaskIDs: []string{"step-b", "step-c"}, OutputSchema: stepSchema},
	}

	if err := ws.AddTask(parent); err != nil {
		t.Fatalf("failed to add parent task: %v", err)
	}
	for _, step := range steps {
		if err := ws.AddTask(step); err != nil {
			t.Fatalf("failed to add subtask %s: %v", step.ID, err)
		}
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	executor := &stubMappedTaskExecutor{
		results: map[string]string{
			"step-a": `{"summary":"research complete"}`,
			"step-b": `{"summary":"analysis complete"}`,
			"step-c": `{"summary":"validation complete"}`,
			"step-d": `{"summary":"decision complete"}`,
		},
	}
	handler := &TaskHandler{
		workspaceStore: store,
		taskHandler:    executor,
	}

	handler.executeParentTaskSequence(ws.ID, parent.ID)

	updatedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	updatedParent, err := updatedWS.GetTask(parent.ID)
	if err != nil {
		t.Fatalf("failed to reload parent task: %v", err)
	}
	if updatedParent.Status != workspace.TaskStatusCompleted {
		t.Fatalf("expected completed parent task, got %q", updatedParent.Status)
	}
	if got, want := strings.Join(executor.calls, ","), "step-a,step-b,step-c,step-d"; got != want {
		t.Fatalf("unexpected graph execution order %q, want %q", got, want)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(updatedParent.Result), &payload); err != nil {
		t.Fatalf("expected structured parent result, got %v", err)
	}
	finalOutputs, ok := payload["final_step_outputs"].([]any)
	if !ok {
		t.Fatalf("expected final_step_outputs array, got %#v", payload["final_step_outputs"])
	}
	if len(finalOutputs) != 4 {
		t.Fatalf("expected 4 final step outputs, got %d", len(finalOutputs))
	}
}

func TestExecuteTaskWithDependencies_RecordsSuccessfulRunHistory(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:          "task-success",
		To:          "Ori",
		Description: "summarize workspace",
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	persistedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch task: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		taskHandler: &stubWorkspaceTaskExecutor{
			result: "Workspace summary result",
		},
	}

	if _, err := handler.executeTaskWithDependencies(ws, persistedTask, true); err != nil {
		t.Fatalf("executeTaskWithDependencies failed: %v", err)
	}

	updatedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated task: %v", err)
	}
	if updatedTask.ExecutionCount != 1 {
		t.Fatalf("expected execution count 1, got %d", updatedTask.ExecutionCount)
	}
	if len(updatedTask.ExecutionHistory) != 1 {
		t.Fatalf("expected 1 execution history entry, got %d", len(updatedTask.ExecutionHistory))
	}
	if updatedTask.ExecutionHistory[0].Status != "success" {
		t.Fatalf("expected success history status, got %q", updatedTask.ExecutionHistory[0].Status)
	}
	if updatedTask.ExecutionHistory[0].Summary == "" {
		t.Fatalf("expected summary to be recorded")
	}
}

func TestExecuteTaskWithDependencies_CancelledRunStaysCancelled(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:          "task-cancel",
		To:          "Ori",
		Description: "run a long step",
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	persistedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch task: %v", err)
	}

	executor := &stubCancellableTaskExecutor{started: make(chan struct{}, 1)}
	handler := &TaskHandler{
		workspaceStore: store,
		taskHandler:    executor,
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := handler.executeTaskWithDependencies(ws, persistedTask, true)
		errCh <- err
	}()

	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for task execution to start")
	}

	if !handler.cancelRunningTask(task.ID) {
		t.Fatal("expected running task cancellation to be registered")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for cancelled task to finish")
	}

	updatedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated task: %v", err)
	}
	if updatedTask.Status != workspace.TaskStatusCancelled {
		t.Fatalf("expected cancelled task status, got %q", updatedTask.Status)
	}
	if updatedTask.Error != "Cancelled by user" {
		t.Fatalf("expected cancellation error, got %q", updatedTask.Error)
	}
	if updatedTask.ExecutionCount != 1 {
		t.Fatalf("expected execution count 1, got %d", updatedTask.ExecutionCount)
	}
	if len(updatedTask.ExecutionHistory) != 1 {
		t.Fatalf("expected 1 execution history entry, got %d", len(updatedTask.ExecutionHistory))
	}
	if updatedTask.ExecutionHistory[0].Status != "cancelled" {
		t.Fatalf("expected cancelled history status, got %q", updatedTask.ExecutionHistory[0].Status)
	}
}

func TestExecuteTaskWithDependencies_RecordsBlockedRunHistory(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:          "task-blocked",
		To:          "Ori",
		Description: "walk me through the amr directory",
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	persistedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch task: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		taskHandler: &stubWorkspaceTaskExecutor{
			err: &workspace.TaskBlockedError{
				ReasonCode:  "tool_access_unavailable",
				Reason:      "Missing filesystem tools",
				RawResponse: "I don't have filesystem access for this task.",
			},
		},
	}

	if _, err := handler.executeTaskWithDependencies(ws, persistedTask, true); err == nil {
		t.Fatalf("expected blocked error")
	}

	updatedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated task: %v", err)
	}
	if updatedTask.ExecutionCount != 1 {
		t.Fatalf("expected execution count 1, got %d", updatedTask.ExecutionCount)
	}
	if len(updatedTask.ExecutionHistory) != 1 {
		t.Fatalf("expected 1 execution history entry, got %d", len(updatedTask.ExecutionHistory))
	}
	if updatedTask.ExecutionHistory[0].Status != "blocked" {
		t.Fatalf("expected blocked history status, got %q", updatedTask.ExecutionHistory[0].Status)
	}
	if updatedTask.ExecutionHistory[0].Summary == "" {
		t.Fatalf("expected blocked summary to be recorded")
	}
}

func TestExecuteTaskWithDependencies_StepThroughPausesAfterFirstStructuredStep(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Folder Organizer", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:            "task-step-through",
		To:            "Ori",
		Description:   "Gather DNM related files into DNM folder",
		ExecutionMode: workspace.TaskExecutionModeStepThrough,
		Context:       map[string]any{},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	persistedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch task: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		taskHandler: &stubSequenceTaskExecutor{
			results: []string{"Allowed directories: /Users/jjdev/Documents"},
		},
	}

	if _, err := handler.executeTaskWithDependencies(ws, persistedTask, true); err != nil {
		t.Fatalf("executeTaskWithDependencies failed: %v", err)
	}

	updatedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated task: %v", err)
	}
	if updatedTask.Status != workspace.TaskStatusInProgress {
		t.Fatalf("expected task to remain in progress, got %q", updatedTask.Status)
	}
	if !workspace.IsTaskAwaitingNextStep(updatedTask) {
		t.Fatalf("expected task to wait for the next step")
	}
	if len(updatedTask.ExecutionSteps) != 7 {
		t.Fatalf("expected 7 execution steps, got %d", len(updatedTask.ExecutionSteps))
	}
	if updatedTask.ExecutionSteps[0].Status != workspace.TaskExecutionStepCompleted {
		t.Fatalf("expected first step completed, got %q", updatedTask.ExecutionSteps[0].Status)
	}
	if updatedTask.ExecutionSteps[1].Status != workspace.TaskExecutionStepPending {
		t.Fatalf("expected second step pending, got %q", updatedTask.ExecutionSteps[1].Status)
	}
	if updatedTask.ExecutionCount != 0 {
		t.Fatalf("expected no terminal execution history entry yet, got %d", updatedTask.ExecutionCount)
	}
}

func TestExecuteTaskWithDependencies_CompletesStaleListingPlanFromExistingResult(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Folder Organizer", Agents: []string{"Ori"}})

	staleSteps := workspace.InferTaskExecutionSteps(workspace.Task{Description: "Gather DNM related files into DNM folder"})
	if len(staleSteps) != 7 {
		t.Fatalf("expected stale mutation plan to contain 7 steps, got %d", len(staleSteps))
	}
	staleResult := "The DNM folder contains:\n- file-a.pdf\n- file-b.pages"
	for i := 0; i < 3; i++ {
		staleSteps[i].Status = workspace.TaskExecutionStepCompleted
		staleSteps[i].Result = staleResult
	}

	task := workspace.Task{
		ID:            "task-stale-listing-plan",
		To:            "Ori",
		Description:   "Give me list of files in DNM folder",
		ExecutionMode: workspace.TaskExecutionModeStepThrough,
		Context: map[string]any{
			"execution_step_waiting":       true,
			"execution_step_waiting_index": 4,
		},
		ExecutionSteps: staleSteps,
		Status:         workspace.TaskStatusInProgress,
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	persistedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch task: %v", err)
	}

	stub := &stubSequenceTaskExecutor{
		results: []string{"this executor should not be called"},
	}
	handler := &TaskHandler{
		workspaceStore: store,
		taskHandler:    stub,
	}

	result, err := handler.executeTaskWithDependencies(ws, persistedTask, true)
	if err != nil {
		t.Fatalf("executeTaskWithDependencies failed: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("expected stale listing task not to re-execute, got %d calls", stub.calls)
	}
	if result != staleResult {
		t.Fatalf("unexpected result: %q", result)
	}

	updatedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated task: %v", err)
	}
	if updatedTask.Status != workspace.TaskStatusCompleted {
		t.Fatalf("expected task completed, got %q", updatedTask.Status)
	}
	if workspace.IsTaskAwaitingNextStep(updatedTask) {
		t.Fatalf("did not expect task to remain waiting for a next step")
	}
	for i := 3; i < len(updatedTask.ExecutionSteps); i++ {
		if updatedTask.ExecutionSteps[i].Status != workspace.TaskExecutionStepSkipped {
			t.Fatalf("expected stale step %d skipped, got %q", i+1, updatedTask.ExecutionSteps[i].Status)
		}
	}
}

func TestExecuteTaskWithDependencies_AutoRunsStructuredStepsToCompletion(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Folder Organizer", Agents: []string{"Ori"}})
	task := workspace.Task{
		ID:            "task-auto-steps",
		To:            "Ori",
		Description:   "Gather DNM related files into DNM folder",
		ExecutionMode: workspace.TaskExecutionModeAuto,
		Context:       map[string]any{},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	persistedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch task: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		taskHandler: &stubSequenceTaskExecutor{
			results: []string{
				"Allowed directories: /Users/jjdev/Documents",
				"Inspected candidate directories.",
				"Identified matching DNM files.",
				"Created DNM folder.",
				"Moved matching files into DNM.",
				"Verified final folder contents.",
				"Moved 3 files into /Users/jjdev/Documents/DNM",
			},
		},
	}

	if _, err := handler.executeTaskWithDependencies(ws, persistedTask, true); err != nil {
		t.Fatalf("executeTaskWithDependencies failed: %v", err)
	}

	updatedTask, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated task: %v", err)
	}
	if updatedTask.Status != workspace.TaskStatusCompleted {
		t.Fatalf("expected task completed, got %q", updatedTask.Status)
	}
	if workspace.IsTaskAwaitingNextStep(updatedTask) {
		t.Fatalf("did not expect task to wait for the next step")
	}
	if len(updatedTask.ExecutionSteps) != 7 {
		t.Fatalf("expected 7 execution steps, got %d", len(updatedTask.ExecutionSteps))
	}
	for index, step := range updatedTask.ExecutionSteps {
		if step.Status != workspace.TaskExecutionStepCompleted {
			t.Fatalf("expected step %d completed, got %q", index+1, step.Status)
		}
	}
	if updatedTask.Result != "Moved 3 files into /Users/jjdev/Documents/DNM" {
		t.Fatalf("unexpected final result: %q", updatedTask.Result)
	}
	if updatedTask.ExecutionCount != 1 {
		t.Fatalf("expected one completed execution, got %d", updatedTask.ExecutionCount)
	}
}
