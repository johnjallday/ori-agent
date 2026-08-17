package workspace

import "testing"

type capabilityEvaluation struct {
	workspaceID string
	capability  string
}

type recordingCapabilityEvaluator struct {
	claims map[string]*TaskBlockedError
	seen   []capabilityEvaluation
}

func (e *recordingCapabilityEvaluator) EvaluateTaskCapability(workspaceID, capability string) (bool, *TaskBlockedError) {
	e.seen = append(e.seen, capabilityEvaluation{workspaceID: workspaceID, capability: capability})
	blocked, claimed := e.claims[capability]
	return claimed, blocked
}

func TestCompositeTaskCapabilityGateClaimsDeterministically(t *testing.T) {
	first := &recordingCapabilityEvaluator{claims: map[string]*TaskBlockedError{
		"email":   nil,
		"runtime": {ReasonCode: "first_runtime_blocker", Reason: "Repair runtime."},
	}}
	second := &recordingCapabilityEvaluator{claims: map[string]*TaskBlockedError{
		"runtime": {ReasonCode: "wrong_evaluator", Reason: "Must not run."},
	}}
	gate := NewCompositeTaskCapabilityGate(nil, first, second)

	blocked := gate.CheckTaskCapabilities("ws-1", []string{" Email ", "email", "RUNTIME", "later"})
	if blocked == nil || blocked.ReasonCode != "first_runtime_blocker" {
		t.Fatalf("first claimed blocker = %+v", blocked)
	}
	if len(first.seen) != 2 || first.seen[0].capability != "email" || first.seen[1].capability != "runtime" {
		t.Fatalf("capability order/dedup changed: %+v", first.seen)
	}
	if len(second.seen) != 0 {
		t.Fatalf("a second evaluator reinterpreted an already-claimed key: %+v", second.seen)
	}
}

func TestCompositeTaskCapabilityGateIgnoresUnclaimedPlanningCapabilities(t *testing.T) {
	evaluator := &recordingCapabilityEvaluator{claims: map[string]*TaskBlockedError{"runtime": nil}}
	gate := NewCompositeTaskCapabilityGate(evaluator)
	if blocked := gate.CheckTaskCapabilities("ws-1", []string{"planning", "filesystem", "toolbox:notes"}); blocked != nil {
		t.Fatalf("ordinary unclaimed capability was blocked: %+v", blocked)
	}
	if len(evaluator.seen) != 3 {
		t.Fatalf("evaluator did not get a chance to claim each key: %+v", evaluator.seen)
	}
	if blocked := (*CompositeTaskCapabilityGate)(nil).CheckTaskCapabilities("ws-1", []string{"runtime"}); blocked != nil {
		t.Fatalf("nil optional composite changed legacy behavior: %+v", blocked)
	}
}

func TestCompositeTaskCapabilityGateUsesTaskRequirementOrderForFirstBlocker(t *testing.T) {
	evaluator := &recordingCapabilityEvaluator{claims: map[string]*TaskBlockedError{
		"first":  {ReasonCode: "first", Reason: "First repair."},
		"second": {ReasonCode: "second", Reason: "Second repair."},
	}}
	gate := NewCompositeTaskCapabilityGate(evaluator)
	blocked := gate.CheckTaskCapabilities("ws-order", []string{"second", "first"})
	if blocked == nil || blocked.ReasonCode != "second" {
		t.Fatalf("task declaration order did not choose first blocker: %+v", blocked)
	}
}
