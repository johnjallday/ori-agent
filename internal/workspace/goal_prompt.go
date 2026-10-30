package workspace

// GoalPromptLine renders a workspace goal as the one-line "Goal: <text>" that
// workspace-scoped prompts carry: the goal's Outcome line (GoalOutcomeLine),
// flattened to one line and bounded by WorkspaceGoalPromptLimit. Empty when
// there is no goal.
func GoalPromptLine(mission string) string {
	goal := GoalOutcomeLine(mission)
	if goal == "" {
		return ""
	}
	return "Goal: " + goal
}
