package orchestrationhttp

import (
	"context"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Whether a failed attempt may be repeated automatically.
//
// The old loop retried anything: `if attempt < maxAttempts { continue }`. That
// is right for a dropped connection and wrong for everything else. An
// `insufficient_quota` failure cost three attempts to reach the same answer,
// and the user was finally shown "Task failed after 3 attempts" — which names
// neither the cause nor the fix.
//
// Two independent questions must BOTH answer yes before a replay:
//
//  1. Can this error change on its own? (typed classification — llm.RetryPolicy)
//  2. Is repeating this attempt safe? (side-effect evidence — replay safety)
//
// Either "no" stops the loop and hands the user an explicit, informed choice.

// retryVerdict is the outcome of that pair of questions.
type retryVerdict struct {
	// Retry reports whether the loop may run another attempt automatically.
	Retry bool
	// Delay is how long to wait first.
	Delay time.Duration
	// ReasonCode is the stable code recorded on a block when Retry is false.
	ReasonCode string
	// Reason is the safe user-facing sentence explaining why.
	Reason string
	// Action is the single repair to offer, when one applies.
	Action *workspace.TaskRepairAction
	// Category is the classified provider category, for logs and history.
	Category llm.ErrorCategory
}

// Stable blocked reason codes for execution failures. They are machine codes:
// safe to log, safe to switch on, and stable across message rewording (FR 56).
const (
	// reasonProviderQuota: the AI provider account is out of quota or credit.
	reasonProviderQuota = "provider_quota_exhausted"
	// reasonProviderConfig: the provider is missing or misconfigured.
	reasonProviderConfig = "provider_configuration_required"
	// reasonModelUnavailable: the configured model cannot be used.
	reasonModelUnavailable = "model_unavailable"
	// reasonRequestRejected: the provider rejected the request as invalid or
	// against policy; the task itself must change.
	reasonRequestRejected = "request_rejected"
	// reasonInputTooLarge: the request exceeded the model's context window.
	reasonInputTooLarge = "input_too_large"
	// reasonRetryExhausted: a genuinely transient failure that kept failing.
	reasonRetryExhausted = "retry_exhausted"
	// reasonReplayUnsafe: the attempt already ran a tool that can change things,
	// so repeating it could duplicate the effect.
	reasonReplayUnsafe = "replay_unsafe"
	// reasonExecutionFailed: an unclassified failure. Deterministic by default.
	reasonExecutionFailed = "execution_failed"
)

// providerSettingsAction points at where provider credentials and billing are
// configured. Quota is a provider-billing problem, never a Gmail or vault one —
// naming the wrong surface is worse than naming none (FR 52).
var providerSettingsAction = &workspace.TaskRepairAction{
	Code:  "configure_provider",
	Label: "Open AI provider settings",
	URL:   "/settings#api-keys",
}

// reasonCodeFor maps a provider category to its stable blocked reason code.
func reasonCodeFor(category llm.ErrorCategory) string {
	switch category {
	case llm.CategoryQuotaExhausted:
		return reasonProviderQuota
	case llm.CategoryAuthentication, llm.CategoryNotConfigured, llm.CategoryPermissionDenied:
		return reasonProviderConfig
	case llm.CategoryInvalidModel:
		return reasonModelUnavailable
	case llm.CategoryContextLength:
		return reasonInputTooLarge
	case llm.CategoryInvalidRequest, llm.CategoryPolicyRejection:
		return reasonRequestRejected
	default:
		return reasonExecutionFailed
	}
}

// repairActionFor returns the one action that can resolve a category, or nil
// when no action would help. Deliberately conservative: offering "switch agent"
// for a quota failure invites the user to waste time on something that cannot
// work.
func repairActionFor(category llm.ErrorCategory) *workspace.TaskRepairAction {
	switch category {
	case llm.CategoryQuotaExhausted, llm.CategoryAuthentication,
		llm.CategoryNotConfigured, llm.CategoryPermissionDenied, llm.CategoryInvalidModel:
		return providerSettingsAction
	default:
		return nil
	}
}

// decideRetry answers both questions for one failed attempt.
//
// deadline is the time remaining before the task's own timeout; a delay that
// would outlast it converts a retryable failure into a timeout, so it is
// refused.
func decideRetry(
	policy llm.RetryPolicy,
	err error,
	attempt int,
	deadline time.Duration,
	evidence taskExecutionEvidence,
) retryVerdict {
	classified := llm.ClassifyProviderError("", err, 0, "")
	category := llm.CategoryUnknown
	message := ""
	if classified != nil {
		category = classified.Category
		message = classified.Message
	}

	// A cancellation is the caller's decision, not a failure to recover from.
	if category == llm.CategoryCanceled {
		return retryVerdict{ReasonCode: reasonExecutionFailed, Reason: "The task was canceled.", Category: category}
	}

	// Question 2 first: even a genuinely transient error must not be replayed if
	// the attempt already changed something. Duplicating a sent email or a moved
	// file is worse than making the user press Retry.
	if safety := evidence.replaySafety(); !safety.Safe {
		// Use the safety verdict's own wording: it distinguishes "already ran"
		// from "failed partway through, so its effect is unknown", and that
		// distinction is exactly what the user needs to judge a manual retry.
		reason := "Ori won't repeat this automatically — " + safety.Reason +
			" (" + strings.Join(safety.UnsafeTools, ", ") + ")."
		if message != "" {
			reason = message + " " + reason
		}
		return retryVerdict{
			ReasonCode: reasonReplayUnsafe,
			Reason:     reason,
			Action:     repairActionFor(category),
			Category:   category,
		}
	}

	// Question 1: can this error change on its own?
	if retry, delay := policy.ShouldRetry(classified, attempt, deadline); retry {
		return retryVerdict{Retry: true, Delay: delay, Category: category}
	}

	reason := message
	code := reasonCodeFor(category)
	if classified != nil && classified.Retryable {
		// Retryable but out of budget: a real transient failure that persisted.
		code = reasonRetryExhausted
		if reason != "" {
			reason += " Ori retried and it kept failing."
		}
	}
	if strings.TrimSpace(reason) == "" {
		reason = "The task failed with an error Ori couldn't classify, so it won't retry automatically."
	}
	return retryVerdict{ReasonCode: code, Reason: reason, Action: repairActionFor(category), Category: category}
}

// remainingDeadline reports how much time the task's context still allows, or 0
// when it has no deadline. A retry delay is measured against this so a backoff
// can never push the task past its own timeout.
func remainingDeadline(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	if remaining := time.Until(deadline); remaining > 0 {
		return remaining
	}
	return time.Nanosecond // expired: nothing fits
}

// retrySuggestedActions renders the verdict's repair as the display action
// list. A verdict with a concrete repair offers ONLY that: listing
// "switch agent" or "let Ori decide" beside a quota failure invites the user to
// waste time on something that cannot work (FR 56).
func retrySuggestedActions(verdict retryVerdict) []string {
	if verdict.Action != nil && strings.TrimSpace(verdict.Action.Label) != "" {
		if url := strings.TrimSpace(verdict.Action.URL); url != "" {
			return []string{verdict.Action.Label + " (" + url + ")"}
		}
		return []string{verdict.Action.Label}
	}
	switch verdict.ReasonCode {
	case reasonReplayUnsafe:
		// The user decides whether repeating the work is acceptable; Ori will not.
		return []string{"review_then_retry", "mark_failed"}
	case reasonRequestRejected, reasonInputTooLarge:
		return []string{"continue_with_instruction", "mark_failed"}
	default:
		return []string{"retry", "continue_with_instruction", "mark_failed"}
	}
}

// waitBeforeRetry sleeps for delay, returning false if the context is canceled
// first. Cancellation must be observed promptly rather than after the full
// backoff (FR 54).
func waitBeforeRetry(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Attempt origins recorded on every attempt, so history distinguishes work Ori
// chose to repeat from work the user asked for again (FR 60).
const (
	attemptOriginInitial   = "initial"
	attemptOriginAutomatic = "automatic_retry"
	attemptOriginExplicit  = "explicit_retry"
)

// attemptOrigin reports how an attempt was started. The first attempt of a run
// is explicit when the user triggered the run after a block, otherwise initial;
// every later attempt in the same run is an automatic retry.
func attemptOrigin(attempt int, userTriggered bool) string {
	if attempt > 1 {
		return attemptOriginAutomatic
	}
	if userTriggered {
		return attemptOriginExplicit
	}
	return attemptOriginInitial
}

// logRetryDecision records the safe fields only: category, reason code, attempt
// number, and origin. No provider payload, prompt, or result text.
func logRetryDecision(taskID string, attempt int, origin string, verdict retryVerdict) {
	fields := logger.Fields{
		"task_id":  taskID,
		"attempt":  attempt,
		"origin":   origin,
		"category": string(verdict.Category),
		"retry":    verdict.Retry,
	}
	if verdict.ReasonCode != "" {
		fields["reason_code"] = verdict.ReasonCode
	}
	if verdict.Retry {
		fields["delay_ms"] = verdict.Delay.Milliseconds()
	}
	logger.Info("Task attempt outcome classified", fields)
}
