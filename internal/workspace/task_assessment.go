package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/llm"
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

	// TaskContextFirstTaskFailure is the code of why a recipe task's last run
	// produced no result. The server writes it when a run fails and clears it
	// when the next one starts.
	TaskContextFirstTaskFailure = "folder_first_task_failure"

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

// RecipeBrief is everything a recipe gives a model: the host's own
// instructions and one message. There is nothing else in the exchange.
type RecipeBrief struct {
	System string
	User   string
	// State is the runner's own record of the brief, handed back to Finish.
	State any
}

// RecipeRunner is a host recipe: it decides what a task under it is given and
// what becomes of the model's reply. It never receives an agent's tools,
// prompt, skills or permissions, because a recipe run has none of them.
type RecipeRunner interface {
	// Ready reports, cheaply and without opening a file or calling a model, a
	// reason the task could not be run now that the user can repair (the folder
	// it was seeded to read is gone, for example). nil when there is none.
	Ready(task Task) *TaskBlockedError
	// Brief re-checks, at the moment of the run, that the task may still read
	// what it was seeded to read, and returns the exchange for a model whose
	// context window is contextWindow tokens (0 when unknown). An error that is
	// a *TaskBlockedError says what the user can repair; nothing has been sent
	// to a model when Brief fails.
	Brief(ctx context.Context, task Task, contextWindow int) (RecipeBrief, error)
	// Finish turns the model's reply into the task's stored result.
	Finish(brief RecipeBrief, reply string) (string, error)
}

// SetRecipeRunner registers the host recipe of that name. Call it while wiring,
// before any task runs.
func (h *LLMTaskHandler) SetRecipeRunner(recipe string, runner RecipeRunner) {
	if h == nil || strings.TrimSpace(recipe) == "" || runner == nil {
		return
	}
	if h.recipes == nil {
		h.recipes = map[string]RecipeRunner{}
	}
	h.recipes[strings.TrimSpace(recipe)] = runner
}

// CanRunRecipe reports whether a task seeded under the recipe could be run
// here at all. It says nothing about any one workspace's model.
func (h *LLMTaskHandler) CanRunRecipe(recipe string) bool {
	return h != nil && h.recipes[strings.TrimSpace(recipe)] != nil
}

// Reasons a recipe task cannot be run. They are codes the surfaces that show
// the task branch on; each has one plain sentence.
const (
	RecipeUnavailable      = "assessment_unavailable"
	RecipeAssistantMissing = "assistant_unavailable"
	RecipeNoModel          = "no_model"
	RecipeModelUnavailable = "model_unavailable"
	RecipeModelUnsupported = "model_unsupported"
	RecipeToolCallRefused  = "tool_call_refused"
)

// agentsRoute is where an agent's model is chosen.
const agentsRoute = "/agents"

// recipeProvider is the one model a recipe run would use.
type recipeProvider struct {
	provider llm.Provider
	name     string
	model    string
	effort   string
}

// resolveRecipeProvider reads which model the task's assistant would answer
// with, and refuses, before anything is read or sent, a model that cannot be
// held to an isolated call. Only the assistant's choice of model is taken from
// it: its prompt, tools, skills and permissions play no part in a recipe run.
func (h *LLMTaskHandler) resolveRecipeProvider(agentName string, task Task, recipe string) (recipeProvider, *TaskBlockedError) {
	nothingSent := " Nothing was read from the project and nothing was sent to a model."
	if !h.CanRunRecipe(recipe) || h.llmFactory == nil {
		return recipeProvider{}, &TaskBlockedError{
			ReasonCode: RecipeUnavailable,
			Reason:     "This assessment cannot be run on this server." + nothingSent,
		}
	}
	ag, err := h.resolveExecutionAgent(agentName, task)
	if err != nil || ag == nil || ag.Agent == nil {
		return recipeProvider{}, &TaskBlockedError{
			ReasonCode: RecipeAssistantMissing,
			Reason:     "The assistant this assessment is assigned to is not available." + nothingSent,
			Question:   "Assign the task to an assistant in this workspace, then try again.",
		}
	}
	model := strings.TrimSpace(ag.Settings.Model)
	if model == "" {
		return recipeProvider{}, &TaskBlockedError{
			ReasonCode: RecipeNoModel,
			Reason:     strings.TrimSpace(agentName) + " has no model chosen." + nothingSent,
			Question:   "Choose a model for the assistant, then try again.",
			Repair:     &TaskRepairAction{Code: "choose_agent_model", Label: "Choose a model", URL: agentsRoute},
		}
	}
	name := h.getProviderForAgent(ag.Settings.Provider, model)
	provider, err := h.llmFactory.GetProvider(name)
	if err != nil || provider == nil {
		return recipeProvider{}, &TaskBlockedError{
			ReasonCode: RecipeModelUnavailable,
			Reason:     "The model " + strings.TrimSpace(agentName) + " is set to use is not set up on this computer." + nothingSent,
			Question:   "Set that model up, or choose another for the assistant, then try again.",
			Repair:     &TaskRepairAction{Code: "choose_agent_model", Label: "Choose a model", URL: agentsRoute},
		}
	}
	// The restriction is the provider's to guarantee. One that cannot is not
	// asked to run the call some other way.
	if !provider.Capabilities().SupportsIsolatedChat {
		return recipeProvider{}, recipeModelUnsupported(agentName, nothingSent)
	}
	return recipeProvider{
		provider: provider, name: name, model: h.normalizeModelForProvider(name, model), effort: ag.Settings.ReasoningEffort,
	}, nil
}

