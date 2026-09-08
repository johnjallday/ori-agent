package settingsreset

import "slices"

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
	if op.SchemaVersion != SchemaVersion || op.State != StateCompleted || len(op.Blockers) != 0 || len(op.Results) == 0 {
		return false
	}
	resultIDs := make([]CategoryID, 0, len(op.Results))
	for _, result := range op.Results {
		if !verifiedCategory(result) {
			return false
		}
		resultIDs = append(resultIDs, result.ID)
	}
	selectionInput := resultIDs
	if op.Intent == IntentStartFresh {
		selectionInput = nil
	}
	expected, err := Selection(op.Intent, selectionInput)
	return err == nil && slices.Equal(expected, resultIDs)
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
