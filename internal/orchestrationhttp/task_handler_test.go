package orchestrationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		t.Fatalf("expected promotion to ensure task assignees exist, got agents=%v instances=%#v", savedWS.Agents, savedWS.AgentInstances)
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
