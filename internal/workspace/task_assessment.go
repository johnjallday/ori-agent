package workspace

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// A folder setup may seed a first task that runs under a host-owned recipe
// instead of as an ordinary agent task. The recipe is named in the task's
// context by the server when it seeds the task, and it decides how the task may
// run: what it is given, and what it is refused.
const (
	// TaskContextFirstTaskRecipe is the context key that names the recipe. Only
	// the server writes it; the task create and update handlers refuse it.
	TaskContextFirstTaskRecipe = "folder_first_task_recipe"
	// TaskContextFirstTaskRecipeVersion is the recipe's version when the task
	// was seeded.
	TaskContextFirstTaskRecipeVersion = "folder_first_task_recipe_version"

	// FirstTaskRecipeSoftwareAssessment is the read-only first assessment of a
	// software project.
	FirstTaskRecipeSoftwareAssessment = "software_assessment"
)

// FirstTaskRecipe returns the host recipe a task runs under, or "" for an
// ordinary task. A value that is not a non-empty string is reported as an
// unknown recipe rather than as none, so a damaged marker never turns a
// restricted task into an ordinary one.
func FirstTaskRecipe(task *Task) string {
	if task == nil {
		return ""
	}
	raw, present := task.Context[TaskContextFirstTaskRecipe]
	if !present {
		return ""
	}
	recipe, _ := raw.(string)
	if recipe = strings.TrimSpace(recipe); recipe != "" {
		return recipe
	}
	return "unknown"
}

// executeRecipeTask runs a task seeded under a host recipe. Such a task never
// falls through to the ordinary agent path, whose tools and permissions belong
// to the agent rather than to the recipe: a recipe this handler cannot run
// under its own restriction is refused, and nothing is sent to a model.
func (h *LLMTaskHandler) executeRecipeTask(_ string, _ Task, _ string) (string, error) {
	return "", &TaskBlockedError{
		ReasonCode: "assessment_unavailable",
		Reason:     "This first assessment cannot run here yet. Nothing was sent to a model.",
		Question:   "The assessment stays ready to start once it is available.",
	}
}

// AddRecipeTask adds a task that runs under a host recipe. It is the one way a
// recipe marker reaches a task: AddTask and AddTasks drop the marker from
// whatever they are given, so a task made through a delegation, a chat tool or
// an import can never claim a recipe, or the looser handling a future recipe
// might have.
func (w *Workspace) AddRecipeTask(task Task, recipe string, version int) (string, error) {
	recipe = strings.TrimSpace(recipe)
	if recipe == "" || version < 1 {
		return "", fmt.Errorf("a recipe task needs a recipe and its version")
	}
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	if err := w.AddTask(task); err != nil {
		return "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	index, found := w.taskIndex[task.ID]
	if !found || index >= len(w.Tasks) {
		return "", fmt.Errorf("the recipe task was not stored")
	}
	if w.Tasks[index].Context == nil {
		w.Tasks[index].Context = map[string]any{}
	}
	w.Tasks[index].Context[TaskContextFirstTaskRecipe] = recipe
	w.Tasks[index].Context[TaskContextFirstTaskRecipeVersion] = version
	return task.ID, nil
}

// withoutReservedTaskContext returns context without the keys only the server
// may write. The caller's map is left as it was.
func withoutReservedTaskContext(context map[string]any) map[string]any {
	reserved := false
	for key := range context {
		reserved = reserved || ReservedTaskContextKey(key)
	}
	if !reserved {
		return context
	}
	kept := make(map[string]any, len(context))
	for key, value := range context {
		if !ReservedTaskContextKey(key) {
			kept[key] = value
		}
	}
	return kept
}

// ReservedTaskContextKey reports a context key only the server may write.
func ReservedTaskContextKey(key string) bool {
	switch strings.TrimSpace(key) {
	case TaskContextFirstTaskRecipe, TaskContextFirstTaskRecipeVersion:
		return true
	}
	return false
}
