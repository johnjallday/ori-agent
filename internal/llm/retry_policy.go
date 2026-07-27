package llm

import (
	"math/rand/v2"
	"time"
)

// The automatic-retry budget.
//
// Two rules make this safe. First, only the typed transient allowlist is ever
// retried automatically — a deterministic failure gets zero retries, so a quota
// error costs one round-trip instead of three. Second, the budget is SHARED:
// the provider SDKs' own retry loops are disabled where Ori drives retries, so
// three task-level attempts cannot silently become nine HTTP requests (FR 54).

// DefaultMaxAutomaticRetries is the retry count for a transient failure: two
// retries after the initial attempt, so at most three attempts total. Enough to
// ride out a blip, few enough that a persistent problem surfaces quickly.
const DefaultMaxAutomaticRetries = 2

// RetryPolicy computes the delay before an automatic retry. Its randomness and
// clock are injectable so tests can assert the schedule exactly.
type RetryPolicy struct {
	// MaxRetries is the number of automatic retries after the first attempt.
	MaxRetries int
	// BaseDelay is the first backoff interval; it doubles per retry.
	BaseDelay time.Duration
	// MaxDelay caps the computed backoff before jitter.
	MaxDelay time.Duration
	// JitterFraction is the proportion of the delay that is randomized, spreading
	// simultaneous retries so a recovering provider is not hit by a thundering
	// herd. 0 disables jitter (used by tests that assert exact delays).
	JitterFraction float64
	// rand supplies the jitter; nil uses the global source.
	rand func() float64
}

// DefaultRetryPolicy is the policy used by task execution.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:     DefaultMaxAutomaticRetries,
		BaseDelay:      time.Second,
		MaxDelay:       8 * time.Second,
		JitterFraction: 0.2,
	}
}

// WithRand injects the jitter source (values in [0,1)).
func (p RetryPolicy) WithRand(source func() float64) RetryPolicy {
	p.rand = source
	return p
}

// ShouldRetry reports whether attempt (1-based) may be retried automatically
// for err, and how long to wait first.
//
// It answers false for every deterministic category regardless of the remaining
// budget: having attempts left is not a reason to repeat a request whose
// outcome cannot change.
func (p RetryPolicy) ShouldRetry(err error, attempt int, deadline time.Duration) (bool, time.Duration) {
	typed, ok := AsProviderError(err)
	if !ok || typed == nil {
		// An unclassified failure is not evidence that retrying is safe (FR 49).
		return false, 0
	}
	if !typed.Retryable {
		return false, 0
	}
	if attempt > p.MaxRetries {
		return false, 0
	}

	delay := p.backoff(attempt)
	// A provider that asked for a specific delay knows better than our schedule
	// — but only up to the bound ProviderError already applied.
	if typed.RetryAfter > 0 {
		delay = typed.RetryAfter
	}
	// Never schedule a wait the task cannot afford: sleeping past the deadline
	// converts a retryable failure into a timeout, which is strictly worse.
	if deadline > 0 && delay >= deadline {
		return false, 0
	}
	return true, delay
}

// backoff returns the exponential delay for a 1-based attempt, capped and
// jittered.
func (p RetryPolicy) backoff(attempt int) time.Duration {
	base := p.BaseDelay
	if base <= 0 {
		base = time.Second
	}
	delay := base
	for i := 1; i < attempt; i++ {
		delay *= 2
		if p.MaxDelay > 0 && delay >= p.MaxDelay {
			delay = p.MaxDelay
			break
		}
	}
	if p.MaxDelay > 0 && delay > p.MaxDelay {
		delay = p.MaxDelay
	}
	if p.JitterFraction <= 0 {
		return delay
	}

	source := p.rand
	if source == nil {
		source = rand.Float64
	}
	// Symmetric jitter around the computed delay: ±JitterFraction.
	spread := float64(delay) * p.JitterFraction
	offset := (source()*2 - 1) * spread
	jittered := time.Duration(float64(delay) + offset)
	if jittered < 0 {
		return 0
	}
	return jittered
}
