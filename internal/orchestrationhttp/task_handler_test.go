package orchestrationhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		Context: map[string]interface{}{
			"human_loop": map[string]interface{}{
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
		Context: map[string]interface{}{
			"human_loop": map[string]interface{}{
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
