package settingsreset

// CompletedCategories includes only named, verified postconditions, never a
// selection, a pending restart, or a category with a contradictory check.
func (op Operation) CompletedCategories() []CategoryID {
	completed := []CategoryID{}
	for _, result := range op.Results {
		if result.Outcome == OutcomeCompleted && verifiedCategory(result) {
			completed = append(completed, result.ID)
		}
	}
	return completed
}

func (op Operation) VerifiedComplete() bool {
	if op.SchemaVersion != SchemaVersion || op.Intent != IntentSelectedData || op.State != StateCompleted || len(op.Blockers) != 0 || len(op.Results) == 0 {
		return false
	}
	ids := make(map[CategoryID]bool)
	for _, result := range op.Results {
		if ids[result.ID] || !verifiedCategory(result) {
			return false
		}
		ids[result.ID] = true
	}
	return true
}

func verifiedCategory(result CategoryResult) bool {
	def, ok := definition(result.ID)
	if !ok || (result.Outcome != OutcomeCompleted && result.Outcome != OutcomePreserved) || result.Retryable || len(result.Checks) != len(def.Checks) {
		return false
	}
	for i, check := range result.Checks {
		if check.Name != def.Checks[i] || check.Outcome != OutcomeCompleted {
			return false
		}
	}
	return true
}
