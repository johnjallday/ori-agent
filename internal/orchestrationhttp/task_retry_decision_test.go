package orchestrationhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func noJitter() llm.RetryPolicy {
	p := llm.DefaultRetryPolicy()
	p.JitterFraction = 0
	return p
}

func noEvidence() taskExecutionEvidence { return taskExecutionEvidence{} }

// The headline behavior: a quota failure stops after ONE attempt, names the
// provider's billing as the fix, and never suggests something that cannot help.
func TestDecideRetry_QuotaStopsImmediately(t *testing.T) {
	quota := llm.ClassifyProviderError("openai",
		errors.New("You exceeded your current quota, please check your plan and billing details"),
		http.StatusTooManyRequests, "insufficient_quota")

	verdict := decideRetry(noJitter(), quota, 1, time.Minute, noEvidence())

	if verdict.Retry {
		t.Fatal("a quota failure must not be retried automatically")
	}
	if verdict.ReasonCode != reasonProviderQuota {
		t.Fatalf("reason code = %q, want %q", verdict.ReasonCode, reasonProviderQuota)
	}
	if verdict.Action == nil || verdict.Action.Code != "configure_provider" {
		t.Fatalf("action = %+v, want provider configuration", verdict.Action)
	}
	// The message must name the provider's billing, not Gmail or a vault.
	lower := strings.ToLower(verdict.Reason)
	if !strings.Contains(lower, "quota") && !strings.Contains(lower, "credit") {
		t.Fatalf("reason = %q, want it to name the quota problem", verdict.Reason)
	}
	for _, wrong := range []string{"gmail", "vault", "email"} {
		if strings.Contains(lower, wrong) {
			t.Fatalf("quota message wrongly mentions %q: %s", wrong, verdict.Reason)
		}
	}
	// Exactly one action, and it is the repair.
	actions := retrySuggestedActions(verdict)
	if len(actions) != 1 || !strings.Contains(actions[0], "provider") {
		t.Fatalf("actions = %v, want only the provider repair", actions)
	}
	for _, action := range actions {
		if strings.Contains(action, "switch_agent") || strings.Contains(action, "Ori decide") {
			t.Fatalf("offered an action that cannot fix quota: %v", actions)
		}
	}
}

// A temporary rate limit is the one 429 that IS worth repeating.
func TestDecideRetry_RateLimitIsRetried(t *testing.T) {
	limited := llm.ClassifyProviderError("openai", errors.New("Rate limit reached"),
		http.StatusTooManyRequests, "rate_limit_exceeded")

	verdict := decideRetry(noJitter(), limited, 1, time.Minute, noEvidence())
	if !verdict.Retry {
		t.Fatalf("a temporary rate limit should be retried: %+v", verdict)
	}
	if verdict.Delay != time.Second {
		t.Fatalf("delay = %v, want 1s", verdict.Delay)
	}
	if verdict.Category != llm.CategoryRateLimited {
		t.Fatalf("category = %q, want rate_limited", verdict.Category)
	}
}

func TestDecideRetry_DeterministicCategories(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantReason string
		wantAction bool
	}{
		{
			name:       "bad api key",
			err:        llm.ClassifyProviderError("openai", errors.New("Incorrect API key"), http.StatusUnauthorized, ""),
			wantReason: reasonProviderConfig,
			wantAction: true,
		},
		{
			name:       "model gone",
			err:        llm.ClassifyProviderError("openai", errors.New("model does not exist"), http.StatusNotFound, ""),
			wantReason: reasonModelUnavailable,
			wantAction: true,
		},
		{
			name:       "context overflow",
			err:        llm.ClassifyProviderError("openai", errors.New("maximum context length"), http.StatusBadRequest, "context_length_exceeded"),
			wantReason: reasonInputTooLarge,
		},
		{
			name:       "invalid request",
			err:        llm.ClassifyProviderError("openai", errors.New("Invalid schema"), http.StatusBadRequest, ""),
			wantReason: reasonRequestRejected,
		},
		{
			name:       "content policy",
			err:        llm.ClassifyProviderError("openai", errors.New("rejected"), http.StatusBadRequest, "content_policy_violation"),
			wantReason: reasonRequestRejected,
		},
		{
			name:       "unclassified",
			err:        errors.New("something went sideways"),
			wantReason: reasonExecutionFailed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict := decideRetry(noJitter(), tc.err, 1, time.Minute, noEvidence())
			if verdict.Retry {
				t.Fatalf("%s must not be retried automatically", tc.name)
			}
			if verdict.ReasonCode != tc.wantReason {
				t.Fatalf("reason code = %q, want %q", verdict.ReasonCode, tc.wantReason)
			}
			if (verdict.Action != nil) != tc.wantAction {
				t.Fatalf("action = %+v, wantAction = %v", verdict.Action, tc.wantAction)
			}
			if strings.TrimSpace(verdict.Reason) == "" {
				t.Fatal("every stop needs an explanation")
			}
		})
	}
}

// A transient failure that persists ends as retry_exhausted — a different
// state from "we never tried", and the message should say so.
func TestDecideRetry_ExhaustsTheBudgetThenStops(t *testing.T) {
	transient := llm.ClassifyProviderError("openai", errors.New("bad gateway"), http.StatusBadGateway, "")
	policy := noJitter()

	if retry, _ := policy.ShouldRetry(transient, 1, time.Minute); !retry {
		t.Fatal("expected the first retry to be allowed")
	}
	verdict := decideRetry(policy, transient, policy.MaxRetries+1, time.Minute, noEvidence())
	if verdict.Retry {
		t.Fatal("the budget must be finite")
	}
	if verdict.ReasonCode != reasonRetryExhausted {
		t.Fatalf("reason code = %q, want %q", verdict.ReasonCode, reasonRetryExhausted)
	}
	if !strings.Contains(verdict.Reason, "kept failing") {
		t.Fatalf("reason = %q, want it to say Ori already retried", verdict.Reason)
	}
}

