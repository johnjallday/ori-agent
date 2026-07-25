package connections

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Rate-limit handling (FR 42): when Google returns 429 / quota exhaustion, the
// affected product moves to HealthRateLimited and Ori backs off. It must NOT be
// treated as a token failure or trigger a reconnect prompt — that would send
// the user into a pointless re-auth.

// retryAfterCeiling caps how long a provider-supplied Retry-After can push a
// retry out, so a hostile or bogus header can't park a grant indefinitely.
const retryAfterCeiling = time.Hour

// IsRateLimited reports whether an HTTP status indicates rate limiting / quota.
func IsRateLimited(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests
}

// BackoffPolicy computes bounded exponential backoff between retries.
type BackoffPolicy struct {
	Base   time.Duration
	Max    time.Duration
	Factor float64
}

// DefaultBackoff is a sane default: 1s base, doubling, capped at 5m.
func DefaultBackoff() BackoffPolicy {
	return BackoffPolicy{Base: time.Second, Max: 5 * time.Minute, Factor: 2}
}

// Delay returns the wait before a retry for a zero-based attempt number. A
// provider Retry-After hint is authoritative when it exceeds the computed
// exponential delay (it may legitimately exceed Max), but is itself capped at
// retryAfterCeiling. The exponential component alone is capped at Max.
func (p BackoffPolicy) Delay(attempt int, retryAfter time.Duration) time.Duration {
	base := p.Base
	if base <= 0 {
		base = time.Second
	}
	maxDelay := p.Max
	if maxDelay <= 0 {
		maxDelay = 5 * time.Minute
	}
	factor := p.Factor
	if factor < 1 {
		factor = 2
	}
	if attempt < 0 {
		attempt = 0
	}

	d := float64(base)
	for i := 0; i < attempt; i++ {
		d *= factor
		if d >= float64(maxDelay) {
			d = float64(maxDelay)
			break
		}
	}
	delay := time.Duration(d)

	delay = max(delay, retryAfter)
	delay = min(delay, retryAfterCeiling)
	return delay
}

// ParseRetryAfter parses a Retry-After header value, which is either
// delta-seconds or an HTTP-date. It returns the wait and whether a valid value
// was present. `now` is the reference time for the HTTP-date form.
func ParseRetryAfter(header string, now time.Time) (time.Duration, bool) {
	h := strings.TrimSpace(header)
	if h == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(h); err == nil {
		return max(t.Sub(now), 0), true
	}
	return 0, false
}
