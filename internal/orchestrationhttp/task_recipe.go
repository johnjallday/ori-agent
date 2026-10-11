package orchestrationhttp

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// recipeFailure is a recipe run that produced no result. Its text is the one
// plain sentence recorded on the task; the cause stays in the log.
type recipeFailure struct{ sentence string }

func (f *recipeFailure) Error() string { return f.sentence }

// executeRecipeTaskOnce makes the single call a task under a host recipe is.
//
// It deliberately has none of what surrounds an ordinary task's run: no attempt
// loop, no reading of the answer's wording to decide the model "needs input",
// no step expansion. The recipe is given the task as it was seeded.
//
// A recipe asks the user nothing, so its run never ends "waiting for your
// answer". Whatever stopped it, including something the user can repair, is
// recorded as a failure that says what happened, and trying again is the
// user's own click. The reason's code is kept beside it so the surfaces that
// show the task can name the repair.
func (th *TaskHandler) executeRecipeTaskOnce(ctx context.Context, persisted *workspace.Task, forExecution workspace.Task) (string, error) {
	run, err := workspace.ExecuteTaskWithRunMetadata(ctx, th.taskHandler, forExecution.To, forExecution)
	if run.RunID != "" {
		persisted.CurrentRunID = run.RunID
	}
	if err == nil && strings.TrimSpace(run.Result) != "" {
		return run.Result, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "", context.Canceled
	}

	code, sentence := "run_failed", "The assessment did not finish. Nothing in the project was run or changed."
	var blocked *workspace.TaskBlockedError
	switch {
	case err == nil:
		code, sentence = "empty_result", "The model returned nothing to report. Nothing in the project was run or changed."
	case errors.As(err, &blocked):
		if strings.TrimSpace(blocked.ReasonCode) != "" {
			code = strings.TrimSpace(blocked.ReasonCode)
		}
		if strings.TrimSpace(blocked.Reason) != "" {
			sentence = strings.TrimSpace(blocked.Reason)
		}
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		code, sentence = "timed_out", "The assessment ran out of time before the model answered. Nothing in the project was run or changed."
	default:
		// A failure the provider layer can name (no quota, not signed in, the
		// model is unknown) is said in its words; one it cannot name is not
		// dressed up as anything more than "did not finish".
		if classified := llm.ClassifyProviderError("", err, 0, ""); classified != nil &&
			classified.Category != llm.CategoryUnknown && strings.TrimSpace(classified.Message) != "" {
			code = reasonCodeFor(classified.Category)
			sentence = strings.TrimSpace(classified.Message) + " Nothing in the project was run or changed."
		}
	}
	cause := "no result"
	if err != nil {
		cause = err.Error()
	}
	logger.Warn("Recipe task did not finish", logger.Fields{"task_id": persisted.ID, "reason": code, "error": cause})
	if persisted.Context == nil {
		persisted.Context = map[string]any{}
	}
	persisted.Context[workspace.TaskContextFirstTaskFailure] = code
	return "", &recipeFailure{sentence: sentence}
}
