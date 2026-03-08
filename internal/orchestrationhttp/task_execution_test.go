package orchestrationhttp

import (
	"context"
	"testing"

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
			name:     "empty output",
			response: "   ",
			want:     true,
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
				Context: map[string]interface{}{"max_attempts": "4"},
			},
			want: 4,
		},
		{
			name: "float override",
			task: &workspace.Task{
				Context: map[string]interface{}{"retry_attempts": 2.0},
			},
			want: 2,
		},
		{
			name: "clamped to max",
			task: &workspace.Task{
				Context: map[string]interface{}{"execution_max_attempts": 20},
			},
			want: maxTaskExecutionAttempts,
		},
		{
			name: "invalid keeps default",
			task: &workspace.Task{
				Context: map[string]interface{}{"max_attempts": "invalid"},
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
	calls  int
}

func (s *stubWorkspaceTaskExecutor) ExecuteTask(_ context.Context, _ string, _ workspace.Task) (string, error) {
	s.calls++
	return s.result, s.err
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
		Context:     map[string]interface{}{},
	}

	_, err := handler.executeTaskIteratively(context.Background(), ws, &task, task, true)
	blockedErr, ok := workspace.AsTaskBlockedError(err)
	if !ok {
		t.Fatalf("expected TaskBlockedError, got %v", err)
	}
	if blockedErr.ReasonCode != "tool_access_unavailable" {
		t.Fatalf("expected tool_access_unavailable, got %q", blockedErr.ReasonCode)
	}
	if stub.calls != 1 {
		t.Fatalf("expected a single execution attempt, got %d", stub.calls)
	}

	retryData, ok := task.Context["execution_retry"].(map[string]interface{})
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
