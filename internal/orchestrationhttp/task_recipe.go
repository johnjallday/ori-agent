package orchestrationhttp

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// executeRecipeTaskOnce makes the single call a task under a host recipe is.
//
// It deliberately has none of what surrounds an ordinary task's run: no attempt
// loop, no reading of the answer's wording to decide the model "needs input",
// no step expansion. The recipe is given the task as it was seeded.
//
// A recipe asks the user nothing, so its run never ends "waiting for your
// answer". Whatever stopped it, including something the user can repair, comes
// back from the task handler as a workspace.RecipeRunError: one plain sentence,
// which is what is recorded on the task, and a code, which is kept beside it so
// the surfaces that show the task can name the repair. Trying again is the
// user's own click.
func (th *TaskHandler) executeRecipeTaskOnce(ctx context.Context, persisted *workspace.Task, forExecution workspace.Task) (string, error) {
	run, err := workspace.ExecuteTaskWithRunMetadata(ctx, th.taskHandler, forExecution.To, forExecution)
	if run.RunID != "" {
		persisted.CurrentRunID = run.RunID
	}
	if err == nil && strings.TrimSpace(run.Result) != "" {
		return run.Result, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "", context.Canceled
	}

	failure, described := workspace.AsRecipeRunError(err)
	if !described {
		// The handler describes every failure of its own. One that reaches here
		// undescribed came from around it (the run could not be recorded, say),
		// and its text is no more fit to show than a provider's.
		cause := "no result"
		if err != nil {
			cause = err.Error()
		}
		logger.Warn("Recipe task did not finish", logger.Fields{"task_id": persisted.ID, "reason": workspace.RecipeRunFailed, "error": cause})
		failure = &workspace.RecipeRunError{
			Code:     workspace.RecipeRunFailed,
			Sentence: "The assessment did not finish. Nothing in the project was run or changed.",
		}
	}
	if persisted.Context == nil {
		persisted.Context = map[string]any{}
	}
	persisted.Context[workspace.TaskContextFirstTaskFailure] = failure.Code
	return "", failure
}
