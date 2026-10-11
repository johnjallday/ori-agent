package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
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
	// How a run ended without a report, when nothing more specific is known.
	RecipeRunFailed      = "run_failed"
	RecipeEmptyResult    = "empty_result"
	RecipeTimedOut       = "timed_out"
	RecipeAlreadyRunning = "already_running"
)

// RecipeRunError is how a run under a host recipe that produced no report is
// returned, to every caller. Its text is one plain sentence that is safe to
// store on the task and show in a browser; Code names the reason for surfaces
// that offer a repair. What actually went wrong (a provider's own error, which
// can carry paths, command output or account details) is written to the log
// and nowhere else.
//
// A recipe asks the user nothing, so a run never ends "waiting for an answer":
// whatever stopped it, including something the user can repair, is this error.
type RecipeRunError struct {
	Code     string
	Sentence string
}

func (e *RecipeRunError) Error() string { return e.Sentence }

// AsRecipeRunError returns the recipe failure in err's chain, if there is one.
func AsRecipeRunError(err error) (*RecipeRunError, bool) {
	var failure *RecipeRunError
	if errors.As(err, &failure) && failure != nil {
		return failure, true
	}
	return nil, false
}

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
	unavailable := &TaskBlockedError{
		ReasonCode: RecipeModelUnavailable,
		Reason:     "The model " + strings.TrimSpace(agentName) + " is set to use is not set up on this computer." + nothingSent,
		Question:   "Set that model up, or choose another for the assistant, then try again.",
		Repair:     &TaskRepairAction{Code: "choose_agent_model", Label: "Choose a model", URL: agentsRoute},
	}
	// The assistant's own provider, or none. An ordinary task whose provider is
	// not set up here is moved to whichever provider the model's name suggests;
	// a recipe run is not, because that other provider may bill differently or
	// be one the user meant to keep this assistant away from.
	if chosen := strings.ToLower(strings.TrimSpace(ag.Settings.Provider)); chosen != "" && !h.llmFactory.HasProvider(chosen) {
		return recipeProvider{}, unavailable
	}
	name := h.getProviderForAgent(ag.Settings.Provider, model)
	provider, err := h.llmFactory.GetProvider(name)
	if err != nil || provider == nil {
		return recipeProvider{}, unavailable
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

// executeRecipeTask is the one way a task seeded under a host recipe is run,
// whichever executor asked: the manual run, the scheduler, the background
// poller, a workspace orchestrator, or another task that lists this one as an
// input. It makes one model call, with what the recipe lays out and nothing
// else, and holds every caller to the same three things:
//
//   - one run of a task at a time: a second start while one is going is
//     refused here, before anything is read or sent;
//   - a failure is a RecipeRunError: one plain sentence and a code, with the
//     cause in the log, so no caller can store or show a provider's raw error;
//   - no caller's retry, delegation or step machinery is given a result to
//     work on: there is a report, or there is that failure.
func (h *LLMTaskHandler) executeRecipeTask(ctx context.Context, agentName string, task Task, recipe string) (string, error) {
	if !h.claimRecipeRun(task.ID) {
		return "", &RecipeRunError{Code: RecipeAlreadyRunning, Sentence: "This assessment is already running. A second one was not started."}
	}
	defer h.releaseRecipeRun(task.ID)

	report, err := h.runRecipeTask(ctx, agentName, task, recipe)
	if err == nil && strings.TrimSpace(report) != "" {
		return report, nil
	}
	// A run the user cancelled is a cancellation, not a failure to describe.
	if errors.Is(ctx.Err(), context.Canceled) {
		return "", context.Canceled
	}
	failure := describeRecipeFailure(ctx, err)
	cause := "no result"
	if err != nil {
		cause = err.Error()
	}
	logger.Warn("Recipe task did not finish", logger.Fields{"task_id": task.ID, "recipe": recipe, "reason": failure.Code, "error": cause})
	return "", failure
}

// claimRecipeRun marks a recipe task as running. It answers false when that
// task already is, from any executor.
func (h *LLMTaskHandler) claimRecipeRun(taskID string) bool {
	h.recipeRunsMu.Lock()
	defer h.recipeRunsMu.Unlock()
	if _, running := h.recipeRuns[taskID]; running {
		return false
	}
	if h.recipeRuns == nil {
		h.recipeRuns = map[string]struct{}{}
	}
	h.recipeRuns[taskID] = struct{}{}
	return true
}

func (h *LLMTaskHandler) releaseRecipeRun(taskID string) {
	h.recipeRunsMu.Lock()
	defer h.recipeRunsMu.Unlock()
	delete(h.recipeRuns, taskID)
}

// describeRecipeFailure turns whatever stopped a recipe run into the sentence
// and code that may leave this package. A reason the run itself named (the
// folder is gone, the model cannot be isolated) is kept in its own words. A
// provider's error is said in the provider layer's classified words when it
// has some, and otherwise only as "did not finish": its text is never passed on.
func describeRecipeFailure(ctx context.Context, err error) *RecipeRunError {
	const unchanged = " Nothing in the project was run or changed."
	var blocked *TaskBlockedError
	var already *RecipeRunError
	switch {
	case err == nil:
		return &RecipeRunError{Code: RecipeEmptyResult, Sentence: "The model returned nothing to report." + unchanged}
	case errors.As(err, &already) && already != nil:
		return already
	case errors.As(err, &blocked) && blocked != nil:
		failure := &RecipeRunError{Code: RecipeRunFailed, Sentence: "The assessment did not finish." + unchanged}
		if code := strings.TrimSpace(blocked.ReasonCode); code != "" {
			failure.Code = code
		}
		if reason := strings.TrimSpace(blocked.Reason); reason != "" {
			failure.Sentence = reason
		}
		return failure
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		return &RecipeRunError{Code: RecipeTimedOut, Sentence: "The assessment ran out of time before the model answered." + unchanged}
	}
	if classified := llm.ClassifyProviderError("", err, 0, ""); classified != nil &&
		classified.Category != llm.CategoryUnknown && strings.TrimSpace(classified.Message) != "" {
		return &RecipeRunError{Code: recipeFailureCode(classified.Category), Sentence: strings.TrimSpace(classified.Message) + unchanged}
	}
	return &RecipeRunError{Code: RecipeRunFailed, Sentence: "The assessment did not finish." + unchanged}
}

// recipeFailureCode names a classified provider failure with the same codes
// the ordinary task path uses for it.
func recipeFailureCode(category llm.ErrorCategory) string {
	switch category {
	case llm.CategoryQuotaExhausted:
		return "provider_quota_exhausted"
	case llm.CategoryAuthentication, llm.CategoryNotConfigured, llm.CategoryPermissionDenied:
		return "provider_configuration_required"
	case llm.CategoryInvalidModel:
		return RecipeModelUnavailable
	case llm.CategoryContextLength:
		return "input_too_large"
	case llm.CategoryInvalidRequest, llm.CategoryPolicyRejection:
		return "request_rejected"
	default:
		return "execution_failed"
	}
}

// runRecipeTask makes the call. Such a task never falls through to the
// ordinary agent path, whose tools and permissions belong to the agent rather
// than to the recipe. The call is made with ChatRequest.Isolated: no tool
// definitions, no MCP servers, no workspace and no execution scope are sent,
// so none of the paths that give a model authority here can be taken; the
// provider must guarantee the rest or refuse. The recipe's instructions travel
// as the request's system prompt, which every provider sends. There is no
// fallback to another provider and no second attempt. A reply that asks to use
// a tool is not acted on: the run fails.
func (h *LLMTaskHandler) runRecipeTask(ctx context.Context, agentName string, task Task, recipe string) (string, error) {
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
		SystemPrompt:        brief.System,
		Messages:            []llm.Message{llm.NewUserMessage(brief.User)},
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
