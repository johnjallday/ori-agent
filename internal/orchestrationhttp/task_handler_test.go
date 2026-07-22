package orchestrationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestSubstituteInputPlaceholders(t *testing.T) {
	tests := []struct {
		name        string
		description string
		inputs      []string
		expected    string
	}{
		{
			name:        "Single numbered placeholder",
			description: "{input1} * 2",
			inputs:      []string{"4"},
			expected:    "4 * 2",
		},
		{
			name:        "Multiple numbered placeholders",
			description: "{input1} + {input2}",
			inputs:      []string{"4", "8"},
			expected:    "4 + 8",
		},
		{
			name:        "Previous shortcut",
			description: "multiply {previous} by 3",
			inputs:      []string{"5"},
			expected:    "multiply 5 by 3",
		},
		{
			name:        "Result shortcut",
			description: "{result} is the answer",
			inputs:      []string{"42"},
			expected:    "42 is the answer",
		},
		{
			name:        "No placeholders",
			description: "just text",
			inputs:      []string{"4"},
			expected:    "just text",
		},
		{
			name:        "No inputs",
			description: "{input1} test",
			inputs:      []string{},
			expected:    "{input1} test",
		},
		{
			name:        "Mixed placeholders",
			description: "Take {previous} and add {input2}",
			inputs:      []string{"10", "5"},
			expected:    "Take 10 and add 5",
		},
		{
			name:        "Multiple occurrences of same placeholder",
			description: "{input1} + {input1} equals double {input1}",
			inputs:      []string{"7"},
			expected:    "7 + 7 equals double 7",
		},
		{
			name:        "Three inputs",
			description: "{input1}, {input2}, and {input3}",
			inputs:      []string{"first", "second", "third"},
			expected:    "first, second, and third",
		},
		{
			name:        "Natural language with context",
			description: "What is the population of that city?",
			inputs:      []string{"Paris"},
			expected:    "What is the population of that city?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := substituteInputPlaceholders(tt.description, tt.inputs)
			if result != tt.expected {
				t.Errorf("substituteInputPlaceholders() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSubstituteInputPlaceholders_EdgeCases(t *testing.T) {
	t.Run("Empty description", func(t *testing.T) {
		result := substituteInputPlaceholders("", []string{"test"})
		if result != "" {
			t.Errorf("Expected empty string, got %q", result)
		}
	})

	t.Run("Placeholder with no matching input", func(t *testing.T) {
		result := substituteInputPlaceholders("{input5}", []string{"1", "2"})
		if result != "{input5}" {
			t.Errorf("Expected unchanged placeholder, got %q", result)
		}
	})

	t.Run("Both shortcuts with same input", func(t *testing.T) {
		result := substituteInputPlaceholders("{previous} and {result}", []string{"same"})
		expected := "same and same"
		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("Numeric input values", func(t *testing.T) {
		result := substituteInputPlaceholders("{input1} + {input2} = ?", []string{"3", "7"})
		expected := "3 + 7 = ?"
		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("Input with special characters", func(t *testing.T) {
		result := substituteInputPlaceholders("Result: {input1}", []string{"$100.50"})
		expected := "Result: $100.50"
		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("Input with curly braces", func(t *testing.T) {
		result := substituteInputPlaceholders("{input1}", []string{"{nested}"})
		expected := "{nested}"
		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("Long input text", func(t *testing.T) {
		longText := "This is a very long text that represents a complete task result with multiple sentences and detailed information."
		result := substituteInputPlaceholders("Summary: {input1}", []string{longText})
		expected := "Summary: " + longText
		if result != expected {
			t.Errorf("Long text substitution failed")
		}
	})
}

func TestApplyBasicFieldUpdates_KanbanColumnID(t *testing.T) {
	h := &TaskHandler{}

	task := workspace.Task{ID: "task-1"}

	col := "in_progress"
	h.applyBasicFieldUpdates(&task, &taskUpdateRequest{TaskID: task.ID, KanbanColumnID: &col})

	if task.Context == nil {
		t.Fatalf("expected context to be initialized")
	}
	got, ok := task.Context["kanban_column_id"]
	if !ok {
		t.Fatalf("expected kanban_column_id to be set")
	}
	if got != "in_progress" {
		t.Fatalf("expected kanban_column_id to be 'in_progress', got %v", got)
	}

	clear := ""
	h.applyBasicFieldUpdates(&task, &taskUpdateRequest{TaskID: task.ID, KanbanColumnID: &clear})
	if _, ok := task.Context["kanban_column_id"]; ok {
		t.Fatalf("expected kanban_column_id to be cleared")
	}
}

func TestApplyBasicFieldUpdates_KanbanMetadata(t *testing.T) {
	h := &TaskHandler{}

	task := workspace.Task{ID: "task-1"}

	labels := []string{"frontend", "ux"}
	due := "2025-01-01"
	h.applyBasicFieldUpdates(&task, &taskUpdateRequest{TaskID: task.ID, KanbanLabels: labels, KanbanDueDate: &due})

	if task.Context == nil {
		t.Fatalf("expected context to be initialized")
	}
	if got, ok := task.Context["kanban_labels"]; !ok {
		t.Fatalf("expected kanban_labels to be set")
	} else {
		gotSlice, ok := got.([]string)
		if !ok {
			t.Fatalf("expected kanban_labels to be []string, got %T", got)
		}
		if len(gotSlice) != 2 {
			t.Fatalf("expected 2 labels, got %d", len(gotSlice))
		}
	}
	if got, ok := task.Context["kanban_due_date"]; !ok {
		t.Fatalf("expected kanban_due_date to be set")
	} else if got != "2025-01-01" {
		t.Fatalf("expected kanban_due_date to be '2025-01-01', got %v", got)
	}

	clearLabels := []string{}
	clearDue := ""
	h.applyBasicFieldUpdates(&task, &taskUpdateRequest{TaskID: task.ID, KanbanLabels: clearLabels, KanbanDueDate: &clearDue})
	if _, ok := task.Context["kanban_labels"]; ok {
		t.Fatalf("expected kanban_labels to be cleared")
	}
	if _, ok := task.Context["kanban_due_date"]; ok {
		t.Fatalf("expected kanban_due_date to be cleared")
	}
}

func TestApplyBasicFieldUpdates_OrchestrationFields(t *testing.T) {
	h := &TaskHandler{}

	task := workspace.Task{ID: "task-graph"}
	orchestrationMode := "graph"
	combinationMode := "structured_outputs"
	combinationInstruction := "Combine child outputs into a release decision."
	outputSchema := &workspace.TaskOutputSchema{
		Name:        "release_decision",
		Description: "Final release recommendation",
		Strict:      true,
		Fields: []workspace.TaskOutputField{
			{Name: "decision", Type: "string", Required: true},
			{Name: "confidence", Type: "number", Required: true},
		},
	}
	templateRef := &workspace.TaskTemplateRef{
		TemplateID:   "template-1",
		TemplateName: "Release Review",
		StepID:       "step-1",
		StepName:     "Assess",
	}

	h.applyBasicFieldUpdates(&task, &taskUpdateRequest{
		TaskID:                 task.ID,
		OrchestrationMode:      &orchestrationMode,
		ResultCombinationMode:  &combinationMode,
		CombinationInstruction: &combinationInstruction,
		OutputSchema:           outputSchema,
		TemplateRef:            templateRef,
	})

	if task.OrchestrationMode != workspace.TaskOrchestrationModeGraph {
		t.Fatalf("expected graph orchestration mode, got %q", task.OrchestrationMode)
	}
	if task.ResultCombinationMode != workspace.TaskResultCombinationStructuredOutput {
		t.Fatalf("expected structured_outputs combination mode, got %q", task.ResultCombinationMode)
	}
	if task.CombinationInstruction != combinationInstruction {
		t.Fatalf("expected combination instruction to persist, got %q", task.CombinationInstruction)
	}
	if task.OutputSchema == nil || len(task.OutputSchema.Fields) != 2 {
		t.Fatalf("expected normalized output schema to persist, got %#v", task.OutputSchema)
	}
	if task.TemplateRef == nil || task.TemplateRef.StepName != "Assess" {
		t.Fatalf("expected template ref to persist, got %#v", task.TemplateRef)
	}
}

func TestExtractTaskIDForDelete(t *testing.T) {
	t.Run("from query id", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/orchestration/tasks?id=task-query-1", nil)
		got := extractTaskIDForDelete(req)
		if got != "task-query-1" {
			t.Fatalf("expected task-query-1, got %q", got)
		}
	})

	t.Run("from rest path", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/orchestration/tasks/task-path-1", nil)
		got := extractTaskIDForDelete(req)
		if got != "task-path-1" {
			t.Fatalf("expected task-path-1, got %q", got)
		}
	})

	t.Run("path with extra segment", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/orchestration/tasks/task-path-2/assist", nil)
		got := extractTaskIDForDelete(req)
		if got != "task-path-2" {
			t.Fatalf("expected task-path-2, got %q", got)
		}
	})

	t.Run("query takes precedence over path", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/orchestration/tasks/task-path-3?id=task-query-3", nil)
		got := extractTaskIDForDelete(req)
		if got != "task-query-3" {
			t.Fatalf("expected task-query-3, got %q", got)
		}
	})
}

func TestTasksPathHandler_PreviewTaskResult(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Brand Kit"})
	ws.ID = "workspace-result-preview"

	task := workspace.Task{
		ID:          "task-result-preview",
		WorkspaceID: ws.ID,
		Description: "Generate a brand kit implementation plan",
		To:          "Ori",
		Status:      workspace.TaskStatusCompleted,
		Result: `## Final Summary: Brand Kit Task List

### 1.0 Research
- [ ] 1.1 Review the brandkit note
- [ ] 1.2 Identify gaps @researcher
`,
	}
	workspace.ApplyTaskResultMetadata(&task, task.Result)
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-result-preview/result/preview", nil)
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp taskResultPreviewResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success || resp.SourceTaskID != task.ID || resp.WorkspaceID != ws.ID {
		t.Fatalf("unexpected preview response: %#v", resp)
	}
	if resp.ResultType != workspace.TaskResultTypeTaskList {
		t.Fatalf("expected task_list result type, got %q", resp.ResultType)
	}
	if resp.ItemCount != 2 {
		t.Fatalf("expected 2 task-list items, got %d", resp.ItemCount)
	}
	if resp.TaskList == nil || resp.TaskList.Groups[0].Items[1].Assignee != "researcher" {
		t.Fatalf("expected parsed task list with assignee, got %#v", resp.TaskList)
	}
}

func TestTasksPathHandler_PromoteTaskResult(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Brand Kit"})
	ws.ID = "workspace-result-promote"

	task := workspace.Task{
		ID:             "task-result-promote",
		WorkspaceID:    ws.ID,
		Description:    "Generate a brand kit implementation plan",
		To:             "Ori",
		AssignedNodeID: "Ori-node-1",
		Priority:       2,
		Status:         workspace.TaskStatusCompleted,
		Result: `## Final Summary: Brand Kit Task List

### 1.0 Research
- [ ] 1.1 Review the brandkit note
- [ ] 1.2 Identify gaps @researcher
`,
	}
	workspace.ApplyTaskResultMetadata(&task, task.Result)
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-result-promote/promote-result", nil)
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp promoteTaskResultResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success || resp.ParentTask == nil {
		t.Fatalf("unexpected promote response: %#v", resp)
	}
	if len(resp.Subtasks) != 2 {
		t.Fatalf("expected 2 subtasks, got %#v", resp.Subtasks)
	}
	if resp.ParentTask.Description != "Brand Kit Task List" {
		t.Fatalf("expected cleaned parent title, got %q", resp.ParentTask.Description)
	}
	if resp.ParentTask.OrchestrationMode != workspace.TaskOrchestrationModeSequential {
		t.Fatalf("expected sequential parent task, got %q", resp.ParentTask.OrchestrationMode)
	}
	if resp.Subtasks[0].ParentTaskID != resp.ParentTask.ID || resp.Subtasks[0].SubtaskIndex != 1 {
		t.Fatalf("expected first subtask to attach to parent with index 1, got %#v", resp.Subtasks[0])
	}
	if resp.Subtasks[0].Description != "Review the brandkit note" {
		t.Fatalf("expected cleaned subtask description, got %q", resp.Subtasks[0].Description)
	}
	if resp.Subtasks[1].To != "researcher" {
		t.Fatalf("expected explicit assignee researcher, got %q", resp.Subtasks[1].To)
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	if len(savedWS.Tasks) != 4 {
		t.Fatalf("expected source task, parent, and 2 subtasks, got %d tasks", len(savedWS.Tasks))
	}
	if !savedWS.HasAgent("Ori") || !savedWS.HasAgent("researcher") {
		t.Fatalf("expected promotion to ensure task assignees exist, got agents=%v instances=%#v", savedWS.AgentNames(), savedWS.AgentInstances)
	}
}

func TestTasksPathHandler_PromoteTaskResultPreservesGroups(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Brand Kit"})
	ws.ID = "workspace-result-promote-groups"

	task := workspace.Task{
		ID:          "task-result-promote-groups",
		WorkspaceID: ws.ID,
		Description: "Generate a grouped brand kit implementation plan",
		To:          "Ori",
		Status:      workspace.TaskStatusCompleted,
		Result: `## Brand Kit → Task List: johnj

**1.0 Brand Identity Foundation**
- [ ] 1.1 Finalize handle format rules
- [ ] 1.2 Lock positioning line

**2.0 Visual Identity**
- [ ] 2.1 Lock color palette
- [ ] 2.2 Lock typography
`,
	}
	workspace.ApplyTaskResultMetadata(&task, task.Result)
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-result-promote-groups/promote-result", nil)
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp promoteTaskResultResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ParentTask == nil {
		t.Fatal("expected parent task")
	}
	if len(resp.Subtasks) != 6 {
		t.Fatalf("expected 2 group tasks and 4 leaf subtasks, got %#v", resp.Subtasks)
	}

	groupOne := resp.Subtasks[0]
	groupTwo := resp.Subtasks[3]
	if groupOne.ParentTaskID != resp.ParentTask.ID || groupOne.SubtaskIndex != 1 {
		t.Fatalf("expected first group to attach to parent at index 1, got %#v", groupOne)
	}
	if groupOne.Description != "1.0 Brand Identity Foundation" {
		t.Fatalf("expected numbered group title, got %q", groupOne.Description)
	}
	if groupOne.OrchestrationMode != workspace.TaskOrchestrationModeSequential {
		t.Fatalf("expected group task to be sequential, got %q", groupOne.OrchestrationMode)
	}
	if groupTwo.ParentTaskID != resp.ParentTask.ID || groupTwo.SubtaskIndex != 2 {
		t.Fatalf("expected second group to attach to parent at index 2, got %#v", groupTwo)
	}
	if groupTwo.Description != "2.0 Visual Identity" {
		t.Fatalf("expected second numbered group title, got %q", groupTwo.Description)
	}
	if resp.Subtasks[1].ParentTaskID != groupOne.ID || resp.Subtasks[1].SubtaskIndex != 1 {
		t.Fatalf("expected first leaf subtask under first group, got %#v", resp.Subtasks[1])
	}
	if resp.Subtasks[4].ParentTaskID != groupTwo.ID || resp.Subtasks[4].SubtaskIndex != 1 {
		t.Fatalf("expected first leaf subtask under second group, got %#v", resp.Subtasks[4])
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	if len(savedWS.Tasks) != 8 {
		t.Fatalf("expected source task, parent, 2 group tasks, and 4 leaf subtasks, got %d tasks", len(savedWS.Tasks))
	}
}

func TestTasksPathHandler_PreviewTaskResultRejectsPlainMarkdown(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Notes"})
	ws.ID = "workspace-result-plain"

	task := workspace.Task{
		ID:          "task-result-plain",
		WorkspaceID: ws.ID,
		Description: "Summarize notes",
		To:          "Ori",
		Status:      workspace.TaskStatusCompleted,
		Result:      "This is a plain markdown result without checklist items.",
	}
	workspace.ApplyTaskResultMetadata(&task, task.Result)
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-result-plain/result/preview", nil)
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTasksPathHandler_PromoteTaskResultRejectsInvalidInputs(t *testing.T) {
	store := workspace.NewInMemoryStore()
	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	t.Run("missing task", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/missing/promote-result", nil)
		rec := httptest.NewRecorder()

		handler.TasksPathHandler(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("non-promotable result", func(t *testing.T) {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Notes"})
		ws.ID = "workspace-result-promote-invalid"
		task := workspace.Task{
			ID:          "task-result-promote-invalid",
			WorkspaceID: ws.ID,
			Description: "Summarize notes",
			To:          "Ori",
			Status:      workspace.TaskStatusCompleted,
			Result:      "This is a plain markdown result without checklist items.",
		}
		workspace.ApplyTaskResultMetadata(&task, task.Result)
		if err := ws.AddTask(task); err != nil {
			t.Fatalf("failed to add task: %v", err)
		}
		if err := store.Save(ws); err != nil {
			t.Fatalf("failed to save workspace: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-result-promote-invalid/promote-result", nil)
		rec := httptest.NewRecorder()

		handler.TasksPathHandler(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected status 422, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleCancelTask_CancelsRunningTask(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workshop"})
	ws.ID = "workspace-cancel"

	task := workspace.Task{
		ID:          "task-cancel",
		Description: "Solder connector",
		To:          "Ori",
		Status:      workspace.TaskStatusInProgress,
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}
	handler.registerRunningTask(task.ID, cancel)

	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-cancel/cancel", nil)
	rec := httptest.NewRecorder()

	handler.handleCancelTask(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected registered task context to be cancelled")
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	savedTask, err := savedWS.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to reload task: %v", err)
	}
	if savedTask.Status != workspace.TaskStatusCancelled {
		t.Fatalf("expected cancelled status, got %q", savedTask.Status)
	}
	if savedTask.Error != "Cancelled by user" {
		t.Fatalf("expected cancellation error, got %q", savedTask.Error)
	}
	if savedTask.CompletedAt == nil {
		t.Fatal("expected completed timestamp to be set")
	}
}

func TestHandleCompleteTask_AllowsAssignedTask(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workshop"})
	ws.ID = "workspace-complete-assigned"

	task := workspace.Task{
		ID:          "task-complete-assigned",
		Description: "Review the checklist",
		To:          "Ori",
		Status:      workspace.TaskStatusAssigned,
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-complete-assigned/complete", nil)
	rec := httptest.NewRecorder()

	handler.handleCompleteTask(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success bool            `json:"success"`
		Task    *workspace.Task `json:"task"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success response")
	}
	if resp.Task == nil || resp.Task.Status != workspace.TaskStatusCompleted {
		t.Fatalf("expected completed task in response, got %#v", resp.Task)
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	savedTask, err := savedWS.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to reload task: %v", err)
	}
	if savedTask.Status != workspace.TaskStatusCompleted {
		t.Fatalf("expected completed status, got %q", savedTask.Status)
	}
	if savedTask.Context["manual_completion"] == nil {
		t.Fatalf("expected manual completion context to be recorded")
	}
}

func TestHandleCompleteTask_RejectsParentWithIncompleteSubtasks(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workshop"})
	ws.ID = "workspace-complete-parent"

	parent := workspace.Task{
		ID:          "parent-task",
		Description: "Build release plan",
		Status:      workspace.TaskStatusAssigned,
	}
	child := workspace.Task{
		ID:           "child-task",
		Description:  "Draft changelog",
		ParentTaskID: parent.ID,
		Status:       workspace.TaskStatusPending,
	}
	if err := ws.AddTask(parent); err != nil {
		t.Fatalf("failed to add parent task: %v", err)
	}
	if err := ws.AddTask(child); err != nil {
		t.Fatalf("failed to add child task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/parent-task/complete", nil)
	rec := httptest.NewRecorder()

	handler.handleCompleteTask(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	savedParent, err := savedWS.GetTask(parent.ID)
	if err != nil {
		t.Fatalf("failed to reload parent task: %v", err)
	}
	if savedParent.Status != workspace.TaskStatusAssigned {
		t.Fatalf("expected parent to remain assigned, got %q", savedParent.Status)
	}
}

func TestHandleCompleteTask_ForceCompletesParentWithIncompleteSubtasks(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workshop"})
	ws.ID = "workspace-complete-parent-force"

	parent := workspace.Task{
		ID:          "parent-task-force",
		Description: "Build release plan",
		Status:      workspace.TaskStatusAssigned,
	}
	child := workspace.Task{
		ID:           "child-task-force",
		Description:  "Draft changelog",
		ParentTaskID: parent.ID,
		Status:       workspace.TaskStatusPending,
	}
	if err := ws.AddTask(parent); err != nil {
		t.Fatalf("failed to add parent task: %v", err)
	}
	if err := ws.AddTask(child); err != nil {
		t.Fatalf("failed to add child task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	body := `{"force":true,"reason":"Verified outside Ori"}`
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/parent-task-force/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.handleCompleteTask(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	savedParent, err := savedWS.GetTask(parent.ID)
	if err != nil {
		t.Fatalf("failed to reload parent task: %v", err)
	}
	if savedParent.Status != workspace.TaskStatusCompleted {
		t.Fatalf("expected parent to be completed, got %q", savedParent.Status)
	}
	record, ok := savedParent.Context["manual_completion"].(map[string]any)
	if !ok {
		t.Fatalf("expected manual completion record, got %T", savedParent.Context["manual_completion"])
	}
	if record["force"] != true {
		t.Fatalf("expected forced completion record, got %#v", record)
	}
	if record["reason"] != "Verified outside Ori" {
		t.Fatalf("expected completion reason to be recorded, got %#v", record)
	}
}

func TestHandleUpdateTask_StatusReturnsUpdatedTask(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workshop"})
	ws.ID = "workspace-status-update"

	task := workspace.Task{
		ID:          "task-status-update",
		Description: "Read results",
		Status:      workspace.TaskStatusPending,
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	body := `{"status":"completed","result":"Looks good"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/orchestration/tasks/task-status-update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var updated workspace.Task
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("failed to decode updated task: %v", err)
	}
	if updated.ID != task.ID {
		t.Fatalf("expected updated task id %q, got %q", task.ID, updated.ID)
	}
	if updated.Status != workspace.TaskStatusCompleted {
		t.Fatalf("expected completed status, got %q", updated.Status)
	}
	if updated.Result != "Looks good" {
		t.Fatalf("expected result to be preserved, got %q", updated.Result)
	}
}

func TestHandleUpdateTask_Priority(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workshop"})
	ws.ID = "workspace-priority-update"

	task := workspace.Task{
		ID:          "task-priority-update",
		Description: "Tune priority",
		Priority:    2,
		Status:      workspace.TaskStatusPending,
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	body := `{"priority":5}`
	req := httptest.NewRequest(http.MethodPatch, "/api/orchestration/tasks/task-priority-update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var updated workspace.Task
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("failed to decode updated task: %v", err)
	}
	if updated.Priority != 5 {
		t.Fatalf("expected priority 5 in response, got %d", updated.Priority)
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	savedTask, err := savedWS.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to reload task: %v", err)
	}
	if savedTask.Priority != 5 {
		t.Fatalf("expected saved priority 5, got %d", savedTask.Priority)
	}
}

func TestHandleCreateAndUpdateTask_OutputContract(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Contracts"})
	ws.ID = "workspace-contracts"
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	createBody := `{
		"workspace_id":"workspace-contracts",
		"description":"Track pollen",
		"result_storage":{"enabled":true,"format":"csv","write_mode":"append"},
		"output_contract":{
			"source":"manual",
			"columns":[
				{"name":"date","type":"date","required":true},
				{"name":"pollen_count","type":"number","required":true},
				{"name":"POLLEN_COUNT","type":"string"}
			]
		}
	}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.TasksHandler(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var createResp struct {
		Task *workspace.Task `json:"task"`
	}
	if err := json.NewDecoder(createRec.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.Task == nil || createResp.Task.OutputContract == nil {
		t.Fatalf("expected created output contract, got %#v", createResp.Task)
	}
	if len(createResp.Task.OutputContract.Columns) != 2 {
		t.Fatalf("expected duplicate column to be normalized away, got %+v", createResp.Task.OutputContract.Columns)
	}
	if createResp.Task.OutputContract.Version == "" {
		t.Fatal("expected output contract version")
	}

	updateBody := `{"output_contract":{"columns":[]}}`
	updateReq := httptest.NewRequest(http.MethodPatch, "/api/orchestration/tasks/"+createResp.Task.ID, strings.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	handler.TasksPathHandler(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}
	var updated workspace.Task
	if err := json.NewDecoder(updateRec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.OutputContract != nil {
		t.Fatalf("expected output contract to be cleared, got %+v", updated.OutputContract)
	}
}

func TestHandleCreateAndUpdateTask_ReferenceURL(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "References"})
	ws.ID = "workspace-references"
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	createBody := `{
		"workspace_id":"workspace-references",
		"description":"Review spec",
		"reference_url":" https://example.com/spec "
	}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.TasksHandler(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var createResp struct {
		Task *workspace.Task `json:"task"`
	}
	if err := json.NewDecoder(createRec.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.Task == nil || createResp.Task.ReferenceURL != "https://example.com/spec" {
		t.Fatalf("created task = %+v, want normalized reference URL", createResp.Task)
	}

	updateBody := `{"reference_url":""}`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/orchestration/tasks/"+createResp.Task.ID, strings.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	handler.TasksHandler(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}
	var updated workspace.Task
	if err := json.NewDecoder(updateRec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.ReferenceURL != "" {
		t.Fatalf("ReferenceURL = %q, want cleared", updated.ReferenceURL)
	}
}

func TestHandleTaskOutputReview_ApproveAppend(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Reviews"})
	ws.ID = "workspace-review-approve"

	outputPath := filepath.Join(t.TempDir(), "pollen.csv")
	task := workspace.Task{
		ID:          "task-review-approve",
		WorkspaceID: ws.ID,
		Description: "Track pollen",
		Status:      workspace.TaskStatusCompleted,
		Result:      `{"date":"bad","location":"NYC"}`,
		ResultStorage: &workspace.ResultStorageConfig{
			Enabled:   true,
			FilePath:  outputPath,
			Format:    "csv",
			WriteMode: "append",
		},
		OutputContract: workspace.NormalizeTaskOutputContract(&workspace.TaskOutputContract{
			Source: "manual",
			Columns: []workspace.TaskOutputContractColumn{
				{Name: "date", Type: "date", Required: true},
				{Name: "location", Type: "string", Required: true},
				{Name: "pollen_count", Type: "number", Required: true},
			},
		}),
		ExecutionHistory: []workspace.TaskExecution{
			{
				TaskID:     "task-review-approve",
				ExecutedAt: time.Now(),
				Status:     "success",
				Result:     `{"date":"bad","location":"NYC"}`,
				Validation: &workspace.TaskValidationResult{
					ValidationStatus: workspace.TaskValidationNeedsReview,
					StorageStatus:    workspace.TaskStorageSkippedInvalid,
				},
			},
		},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	body := `{"action":"approve_append","history_index":0,"result":"{\"date\":\"2026-05-20\",\"location\":\"NYC\",\"pollen_count\":8}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-review-approve/review", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	records := readReviewJSONL(t, outputPath)
	if len(records) != 1 || records[0]["date"] != "2026-05-20" || records[0]["location"] != "NYC" || records[0]["pollen_count"] != "8" {
		t.Fatalf("unexpected appended records: %#v", records)
	}
	updatedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	updatedTask, err := updatedWS.GetTask(task.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	validation := updatedTask.ExecutionHistory[0].Validation
	if validation == nil {
		t.Fatal("expected validation result")
	}
	if validation.ValidationStatus != workspace.TaskValidationManuallyApproved || validation.StorageStatus != workspace.TaskStorageManuallyAppended {
		t.Fatalf("validation = %+v, want manually approved/appended", validation)
	}
	if validation.ManualApproval == nil {
		t.Fatal("expected manual approval marker")
	}
}

func TestHandleTaskOutputReview_ApproveAppendWritesJSONL(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Reviews"})
	ws.ID = "workspace-review-approve-header-mismatch"

	outputPath := filepath.Join(t.TempDir(), "pollen.jsonl")
	// A pre-existing record must not block the approval append (JSONL has no
	// header to reconcile, unlike the old CSV-append path).
	if err := os.WriteFile(outputPath, []byte(`{"date":"2026-05-19","location":"Boston"}`+"\n"), 0644); err != nil {
		t.Fatalf("write existing jsonl: %v", err)
	}
	task := workspace.Task{
		ID:          "task-review-approve-header-mismatch",
		WorkspaceID: ws.ID,
		Description: "Track pollen",
		Status:      workspace.TaskStatusCompleted,
		ResultStorage: &workspace.ResultStorageConfig{
			Enabled:   true,
			FilePath:  outputPath,
			Format:    "csv",
			WriteMode: "append",
		},
		OutputContract: workspace.NormalizeTaskOutputContract(&workspace.TaskOutputContract{
			Source: "manual",
			Columns: []workspace.TaskOutputContractColumn{
				{Name: "date", Type: "date", Required: true},
				{Name: "location", Type: "string", Required: true},
			},
		}),
		ExecutionHistory: []workspace.TaskExecution{
			{
				TaskID:     "task-review-approve-header-mismatch",
				RunID:      "run-header-mismatch",
				ExecutedAt: time.Now(),
				Status:     "success",
				Result:     `{"date":"bad","location":"NYC"}`,
				Validation: &workspace.TaskValidationResult{
					ValidationStatus: workspace.TaskValidationNeedsReview,
					StorageStatus:    workspace.TaskStorageSkippedInvalid,
				},
			},
		},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}
	body := `{"action":"approve_append","history_index":0,"result":"{\"date\":\"2026-05-20\",\"location\":\"NYC\"}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-review-approve-header-mismatch/review", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	records := readReviewJSONL(t, outputPath)
	if len(records) != 2 {
		t.Fatalf("expected existing + approved record, got %d", len(records))
	}
	approved := records[len(records)-1]
	if approved["date"] != "2026-05-20" || approved["location"] != "NYC" {
		t.Fatalf("unexpected approved record: %#v", approved)
	}
	updatedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	updatedTask, err := updatedWS.GetTask(task.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	validation := updatedTask.ExecutionHistory[0].Validation
	if validation == nil || validation.ValidationStatus != workspace.TaskValidationManuallyApproved || validation.StorageStatus != workspace.TaskStorageManuallyAppended {
		t.Fatalf("validation = %+v, want manually_approved/manually_appended", validation)
	}
}

// readReviewJSONL parses a .jsonl dataset file into records. The review/approve
// append paths now write JSONL (converted from the CSV they validate).
func readReviewJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %q is not JSON: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func TestTaskOutputReviewApproval_UsesValidationSpecSnapshot(t *testing.T) {
	runSpec, errs := workspace.NormalizeTaskOutputSpec(&workspace.TaskOutputSpec{
		Source: "manual",
		Schema: &workspace.TaskOutputSchema{
			Name:   "pollen_v1",
			Strict: true,
			Fields: []workspace.TaskOutputField{
				{Name: "date", Type: "string", Required: true},
				{Name: "pollen_count", Type: "number", Required: true},
			},
		},
		Contract: &workspace.TaskOutputContract{
			Source: "manual",
			Columns: []workspace.TaskOutputContractColumn{
				{Name: "date", Type: "date", Required: true},
				{Name: "pollen_count", Type: "number", Required: true},
			},
		},
		Mappings: []workspace.TaskOutputMapping{
			{SchemaField: "date", CSVColumn: "date", Transform: workspace.TaskOutputMappingTransformIdentity},
			{SchemaField: "pollen_count", CSVColumn: "pollen_count", Transform: workspace.TaskOutputMappingTransformIdentity},
		},
	})
	if len(errs) > 0 {
		t.Fatalf("normalize run spec: %v", errs)
	}
	runSpec = workspace.AssignTaskOutputSpecVersion(runSpec)
	activeSpec := workspace.SnapshotTaskOutputSpec(runSpec)
	activeSpec.Schema.Fields = append(activeSpec.Schema.Fields, workspace.TaskOutputField{Name: "location", Type: "string", Required: true})
	activeSpec.Contract.Columns = append(activeSpec.Contract.Columns, workspace.TaskOutputContractColumn{Name: "location", Type: "string", Required: true})
	activeSpec.Mappings = append(activeSpec.Mappings, workspace.TaskOutputMapping{SchemaField: "location", CSVColumn: "location", Transform: workspace.TaskOutputMappingTransformIdentity})

	task := &workspace.Task{ID: "task-review-snapshot", OutputSpec: activeSpec}
	reviewTask := taskOutputReviewValidationTask(task, &workspace.TaskValidationResult{OutputSpec: runSpec})
	validation, _ := validateTaskOutputReviewApproval(reviewTask, `{"date":"2026-05-20","pollen_count":8}`)

	if validation.ValidationStatus != workspace.TaskValidationPassed {
		t.Fatalf("validation = %+v, want passed under run spec snapshot", validation)
	}
	if validation.ContractVersion != runSpec.Version {
		t.Fatalf("contract_version = %q, want %q", validation.ContractVersion, runSpec.Version)
	}
}

func TestHandleTaskOutputReview_RetryNormalizationUsesRunSpecSnapshot(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Reviews"})
	ws.ID = "workspace-review-retry-normalization"

	runSpec, errs := workspace.NormalizeTaskOutputSpec(&workspace.TaskOutputSpec{
		Source: "manual",
		Schema: &workspace.TaskOutputSchema{
			Name:   "pollen_v1",
			Strict: true,
			Fields: []workspace.TaskOutputField{
				{Name: "forecast_date", Type: "string", Required: true},
				{Name: "pollen_count", Type: "number", Required: true},
			},
		},
		Contract: &workspace.TaskOutputContract{
			Source: "manual",
			Columns: []workspace.TaskOutputContractColumn{
				{Name: "forecast_date", Type: "date", Required: true},
				{Name: "pollen_count", Type: "number", Required: true},
			},
		},
		Mappings: []workspace.TaskOutputMapping{
			{SchemaField: "forecast_date", CSVColumn: "forecast_date", Transform: workspace.TaskOutputMappingTransformIdentity},
			{SchemaField: "pollen_count", CSVColumn: "pollen_count", Transform: workspace.TaskOutputMappingTransformIdentity},
		},
	})
	if len(errs) > 0 {
		t.Fatalf("normalize run spec: %v", errs)
	}
	runSpec = workspace.AssignTaskOutputSpecVersion(runSpec)
	activeSpec := workspace.SnapshotTaskOutputSpec(runSpec)
	activeSpec.Schema.Fields = append(activeSpec.Schema.Fields, workspace.TaskOutputField{Name: "location", Type: "string", Required: true})
	activeSpec.Contract.Columns = append(activeSpec.Contract.Columns, workspace.TaskOutputContractColumn{Name: "location", Type: "string", Required: true})
	activeSpec.Mappings = append(activeSpec.Mappings, workspace.TaskOutputMapping{SchemaField: "location", CSVColumn: "location", Transform: workspace.TaskOutputMappingTransformIdentity})

	outputPath := filepath.Join(t.TempDir(), "pollen.csv")
	executedAt := time.Date(2026, 5, 21, 9, 30, 0, 0, time.UTC)
	task := workspace.Task{
		ID:          "task-review-retry-normalization",
		WorkspaceID: ws.ID,
		Description: "Track pollen",
		To:          "Ori",
		Status:      workspace.TaskStatusCompleted,
		OutputSpec:  activeSpec,
		ResultStorage: &workspace.ResultStorageConfig{
			Enabled:   true,
			FilePath:  outputPath,
			Format:    "csv",
			WriteMode: "append",
		},
		ExecutionHistory: []workspace.TaskExecution{
			{
				TaskID:     "task-review-retry-normalization",
				RunID:      "run-review-normalize",
				ExecutedAt: executedAt,
				Status:     "success",
				Result:     "Pollen count was 9.7 on May 21.",
				Duration:   2500,
				Validation: &workspace.TaskValidationResult{
					ValidationStatus: workspace.TaskValidationNeedsReview,
					StorageStatus:    workspace.TaskStorageSkippedInvalid,
					ContractVersion:  runSpec.Version,
					OutputSpec:       workspace.SnapshotTaskOutputSpec(runSpec),
					Errors: []workspace.TaskValidationError{{
						Code:    "normalization_provider_error",
						Message: "provider unavailable",
					}},
				},
			},
		},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	eventBus := workspace.NewEventBus(10, 20)
	executor := &reviewNormalizationExecutor{
		normalizeResult: `{"forecast_date":"2026-05-21","pollen_count":9.7}`,
	}
	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
		taskHandler:    executor,
		eventBus:       eventBus,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-review-retry-normalization/review", strings.NewReader(`{"action":"retry_normalization","history_index":0}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if executor.normalizeCalls != 1 {
		t.Fatalf("normalizeCalls=%d, want 1", executor.normalizeCalls)
	}
	records := readReviewJSONL(t, outputPath)
	if len(records) != 1 {
		t.Fatalf("expected 1 normalized record, got %d", len(records))
	}
	record := records[0]
	if record["forecast_date"] != "2026-05-21" || record["pollen_count"] != "9.7" ||
		record["run_id"] != "run-review-normalize" || record["status"] != "success" {
		t.Fatalf("unexpected normalized record: %#v", record)
	}
	updatedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	updatedTask, err := updatedWS.GetTask(task.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	validation := updatedTask.ExecutionHistory[0].Validation
	if validation == nil || validation.ValidationStatus != workspace.TaskValidationPassed || validation.StorageStatus != workspace.TaskStorageAppended {
		t.Fatalf("validation = %+v, want passed/appended", validation)
	}
	if validation.ContractVersion != runSpec.Version {
		t.Fatalf("contract_version=%q, want run snapshot %q", validation.ContractVersion, runSpec.Version)
	}
	if validation.NormalizedRow == nil || validation.NormalizedRow["forecast_date"] != "2026-05-21" {
		t.Fatalf("normalized row not recorded: %+v", validation.NormalizedRow)
	}
	events := eventBus.GetHistory(func(event workspace.Event) bool {
		return event.Type == workspace.EventTaskOutput
	}, 1)
	if len(events) != 1 || events[0].Data["review"] != "retry_normalization" {
		t.Fatalf("expected retry_normalization telemetry, got %#v", events)
	}
}

func TestAppendCSVContractReviewSmoke_CreateInvalidApprove(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Reviews"})
	ws.ID = "workspace-review-smoke"
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "pollen.csv")
	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	createBody := map[string]any{
		"workspace_id": ws.ID,
		"description":  "Track pollen daily",
		"to":           "Ori",
		"result_storage": map[string]any{
			"enabled":    true,
			"file_path":  outputPath,
			"format":     "csv",
			"write_mode": "append",
		},
		"output_contract": map[string]any{
			"source": "manual",
			"columns": []any{
				map[string]any{"name": "date", "type": "date", "required": true},
				map[string]any{"name": "location", "type": "string", "required": true},
				map[string]any{"name": "pollen_count", "type": "number", "required": true},
			},
		},
	}
	bodyBytes, err := json.Marshal(createBody)
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks", strings.NewReader(string(bodyBytes)))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.TasksHandler(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var createResp struct {
		Task *workspace.Task `json:"task"`
	}
	if err := json.NewDecoder(createRec.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.Task == nil || createResp.Task.OutputContract == nil {
		t.Fatalf("expected created append contract task, got %#v", createResp.Task)
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	storedTask, err := savedWS.GetTask(createResp.Task.ID)
	if err != nil {
		t.Fatalf("load created task: %v", err)
	}
	invalidResult := `{"date":"bad","location":"NYC"}`
	storedTask.Status = workspace.TaskStatusCompleted
	storedTask.Result = invalidResult
	storedTask.CurrentRunID = "run-review-smoke"
	workspace.RecordTaskExecution(storedTask, "success", invalidResult, time.Now(), time.Second)
	if err := savedWS.UpdateTask(*storedTask); err != nil {
		t.Fatalf("persist invalid execution: %v", err)
	}

	workspace.AutoStoreResult(savedWS, storedTask, invalidResult, store)
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("expected invalid output not to create csv, stat err = %v", err)
	}
	updatedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	updatedTask, err := updatedWS.GetTask(createResp.Task.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	validation := updatedTask.ExecutionHistory[len(updatedTask.ExecutionHistory)-1].Validation
	if validation == nil || validation.ValidationStatus != workspace.TaskValidationNeedsReview {
		t.Fatalf("validation = %+v, want needs_review", validation)
	}

	approveBody := `{"action":"approve_append","result":"{\"date\":\"2026-05-20\",\"location\":\"NYC\",\"pollen_count\":8}","approved_by":"test"}`
	approveReq := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/"+createResp.Task.ID+"/review", strings.NewReader(approveBody))
	approveReq.Header.Set("Content-Type", "application/json")
	approveRec := httptest.NewRecorder()
	handler.TasksPathHandler(approveRec, approveReq)

	if approveRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", approveRec.Code, approveRec.Body.String())
	}
	records := readReviewJSONL(t, outputPath)
	if len(records) != 1 || records[0]["date"] != "2026-05-20" || records[0]["location"] != "NYC" || records[0]["pollen_count"] != "8" {
		t.Fatalf("unexpected approved records: %#v", records)
	}
}

func TestHandleTaskOutputReview_Dismiss(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Reviews"})
	ws.ID = "workspace-review-dismiss"

	task := workspace.Task{
		ID:          "task-review-dismiss",
		WorkspaceID: ws.ID,
		Description: "Track pollen",
		Status:      workspace.TaskStatusCompleted,
		ExecutionHistory: []workspace.TaskExecution{
			{
				TaskID:     "task-review-dismiss",
				ExecutedAt: time.Now(),
				Status:     "success",
				Result:     `{"date":"bad"}`,
				Validation: &workspace.TaskValidationResult{
					ValidationStatus: workspace.TaskValidationNeedsReview,
					StorageStatus:    workspace.TaskStorageSkippedInvalid,
				},
			},
		},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	eventBus := workspace.NewEventBus(10, 10)
	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
		eventBus:       eventBus,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-review-dismiss/review", strings.NewReader(`{"action":"dismiss","history_index":0}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	updatedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	updatedTask, err := updatedWS.GetTask(task.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	validation := updatedTask.ExecutionHistory[0].Validation
	if validation == nil || validation.ValidationStatus != workspace.TaskValidationDismissed {
		t.Fatalf("validation = %+v, want dismissed", validation)
	}
	events := eventBus.GetHistory(func(event workspace.Event) bool {
		return event.Type == workspace.EventTaskOutput
	}, 1)
	if len(events) != 1 {
		t.Fatalf("expected one review telemetry event, got %d", len(events))
	}
	if events[0].Data["action"] != "review_action" || events[0].Data["review"] != "dismiss" {
		t.Fatalf("unexpected review telemetry: %#v", events[0].Data)
	}
}

func TestHandleTaskOutputReview_RerunStartsTaskExecution(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Reviews"})
	ws.ID = "workspace-review-rerun"

	task := workspace.Task{
		ID:          "task-review-rerun",
		WorkspaceID: ws.ID,
		Description: "Report status",
		To:          "Ori",
		Status:      workspace.TaskStatusCompleted,
		Result:      `{"date":"bad"}`,
		ExecutionHistory: []workspace.TaskExecution{
			{
				TaskID:     "task-review-rerun",
				ExecutedAt: time.Now(),
				Status:     "success",
				Result:     `{"date":"bad"}`,
				Validation: &workspace.TaskValidationResult{
					ValidationStatus: workspace.TaskValidationNeedsReview,
					StorageStatus:    workspace.TaskStorageSkippedInvalid,
				},
			},
		},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	eventBus := workspace.NewEventBus(10, 20)
	executor := &stubWorkspaceTaskExecutor{result: "Re-run completed successfully."}
	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
		taskHandler:    executor,
		eventBus:       eventBus,
		runningCancels: make(map[string]context.CancelFunc),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-review-rerun/review", strings.NewReader(`{"action":"rerun","history_index":0}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if executor.calls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if executor.calls.Load() == 0 {
		t.Fatal("expected review re-run to start task execution")
	}
	events := eventBus.GetHistory(func(event workspace.Event) bool {
		return event.Type == workspace.EventTaskOutput
	}, 1)
	if len(events) != 1 {
		t.Fatalf("expected one review telemetry event, got %d", len(events))
	}
	if events[0].Data["review"] != "rerun" {
		t.Fatalf("expected rerun review telemetry, got %#v", events[0].Data)
	}
}

type reviewNormalizationExecutor struct {
	stubWorkspaceTaskExecutor
	normalizeResult string
	normalizeErr    error
	repairResult    string
	repairErr       error
	normalizeCalls  int
	repairCalls     int
}

func (e *reviewNormalizationExecutor) NormalizeTaskOutputSpec(_ context.Context, _ workspace.Task, _ string) (string, error) {
	e.normalizeCalls++
	return e.normalizeResult, e.normalizeErr
}

func (e *reviewNormalizationExecutor) RepairTaskOutputSpec(_ context.Context, _ workspace.Task, _ string, _ map[string]any, _ []workspace.TaskValidationError) (string, error) {
	e.repairCalls++
	return e.repairResult, e.repairErr
}

func TestHandleTaskOutputReview_InspectReturnsRawResult(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Reviews"})
	ws.ID = "workspace-review-inspect"

	task := workspace.Task{
		ID:          "task-review-inspect",
		WorkspaceID: ws.ID,
		Description: "Track pollen",
		Status:      workspace.TaskStatusCompleted,
		ExecutionHistory: []workspace.TaskExecution{
			{
				TaskID:     "task-review-inspect",
				ExecutedAt: time.Now(),
				Status:     "success",
				Result:     `{"date":"bad","location":"NYC"}`,
				Summary:    "bad row",
				Validation: &workspace.TaskValidationResult{
					ValidationStatus: workspace.TaskValidationNeedsReview,
					StorageStatus:    workspace.TaskStorageSkippedInvalid,
				},
			},
		},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-review-inspect/review", strings.NewReader(`{"action":"inspect","history_index":0}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success    bool                            `json:"success"`
		Result     string                          `json:"result"`
		Validation *workspace.TaskValidationResult `json:"validation_result"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success || resp.Result != `{"date":"bad","location":"NYC"}` {
		t.Fatalf("response = %+v, want raw result", resp)
	}
	if resp.Validation == nil || resp.Validation.ValidationStatus != workspace.TaskValidationNeedsReview {
		t.Fatalf("validation = %+v, want needs_review", resp.Validation)
	}
}

func TestHandleTaskOutputReview_ApproveAppendValidationFailure(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Reviews"})
	ws.ID = "workspace-review-invalid"

	task := workspace.Task{
		ID:          "task-review-invalid",
		WorkspaceID: ws.ID,
		Description: "Track pollen",
		Status:      workspace.TaskStatusCompleted,
		ResultStorage: &workspace.ResultStorageConfig{
			Enabled:   true,
			FilePath:  filepath.Join(t.TempDir(), "pollen.csv"),
			Format:    "csv",
			WriteMode: "append",
		},
		OutputContract: workspace.NormalizeTaskOutputContract(&workspace.TaskOutputContract{
			Source: "manual",
			Columns: []workspace.TaskOutputContractColumn{
				{Name: "date", Type: "date", Required: true},
				{Name: "pollen_count", Type: "number", Required: true},
			},
		}),
		ExecutionHistory: []workspace.TaskExecution{
			{
				TaskID:     "task-review-invalid",
				ExecutedAt: time.Now(),
				Status:     "success",
				Result:     `{"date":"bad"}`,
				Validation: &workspace.TaskValidationResult{
					ValidationStatus: workspace.TaskValidationNeedsReview,
					StorageStatus:    workspace.TaskStorageSkippedInvalid,
				},
			},
		},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	body := `{"action":"approve_append","history_index":0,"result":"{\"date\":\"bad\"}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-review-invalid/review", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.TasksPathHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Validation *workspace.TaskValidationResult `json:"validation_result"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Validation == nil || resp.Validation.ValidationStatus != workspace.TaskValidationNeedsReview {
		t.Fatalf("validation = %+v, want needs_review", resp.Validation)
	}
}

func TestHandleAssistTask_PersistsSelectedChoice(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Spain"})
	ws.ID = "workspace-choice"

	task := workspace.Task{
		ID:          "task-choice",
		WorkspaceID: ws.ID,
		Description: "Plan Lisbon trip",
		To:          "Ori",
		Status:      workspace.TaskStatusPending,
		Context: map[string]any{
			"human_loop": map[string]any{
				"state": "blocked",
			},
		},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	body := `{"action":"mark_failed","choice_id":"save-note","choice_label":"Save as Note","choice_number":"1","message":"Use the spain workspace note"}`
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-choice/assist", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.handleAssistTask(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	savedTask, err := savedWS.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to reload task: %v", err)
	}

	userAssistMessage, _ := savedTask.Context["user_assist_message"].(string)
	if !strings.Contains(userAssistMessage, "Selected next step: Save as Note.") {
		t.Fatalf("expected user assist message to include selected choice, got %q", userAssistMessage)
	}

	userAssistChoice, ok := savedTask.Context["user_assist_choice"].(*workspace.TaskBlockedChoice)
	if !ok {
		t.Fatalf("expected user_assist_choice to be stored as *workspace.TaskBlockedChoice, got %T", savedTask.Context["user_assist_choice"])
	}
	if userAssistChoice.ID != "save-note" {
		t.Fatalf("expected saved choice id save-note, got %q", userAssistChoice.ID)
	}
}

func TestHandleAssistTask_ContinuePersistsChoiceAndResumesSameTask(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Pollen"})
	ws.ID = "workspace-choice-resume"

	task := workspace.Task{
		ID:          "task-choice-resume",
		WorkspaceID: ws.ID,
		Description: "check pollen count in NYC",
		To:          "Ori",
		Status:      workspace.TaskStatusWaitingForChoice,
		Context: map[string]any{
			"human_loop": map[string]any{
				"state":    "waiting_for_choice",
				"block_id": "block-1",
				"workflow_step": &workspace.TaskBlockedWorkflowStep{
					StepType: "ask_choice",
					Choices: []workspace.TaskBlockedChoice{
						{ID: "alternate-source", Label: "Use an alternative data source", Number: "C"},
					},
				},
			},
		},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	stub := &stubWorkspaceTaskExecutor{result: "NYC pollen is high. Source: Example Pollen"}
	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
		taskHandler:    stub,
	}

	body := `{"action":"continue_with_instruction","block_id":"block-1","choice_id":"alternate-source","choice_label":"Use an alternative data source","choice_number":"C","message":"Prefer a no-key public source."}`
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-choice-resume/assist", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.handleAssistTask(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		savedWS, err := store.Get(ws.ID)
		if err != nil {
			t.Fatalf("failed to reload workspace: %v", err)
		}
		savedTask, err := savedWS.GetTask(task.ID)
		if err != nil {
			t.Fatalf("failed to reload task: %v", err)
		}
		if savedTask.Status == workspace.TaskStatusCompleted {
			if savedTask.ID != task.ID {
				t.Fatalf("expected same task to resume, got %q", savedTask.ID)
			}
			if _, ok := savedTask.Context["user_assist_choice"].(*workspace.TaskBlockedChoice); !ok {
				t.Fatalf("expected user_assist_choice to survive resume, got %T", savedTask.Context["user_assist_choice"])
			}
			if _, hasHumanLoop := savedTask.Context["human_loop"]; hasHumanLoop {
				t.Fatal("expected human_loop to be cleared after resumed execution starts")
			}
			if stub.calls.Load() == 0 {
				t.Fatal("expected task handler to be called during resume")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for resumed task completion, last status %q", savedTask.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHandleAssistTask_PersistsFieldValues(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workshop"})
	ws.ID = "workspace-form"

	task := workspace.Task{
		ID:          "task-form",
		WorkspaceID: ws.ID,
		Description: "Plan shelf build",
		To:          "Ori",
		Status:      workspace.TaskStatusPending,
		Context: map[string]any{
			"human_loop": map[string]any{
				"state": "blocked",
				"workflow_step": &workspace.TaskBlockedWorkflowStep{
					StepType: "ask_form",
					Fields: []workspace.TaskBlockedField{
						{ID: "room", Label: "Room", Type: "text", Required: true},
						{ID: "mounting_type", Label: "Mounting type", Type: "select", Required: true},
					},
				},
			},
		},
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("failed to add task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	body := `{"action":"mark_failed","field_values":[{"id":"room","label":"Room","value":"Living room"},{"id":"mounting_type","label":"Mounting type","value":"Freestanding"}],"message":"Use pine boards."}`
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-form/assist", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.handleAssistTask(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	savedWS, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	savedTask, err := savedWS.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to reload task: %v", err)
	}

	userAssistMessage, _ := savedTask.Context["user_assist_message"].(string)
	if !strings.Contains(userAssistMessage, "Provided details:") {
		t.Fatalf("expected user assist message to include provided details, got %q", userAssistMessage)
	}
	if !strings.Contains(userAssistMessage, "- Room: Living room") {
		t.Fatalf("expected room field in user assist message, got %q", userAssistMessage)
	}
	if !strings.Contains(userAssistMessage, "- Mounting type: Freestanding") {
		t.Fatalf("expected mounting field in user assist message, got %q", userAssistMessage)
	}

	userAssistFields, ok := savedTask.Context["user_assist_fields"].([]workspace.TaskBlockedFieldValue)
	if !ok {
		t.Fatalf("expected user_assist_fields to be stored as []workspace.TaskBlockedFieldValue, got %T", savedTask.Context["user_assist_fields"])
	}
	if len(userAssistFields) != 2 {
		t.Fatalf("expected 2 saved field values, got %d", len(userAssistFields))
	}
	if userAssistFields[0].ID != "room" || userAssistFields[0].Value != "Living room" {
		t.Fatalf("unexpected first field value %#v", userAssistFields[0])
	}
}

// TestHandleGetTasks_ExcludesBacklog covers task-list 4.11/1.10 (PRD
// workspace-backlog FR40): the list endpoint every Tasks surface (modal,
// drawer, board, Active Tasks Map window, task counts) reads from must never
// return canonical Backlog items — Backlog has its own dedicated surface.
func TestHandleGetTasks_ExcludesBacklog(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr"})
	if err := ws.AddTask(workspace.Task{Status: workspace.TaskStatusBacklog, Description: "someday maybe"}); err != nil {
		t.Fatalf("add backlog task: %v", err)
	}
	if err := ws.AddTask(workspace.Task{Status: workspace.TaskStatusPending, Description: "ready to go"}); err != nil {
		t.Fatalf("add ready task: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/orchestration/tasks?workspace_id="+ws.ID, nil)
	rec := httptest.NewRecorder()
	handler.TasksHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Tasks []workspace.Task `json:"tasks"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 1 || len(resp.Tasks) != 1 {
		t.Fatalf("expected 1 task (Backlog excluded), got %+v", resp.Tasks)
	}
	if resp.Tasks[0].Description != "ready to go" {
		t.Fatalf("unexpected task returned: %+v", resp.Tasks[0])
	}
}
