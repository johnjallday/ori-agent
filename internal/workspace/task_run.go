package workspace

import "context"

// TaskRunResult carries the user-facing task result plus the durable Workspace
// Run that produced it, when the handler is backed by the harness.
type TaskRunResult struct {
	Result string
	RunID  string
}

// RunAwareTaskHandler extends TaskHandler for implementations that execute
// through Workspace Runs and can expose the backing run ID to callers.
type RunAwareTaskHandler interface {
	TaskHandler
	ExecuteTaskRun(ctx context.Context, agentName string, task Task) (TaskRunResult, error)
}

// ExecuteTaskWithRunMetadata keeps existing TaskHandler implementations working
// while allowing run-backed handlers to return the backing Run ID.
func ExecuteTaskWithRunMetadata(ctx context.Context, handler TaskHandler, agentName string, task Task) (TaskRunResult, error) {
	if runAware, ok := handler.(RunAwareTaskHandler); ok {
		return runAware.ExecuteTaskRun(ctx, agentName, task)
	}
	result, err := handler.ExecuteTask(ctx, agentName, task)
	return TaskRunResult{Result: result}, err
}
