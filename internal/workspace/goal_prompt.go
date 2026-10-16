package workspace

// GoalPromptLine renders a workspace goal as the one-line "Goal: <text>" that
// workspace-scoped prompts carry: flattened to one line and bounded by
// WorkspaceGoalPromptLimit. Empty when there is no goal.
func GoalPromptLine(mission string) string {
	goal := sanitizeTaskPromptText(mission, WorkspaceGoalPromptLimit)
	if goal == "" {
		return ""
	}
	return "Goal: " + goal
}
