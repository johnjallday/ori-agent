package llm

import (
	"errors"
	"testing"
	"time"
)

func fixedPolicy() RetryPolicy {
	p := DefaultRetryPolicy()
	p.JitterFraction = 0 // assert the exact schedule
	return p
}

// The rule that saves the user money: a deterministic failure gets ZERO
// automatic retries, however much budget remains.
func TestRetryPolicy_DeterministicFailuresAreNeverRetried(t *testing.T) {
	policy := fixedPolicy()
	for _, category := range []ErrorCategory{
		CategoryQuotaExhausted,
		CategoryAuthentication,
		CategoryNotConfigured,
		CategoryPermissionDenied,
		CategoryInvalidModel,
		CategoryInvalidRequest,
		CategoryContextLength,
		CategoryPolicyRejection,
		CategoryCanceled,
		CategoryUnknown,
	} {
		err := NewProviderError("openai", category, errors.New("x"))
		if retry, _ := policy.ShouldRetry(err, 1, time.Minute); retry {
			t.Fatalf("%s must not be retried automatically", category)
		}
	}
}

func TestRetryPolicy_TransientFailuresGetABoundedBudget(t *testing.T) {
	policy := fixedPolicy()
	for _, category := range []ErrorCategory{
		CategoryNetwork, CategoryTimeout, CategoryRateLimited,
		CategoryProviderError, CategoryProviderOverloaded,
	} {
		err := NewProviderError("openai", category, errors.New("x"))

		retry, first := policy.ShouldRetry(err, 1, time.Minute)
		if !retry || first != time.Second {
			t.Fatalf("%s attempt 1: retry=%v delay=%v, want true/1s", category, retry, first)
		}
		retry, second := policy.ShouldRetry(err, 2, time.Minute)
		if !retry || second != 2*time.Second {
			t.Fatalf("%s attempt 2: retry=%v delay=%v, want true/2s", category, retry, second)
		}
		// Two retries is the whole budget: a third would be a fourth attempt.
		if retry, _ := policy.ShouldRetry(err, 3, time.Minute); retry {
			t.Fatalf("%s attempt 3 must exhaust the budget", category)
		}
	}
}

// An unclassified error is not evidence that retrying is safe.
func TestRetryPolicy_UntypedErrorsAreNotRetried(t *testing.T) {
	policy := fixedPolicy()
	if retry, _ := policy.ShouldRetry(errors.New("mystery"), 1, time.Minute); retry {
		t.Fatal("an unclassified error must not be retried automatically")
	}
	if retry, _ := policy.ShouldRetry(nil, 1, time.Minute); retry {
		t.Fatal("a nil error is not a failure to retry")
	}
}

// A provider-supplied delay wins over the schedule — within the bound the typed
// error already applied.
func TestRetryPolicy_HonorsBoundedRetryAfter(t *testing.T) {
	policy := fixedPolicy()
	err := NewProviderError("openai", CategoryRateLimited, errors.New("slow down")).
		WithRetryAfter(7 * time.Second)

	retry, delay := policy.ShouldRetry(err, 1, time.Minute)
	if !retry || delay != 7*time.Second {
		t.Fatalf("retry=%v delay=%v, want true/7s", retry, delay)
	}
}

// Sleeping past the task's deadline turns a retryable failure into a timeout,
// which is strictly worse than reporting it now.
func TestRetryPolicy_RefusesADelayTheTaskCannotAfford(t *testing.T) {
	policy := fixedPolicy()
	err := NewProviderError("openai", CategoryRateLimited, errors.New("slow down")).
		WithRetryAfter(20 * time.Second)

	if retry, _ := policy.ShouldRetry(err, 1, 5*time.Second); retry {
		t.Fatal("a delay longer than the remaining deadline must not be scheduled")
	}
	// With room to spare it is fine.
	if retry, _ := policy.ShouldRetry(err, 1, time.Minute); !retry {
		t.Fatal("the same delay must be allowed when the deadline permits")
	}
	// A zero deadline means "unbounded", not "no time left".
	if retry, _ := policy.ShouldRetry(err, 1, 0); !retry {
		t.Fatal("a zero deadline must not block retries")
	}
}

func TestRetryPolicy_BackoffIsCapped(t *testing.T) {
	policy := RetryPolicy{MaxRetries: 10, BaseDelay: time.Second, MaxDelay: 4 * time.Second}
	err := NewProviderError("openai", CategoryProviderError, errors.New("x"))

	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 4 * time.Second} {
		retry, delay := policy.ShouldRetry(err, attempt, time.Hour)
		if !retry || delay != want {
			t.Fatalf("attempt %d: retry=%v delay=%v, want true/%v", attempt, retry, delay, want)
		}
	}
}

// Jitter spreads simultaneous retries so a recovering provider is not hit by a
// thundering herd. It is symmetric around the computed delay and never negative.
func TestRetryPolicy_Jitter(t *testing.T) {
	base := RetryPolicy{MaxRetries: 2, BaseDelay: time.Second, MaxDelay: 8 * time.Second, JitterFraction: 0.5}
	err := NewProviderError("openai", CategoryProviderError, errors.New("x"))

	// rand()=0 → the low end; rand()=1 → the high end; rand()=0.5 → exactly base.
	for _, tc := range []struct {
		random float64
		want   time.Duration
	}{
		{0, 500 * time.Millisecond},
		{0.5, time.Second},
		{0.999, time.Duration(float64(time.Second) * 1.499)},
	} {
		policy := base.WithRand(func() float64 { return tc.random })
		_, delay := policy.ShouldRetry(err, 1, time.Hour)
		if diff := delay - tc.want; diff > 5*time.Millisecond || diff < -5*time.Millisecond {
			t.Fatalf("rand=%v gave %v, want ≈%v", tc.random, delay, tc.want)
		}
	}

	// Extreme jitter must never produce a negative sleep.
	policy := RetryPolicy{MaxRetries: 2, BaseDelay: time.Second, JitterFraction: 5}.
		WithRand(func() float64 { return 0 })
	if _, delay := policy.ShouldRetry(err, 1, time.Hour); delay < 0 {
		t.Fatalf("delay = %v, must never be negative", delay)
	}
}

// The whole point of the budget: three attempts must mean three requests, not
// three times the SDK's own retry count.
func TestRetryPolicy_BudgetIsTheWholeBudget(t *testing.T) {
	policy := fixedPolicy()
	err := NewProviderError("openai", CategoryProviderError, errors.New("x"))

	attempts := 1 // the initial attempt
	for policy.MaxRetries > 0 {
		retry, _ := policy.ShouldRetry(err, attempts, time.Hour)
		if !retry {
			break
		}
		attempts++
		if attempts > 10 {
			t.Fatal("retry loop did not terminate")
		}
	}
	if attempts != DefaultMaxAutomaticRetries+1 {
		t.Fatalf("total attempts = %d, want %d", attempts, DefaultMaxAutomaticRetries+1)
	}
}
