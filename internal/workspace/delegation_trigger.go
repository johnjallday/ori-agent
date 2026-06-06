package workspace

import (
	"fmt"
	"strings"
)

// Delegation trigger codes for the "plan, adapt on failure" model.
const (
	DelegationTriggerBlocked       = "blocked"
	DelegationTriggerFailed        = "failed"
	DelegationTriggerEmptyOutput   = "empty_required_output"
	DelegationTriggerInvalidOutput = "invalid_output"
)

// DelegationTrigger reports whether the adaptive delegation loop should engage
// after a task execution outcome, and why.
type DelegationTrigger struct {
	Trigger bool
	Code    string
	Reason  string
}

// ClassifyDelegationTrigger implements the "always plan, adapt on failure"
// trigger model: a static plan executes first, and the dynamic delegation loop
// is entered only when a step fails, is blocked, or produces an unusable result.
// A clean success returns Trigger=false, so successful plans never enter the loop.
func ClassifyDelegationTrigger(task Task, result string, execErr error) DelegationTrigger {
	if blocked, ok := AsTaskBlockedError(execErr); ok {
		reason := strings.TrimSpace(blocked.Reason)
		if reason == "" {
			reason = "task is blocked and needs adaptation"
		}
		return DelegationTrigger{Trigger: true, Code: DelegationTriggerBlocked, Reason: reason}
	}
	if execErr != nil {
		return DelegationTrigger{Trigger: true, Code: DelegationTriggerFailed, Reason: execErr.Error()}
	}
	// An empty result only counts as a failure when the task actually requires
	// output. Side-effect tasks (send email, write file) legitimately complete
	// without producing a result and must not enter the loop. This mirrors the
	// requirement check used by the invalid-output path below.
	if strings.TrimSpace(result) == "" && taskRequiresOutput(task) {
		return DelegationTrigger{
			Trigger: true,
			Code:    DelegationTriggerEmptyOutput,
			Reason:  "task completed without producing any output",
		}
	}
	if reason, ok := classifyOutputValidation(task, result); ok {
		return DelegationTrigger{Trigger: true, Code: DelegationTriggerInvalidOutput, Reason: reason}
	}
	return DelegationTrigger{}
}

// taskRequiresOutput reports whether the task declares an output requirement
// (an output spec or contract). Tasks without one are side-effect tasks that may
// legitimately complete with an empty result.
func taskRequiresOutput(task Task) bool {
	return task.OutputSpec != nil || task.OutputContract != nil
}

// classifyOutputValidation returns a reason and true when the task declares an
// output spec/contract and the result fails it (needs_review). Tasks without an
// output requirement, or whose output passed / was approved, do not trigger.
func classifyOutputValidation(task Task, result string) (string, bool) {
	var validation *TaskValidationResult
	switch {
	case task.OutputSpec != nil:
		validation, _ = ValidateTaskOutputSpecResult(&task, result)
	case task.OutputContract != nil:
		validation, _ = ValidateTaskOutputContractResult(&task, result)
	default:
		return "", false
	}
	if validation == nil || validation.ValidationStatus != TaskValidationNeedsReview {
		return "", false
	}
	return fmt.Sprintf("task output failed validation (%d error(s))", len(validation.Errors)), true
}