func recipeModelUnsupported(agentName, nothingSent string) *TaskBlockedError {
	return &TaskBlockedError{
		ReasonCode: RecipeModelUnsupported,
		Reason:     "The model " + strings.TrimSpace(agentName) + " is set to use cannot be held to a read-only assessment: Ori cannot switch its tools off." + nothingSent,
		Question:   "Choose another model for the assistant, then try again.",
		Repair:     &TaskRepairAction{Code: "choose_agent_model", Label: "Choose a model", URL: agentsRoute},
	}
}

// RecipeReadiness reports, without reading or sending anything, why a task
// under a host recipe could not be run by its assignee right now: "" when it
// could. It answers from the same checks the run itself makes first, so a
// surface that offers Start never offers one the run would refuse for a
// reason known in advance.
func (h *LLMTaskHandler) RecipeReadiness(task Task) string {
	recipe := FirstTaskRecipe(&task)
	if recipe == "" {
		return ""
	}
	if h == nil {
		return RecipeUnavailable
	}
	if _, blocked := h.resolveRecipeProvider(task.To, task, recipe); blocked != nil {
		return blocked.ReasonCode
	}
	if blocked := h.recipes[recipe].Ready(task); blocked != nil {
		return blocked.ReasonCode
	}
	return ""
}

// executeRecipeTask runs a task seeded under a host recipe: one model call,
// with what the recipe lays out and nothing else.
//
// Such a task never falls through to the ordinary agent path, whose tools and
// permissions belong to the agent rather than to the recipe. The call is made
// with ChatRequest.Isolated: no tool definitions, no MCP servers, no workspace
// and no execution scope are sent, so none of the paths that give a model
// authority here can be taken; the provider must guarantee the rest or refuse.
// There is no fallback to another provider and no second attempt. A reply
// that asks to use a tool is not acted on: the run fails.
func (h *LLMTaskHandler) executeRecipeTask(ctx context.Context, agentName string, task Task, recipe string) (string, error) {
	chosen, blocked := h.resolveRecipeProvider(agentName, task, recipe)
	if blocked != nil {
		return "", blocked
	}
	runner := h.recipes[recipe]
	window := llm.ResolveModelContextWindow(chosen.provider, chosen.model)
	brief, err := runner.Brief(ctx, task, window)
	if err != nil {
		return "", err
	}
	if h.eventBus != nil {
		h.eventBus.Publish(NewTaskEvent(EventTaskThinking, task.WorkspaceID, task.ID, agentName, map[string]any{
			"phase":   "awaiting_llm",
			"message": "Reading what Ori gathered from the project. Nothing is run and no tools are available.",
		}))
	}
	response, err := chosen.provider.Chat(ctx, llm.ChatRequest{
		Model:               chosen.model,
		Messages:            []llm.Message{llm.NewSystemMessage(brief.System), llm.NewUserMessage(brief.User)},
		ContextWindowTokens: window,
		ReasoningEffort:     chosen.effort,
		Isolated:            true,
	})
	if errors.Is(err, llm.ErrIsolationUnsupported) {
		return "", recipeModelUnsupported(agentName, " Nothing was sent to it.")
	}
	if err != nil {
		return "", err
	}
	if response == nil {
		return "", errors.New("the model returned no response")
	}
	h.recordTaskUsage(chosen.name, chosen.model, agentName, response.Usage)
	if len(response.ToolCalls) > 0 {
		return "", &TaskBlockedError{
			ReasonCode: RecipeToolCallRefused,
			Reason:     "The model asked to use a tool, which a read-only assessment does not allow. Nothing was run, and its answer was discarded.",
			Question:   "Try again, or choose another model for the assistant.",
		}
	}
	return runner.Finish(brief, response.Content)
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
	case TaskContextFirstTaskRecipe, TaskContextFirstTaskRecipeVersion, TaskContextFirstTaskFailure:
		return true
	}
	return false
}
