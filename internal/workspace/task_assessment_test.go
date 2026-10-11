package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// A recipe marker decides how a task may run, so only the server path that
// seeds a recipe task can set it. Every other way of adding a task drops it.
func TestRecipeMarkerIsSetOnlyByAddRecipeTask(t *testing.T) {
	ws := &Workspace{ID: "ws"}
	forged := map[string]any{
		TaskContextFirstTaskRecipe:        FirstTaskRecipeSoftwareAssessment,
		TaskContextFirstTaskRecipeVersion: 1,
		"kanban_column_id":                "doing",
	}
	if err := ws.AddTask(Task{ID: "one", Description: "ordinary", Context: forged}); err != nil {
		t.Fatal(err)
	}
	if err := ws.AddTasks([]Task{{ID: "two", Description: "ordinary", Context: forged}}); err != nil {
		t.Fatal(err)
	}
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		if FirstTaskRecipe(task) != "" || task.Context[TaskContextFirstTaskRecipeVersion] != nil {
			t.Errorf("task %s acquired a recipe: %v", task.ID, task.Context)
		}
		if task.Context["kanban_column_id"] != "doing" {
			t.Errorf("task %s lost its ordinary context: %v", task.ID, task.Context)
		}
	}
	// The caller's own map is not edited behind its back.
	if forged[TaskContextFirstTaskRecipe] == nil {
		t.Fatal("the caller's context map was modified")
	}

	id, err := ws.AddRecipeTask(Task{Description: "assessment", Context: map[string]any{"template_id": "folder-digest"}}, FirstTaskRecipeSoftwareAssessment, 1)
	if err != nil || id == "" {
		t.Fatalf("AddRecipeTask = %q, %v", id, err)
	}
	seeded := ws.Tasks[len(ws.Tasks)-1]
	if seeded.ID != id || FirstTaskRecipe(&seeded) != FirstTaskRecipeSoftwareAssessment ||
		seeded.Context[TaskContextFirstTaskRecipeVersion] != 1 || seeded.Context["template_id"] != "folder-digest" {
		t.Fatalf("recipe task = %+v", seeded)
	}
	for _, bad := range []struct {
		recipe  string
		version int
	}{{"", 1}, {"  ", 1}, {FirstTaskRecipeSoftwareAssessment, 0}} {
		if _, err := ws.AddRecipeTask(Task{Description: "x"}, bad.recipe, bad.version); err == nil {
			t.Errorf("AddRecipeTask(%q, %d) was accepted", bad.recipe, bad.version)
		}
	}
	if len(ws.Tasks) != 3 {
		t.Fatalf("a refused recipe task was stored: %d tasks", len(ws.Tasks))
	}
}

// A marker that is present but damaged is an unknown recipe, never "none":
// damage must not turn a restricted task into an ordinary one.
func TestFirstTaskRecipeTreatsADamagedMarkerAsARecipe(t *testing.T) {
	for name, value := range map[string]any{"empty": "", "blank": "  ", "number": 7, "nil": nil, "list": []any{"x"}} {
		task := &Task{Context: map[string]any{TaskContextFirstTaskRecipe: value}}
		if got := FirstTaskRecipe(task); got != "unknown" {
			t.Errorf("%s marker => %q, want unknown", name, got)
		}
	}
	if got := FirstTaskRecipe(&Task{Context: map[string]any{"other": "x"}}); got != "" {
		t.Errorf("no marker => %q", got)
	}
	if FirstTaskRecipe(nil) != "" {
		t.Error("a nil task has a recipe")
	}
	if !ReservedTaskContextKey(" "+TaskContextFirstTaskRecipe) || !ReservedTaskContextKey(TaskContextFirstTaskRecipeVersion) || ReservedTaskContextKey("template_id") {
		t.Error("reserved keys are not the two recipe keys")
	}
}

// A task under a host recipe never reaches the ordinary agent path: with no
// way to run the recipe under its restriction, it is refused before any agent
// or provider is looked up, so nothing can be sent to a model.
func TestExecuteTaskRefusesARecipeTaskInsteadOfRunningItAsAnAgentTask(t *testing.T) {
	// The handler has no agent store and an empty provider factory: reaching
	// for either would fail differently (or panic), which is the point.
	handler := NewLLMTaskHandler(nil, llm.NewFactory(), nil)
	for _, recipe := range []any{FirstTaskRecipeSoftwareAssessment, "some_future_recipe", 12} {
		task := Task{ID: "t", WorkspaceID: "ws", Description: "assess", Context: map[string]any{TaskContextFirstTaskRecipe: recipe}}
		result, err := handler.ExecuteTask(context.Background(), "Any Agent", task)
		var blocked *TaskBlockedError
		if !errors.As(err, &blocked) || blocked.ReasonCode != "assessment_unavailable" || result != "" {
			t.Fatalf("recipe %v => %q, %v; want a blocked task", recipe, result, err)
		}
	}
}
