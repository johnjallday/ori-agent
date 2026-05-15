package workspacerun

import "context"

type ContextPreparer interface {
	PrepareContext(ctx context.Context, run *Run) (*PreparedContext, error)
}

func DefaultTaskContextPlan() ContextPlan {
	return ContextPlan{
		Strategy:                 "task_default",
		IncludeWorkspaceSnapshot: true,
		IncludeAttachedFiles:     true,
		ExposeWorkspaceTools:     true,
	}
}
