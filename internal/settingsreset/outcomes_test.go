package settingsreset

import "testing"

func TestOutcomesRequireActualNamedPostconditions(t *testing.T) {
	def, _ := definition(CategoryAgents)
	result := CategoryResult{ID: CategoryAgents, Outcome: OutcomeCompleted}
	for _, name := range def.Checks {
		result.Checks = append(result.Checks, CheckResult{Name: name, Outcome: OutcomeCompleted})
	}
	op := Operation{SchemaVersion: SchemaVersion, Intent: IntentSelectedData, State: StateCompleted, Results: []CategoryResult{result}}
	if !op.VerifiedComplete() || len(op.CompletedCategories()) != 1 {
		t.Fatal("verified category was not recognized")
	}
	for _, state := range []OperationState{StatePreparing, StateAwaitingRestart, StateVerifying, StatePartialFailure, StateBlocked, StateInterrupted} {
		op.State = state
		if op.VerifiedComplete() {
			t.Fatal("non-completion state became success:", state)
		}
	}
	op.State = StateCompleted
	for _, outcome := range []Outcome{OutcomePending, OutcomeFailed, OutcomeSkipped, OutcomeUnknown, OutcomePreserved} {
		op.Results[0].Checks[0].Outcome = outcome
		if op.VerifiedComplete() || len(op.CompletedCategories()) != 0 {
			t.Fatal("unverified check became a completed category:", outcome)
		}
	}
}