// Replay safety outranks retryability: even a genuinely transient error is not
// replayed when the attempt already changed something.
func TestDecideRetry_MutatingAttemptIsNeverReplayed(t *testing.T) {
	transient := llm.ClassifyProviderError("openai", errors.New("bad gateway"), http.StatusBadGateway, "")
	evidence := taskExecutionEvidence{Attempts: []workspace.ToolAttempt{
		{Name: "read_file", Class: workspace.ToolSideEffectRead, Completed: true},
		{Name: "move_file", Class: workspace.ToolSideEffectWrite, Completed: true},
	}}

	verdict := decideRetry(noJitter(), transient, 1, time.Minute, evidence)
	if verdict.Retry {
		t.Fatal("an attempt that moved a file must not be replayed automatically")
	}
	if verdict.ReasonCode != reasonReplayUnsafe {
		t.Fatalf("reason code = %q, want %q", verdict.ReasonCode, reasonReplayUnsafe)
	}
	if !strings.Contains(verdict.Reason, "move_file") {
		t.Fatalf("reason = %q, want it to name the tool that blocked replay", verdict.Reason)
	}
	// The user decides; Ori does not offer to repeat the work itself.
	actions := retrySuggestedActions(verdict)
	if len(actions) == 0 || actions[0] != "review_then_retry" {
		t.Fatalf("actions = %v, want the user to review first", actions)
	}
}

// The ambiguous case: a mutation that failed partway through. Its effect is
// unknown, which is the strongest reason not to repeat it.
func TestDecideRetry_AmbiguousMutationIsNeverReplayed(t *testing.T) {
	transient := llm.ClassifyProviderError("openai", errors.New("connection reset by peer"), 0, "")
	evidence := taskExecutionEvidence{Attempts: []workspace.ToolAttempt{
		{Name: "write_file", Class: workspace.ToolSideEffectWrite, Completed: false},
	}}

	verdict := decideRetry(noJitter(), transient, 1, time.Minute, evidence)
	if verdict.Retry {
		t.Fatal("an interrupted write must not be replayed automatically")
	}
	if !strings.Contains(verdict.Reason, "unknown") {
		t.Fatalf("reason = %q, want it to admit the effect is unknown", verdict.Reason)
	}
}

// A read-only attempt stays replayable, which is what keeps the retry budget
// useful for the common case.
func TestDecideRetry_ReadOnlyAttemptStaysReplayable(t *testing.T) {
	transient := llm.ClassifyProviderError("openai", errors.New("service unavailable"), http.StatusServiceUnavailable, "")
	evidence := taskExecutionEvidence{Attempts: []workspace.ToolAttempt{
		{Name: "read_file", Class: workspace.ToolSideEffectRead, Completed: true},
		{Name: "mail_search_threads", Class: workspace.ToolSideEffectRead, Completed: true},
	}}

	verdict := decideRetry(noJitter(), transient, 1, time.Minute, evidence)
	if !verdict.Retry {
		t.Fatalf("a read-only attempt should stay replayable: %+v", verdict)
	}
}

// Cancellation is the caller's decision, not a failure to recover from.
func TestDecideRetry_CancellationIsNotRetried(t *testing.T) {
	verdict := decideRetry(noJitter(), context.Canceled, 1, time.Minute, noEvidence())
	if verdict.Retry {
		t.Fatal("a canceled task must not be retried")
	}
	if verdict.Category != llm.CategoryCanceled {
		t.Fatalf("category = %q, want canceled", verdict.Category)
	}
}

func TestRemainingDeadline(t *testing.T) {
	if got := remainingDeadline(context.Background()); got != 0 {
		t.Fatalf("no deadline should report 0, got %v", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if got := remainingDeadline(ctx); got <= 0 || got > time.Minute {
		t.Fatalf("remaining = %v, want just under a minute", got)
	}

	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if got := remainingDeadline(expired); got != time.Nanosecond {
		t.Fatalf("expired = %v, want a value that fits nothing", got)
	}
}

// A backoff must never outlast the task: sleeping past the deadline converts a
// retryable failure into a timeout.
func TestDecideRetry_RefusesToSleepPastTheDeadline(t *testing.T) {
	limited := llm.ClassifyProviderError("openai", errors.New("rate limited"),
		http.StatusTooManyRequests, "rate_limit_exceeded")

	if verdict := decideRetry(noJitter(), limited, 1, 100*time.Millisecond, noEvidence()); verdict.Retry {
		t.Fatal("a 1s backoff must not be scheduled inside a 100ms deadline")
	}
}

func TestWaitBeforeRetry_RespondsToCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitBeforeRetry(ctx, time.Hour) {
		t.Fatal("a canceled context must abort the wait immediately")
	}

	if !waitBeforeRetry(context.Background(), time.Millisecond) {
		t.Fatal("a normal wait should complete")
	}
	if !waitBeforeRetry(context.Background(), 0) {
		t.Fatal("a zero delay should proceed")
	}
}

// History must distinguish work Ori chose to repeat from work the user asked
// for again (FR 60).
func TestAttemptOrigin(t *testing.T) {
	if got := attemptOrigin(1, false); got != attemptOriginInitial {
		t.Fatalf("first scheduled attempt = %q, want initial", got)
	}
	if got := attemptOrigin(1, true); got != attemptOriginExplicit {
		t.Fatalf("first user-triggered attempt = %q, want explicit", got)
	}
	if got := attemptOrigin(2, true); got != attemptOriginAutomatic {
		t.Fatalf("second attempt = %q, want automatic regardless of who started the run", got)
	}
}
