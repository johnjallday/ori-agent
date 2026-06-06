package workspace

import (
	"errors"
	"testing"
)

func TestClassifyDelegationTrigger(t *testing.T) {
	t.Run("blocked error triggers", func(t *testing.T) {
		err := &TaskBlockedError{ReasonCode: "stuck", Reason: "agent needs guidance"}
		got := ClassifyDelegationTrigger(Task{}, "", err)
		if !got.Trigger || got.Code != DelegationTriggerBlocked || got.Reason != "agent needs guidance" {
			t.Fatalf("got %+v, want blocked trigger", got)
		}
	})

	t.Run("generic failure triggers", func(t *testing.T) {
		got := ClassifyDelegationTrigger(Task{}, "partial", errors.New("boom"))
		if !got.Trigger || got.Code != DelegationTriggerFailed || got.Reason != "boom" {
			t.Fatalf("got %+v, want failed trigger", got)
		}
	})

	t.Run("empty result triggers when output is required", func(t *testing.T) {
		task := Task{OutputSpec: &TaskOutputSpec{}}
		got := ClassifyDelegationTrigger(task, "   \n ", nil)
		if !got.Trigger || got.Code != DelegationTriggerEmptyOutput {
			t.Fatalf("got %+v, want empty-output trigger", got)
		}
	})

	t.Run("empty result does not trigger without an output requirement", func(t *testing.T) {
		// A side-effect task (send email, write file) legitimately completes with
		// no result. Without an output spec/contract there is nothing to violate.
		got := ClassifyDelegationTrigger(Task{Description: "send email"}, "   \n ", nil)
		if got.Trigger {
			t.Fatalf("got %+v, want no trigger on empty result with no output requirement", got)
		}
	})

	t.Run("clean success does not trigger", func(t *testing.T) {
		got := ClassifyDelegationTrigger(Task{}, "here is the answer", nil)
		if got.Trigger {
			t.Fatalf("got %+v, want no trigger on clean success", got)
		}
	})

	t.Run("success with no output spec does not run validation", func(t *testing.T) {
		// A task with neither OutputSpec nor OutputContract and a non-empty result
		// must not trigger (no output requirement to violate).
		got := ClassifyDelegationTrigger(Task{Description: "side-effect task"}, "done", nil)
		if got.Trigger {
			t.Fatalf("got %+v, want no trigger", got)
		}
	})
}
