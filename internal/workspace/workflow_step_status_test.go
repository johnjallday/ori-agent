package workspace

import (
	"errors"
	"testing"
)

func TestWorkflowStep_SetStatus_LegalProgression(t *testing.T) {
	step := &WorkflowStep{ID: "s1", Status: StepStatusPending}

	for _, target := range []StepStatus{
		StepStatusReady,
		StepStatusInProgress,
		StepStatusCompleted,
	} {
		if err := step.SetStatus(target); err != nil {
			t.Fatalf("SetStatus(%q) on %q: unexpected error %v", target, step.Status, err)
		}
		if step.Status != target {
			t.Fatalf("expected status %q, got %q", target, step.Status)
		}
	}
}

func TestWorkflowStep_SetStatus_RejectsSameState(t *testing.T) {
	step := &WorkflowStep{ID: "s1", Status: StepStatusReady}
	err := step.SetStatus(StepStatusReady)
	if err == nil {
		t.Fatal("expected SetStatus to reject same-state transition")
	}
	var illegal *IllegalStepTransitionError
	if !errors.As(err, &illegal) {
		t.Fatalf("expected *IllegalStepTransitionError, got %T", err)
	}
}

func TestWorkflowStep_SetStatus_RejectsCompletedToFailed(t *testing.T) {
	step := &WorkflowStep{ID: "s1", Status: StepStatusCompleted}
	if err := step.SetStatus(StepStatusFailed); err == nil {
		t.Fatal("expected Completed → Failed to be rejected")
	}
	if step.Status != StepStatusCompleted {
		t.Fatalf("step status mutated despite rejected transition: %q", step.Status)
	}
}

func TestWorkflowStep_SetStatus_RejectsTerminalToInProgressDirectly(t *testing.T) {
	// Cancelled is fully terminal except for an explicit reset to Pending —
	// going straight to InProgress without resetting first should be rejected.
	step := &WorkflowStep{ID: "s1", Status: StepStatusCancelled}
	if err := step.SetStatus(StepStatusInProgress); err == nil {
		t.Fatal("expected Cancelled → InProgress to be rejected")
	}
}

func TestWorkflowStep_SetStatus_AllowsResetFromCompleted(t *testing.T) {
	step := &WorkflowStep{ID: "s1", Status: StepStatusCompleted}
	if err := step.SetStatus(StepStatusPending); err != nil {
		t.Fatalf("Completed → Pending reset rejected: %v", err)
	}
}

func TestWorkflowStep_SetStatus_FromZeroValue(t *testing.T) {
	step := &WorkflowStep{ID: "s1"} // Status == ""
	if err := step.SetStatus(StepStatusPending); err != nil {
		t.Fatalf("'' → Pending should be allowed: %v", err)
	}
}

func TestWorkflowStep_ForceStatus_BypassesTable(t *testing.T) {
	// ForceStatus is the documented escape hatch for orphan cleanup. Verify
	// it sets the status without checking the legality table.
	step := &WorkflowStep{ID: "s1", Status: StepStatusCancelled}
	step.ForceStatus(StepStatusInProgress)
	if step.Status != StepStatusInProgress {
		t.Fatalf("ForceStatus did not assign: got %q", step.Status)
	}
}

func TestWorkspace_MutateWorkflowStep_AppliesFn(t *testing.T) {
	ws := &Workspace{ID: "ws1"}
	if err := ws.AddWorkflow(Workflow{
		ID: "wf1",
		Steps: []WorkflowStep{
			{ID: "s1", Status: StepStatusPending},
		},
	}); err != nil {
		t.Fatalf("AddWorkflow: %v", err)
	}

	if err := ws.MutateWorkflowStep("wf1", "s1", func(s *WorkflowStep) error {
		return s.SetStatus(StepStatusReady)
	}); err != nil {
		t.Fatalf("MutateWorkflowStep: %v", err)
	}

	got := ws.Workflows["wf1"].Steps[0].Status
	if got != StepStatusReady {
		t.Fatalf("expected Ready, got %q", got)
	}
}

func TestWorkspace_MutateWorkflowStep_RollsBackOnFnError(t *testing.T) {
	ws := &Workspace{ID: "ws1"}
	if err := ws.AddWorkflow(Workflow{
		ID: "wf1",
		Steps: []WorkflowStep{
			{ID: "s1", Status: StepStatusPending, Result: "before"},
		},
	}); err != nil {
		t.Fatalf("AddWorkflow: %v", err)
	}

	sentinel := errors.New("fn refused")
	err := ws.MutateWorkflowStep("wf1", "s1", func(s *WorkflowStep) error {
		// fn mutates and then returns an error. The test asserts that the
		// caller sees the error AND that a partial mutation is NOT discarded
		// — this matches MutateTask semantics: fn-error short-circuits the
		// UpdatedAt bump and the writeback, but step-field mutations made on
		// the live slice element before the return do persist. The contract
		// is "fn must self-rollback on error"; we pin it down here.
		s.Result = "during"
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if ws.Workflows["wf1"].Steps[0].Result != "during" {
		t.Fatal("expected fn's pre-error mutation to be visible (caller-rollback contract)")
	}
}

func TestWorkspace_MutateWorkflowStep_UnknownIDs(t *testing.T) {
	ws := &Workspace{ID: "ws1"}
	if err := ws.AddWorkflow(Workflow{
		ID:    "wf1",
		Steps: []WorkflowStep{{ID: "s1"}},
	}); err != nil {
		t.Fatalf("AddWorkflow: %v", err)
	}
	if err := ws.MutateWorkflowStep("missing", "s1", func(*WorkflowStep) error { return nil }); err == nil {
		t.Fatal("expected unknown workflow to error")
	}
	if err := ws.MutateWorkflowStep("wf1", "missing", func(*WorkflowStep) error { return nil }); err == nil {
		t.Fatal("expected unknown step to error")
	}
}

func TestWorkspace_MutateWorkflowStep_NilFn(t *testing.T) {
	ws := &Workspace{ID: "ws1"}
	if err := ws.AddWorkflow(Workflow{ID: "wf1", Steps: []WorkflowStep{{ID: "s1"}}}); err != nil {
		t.Fatalf("AddWorkflow: %v", err)
	}
	if err := ws.MutateWorkflowStep("wf1", "s1", nil); err == nil {
		t.Fatal("expected nil fn to error")
	}
}
