package mailbox

import (
	"errors"
	"time"
)

// Typed error classes let every caller (brief snapshot, agent tools, UI) map a
// failure to a distinct state instead of a generic error, so a rate limit,
// an expired token, and a real outage are never conflated (task 3.11, contract
// §3.3). Adapters classify provider failures into these; they never surface raw
// provider errors (which can carry tokens/PII) to callers.
var (
	// ErrDisconnected means no usable account/credential is connected.
	ErrDisconnected = errors.New("mailbox: account is disconnected")
	// ErrExpired means the stored credential expired or was revoked; the user
	// must reconnect. Distinct from ErrDisconnected so the UI can offer repair.
	ErrExpired = errors.New("mailbox: credentials expired or revoked")
	// ErrPermissionDenied means the caller lacks a required grant/scope for the
	// operation (task 3.8) — most-restrictive-layer denial.
	ErrPermissionDenied = errors.New("mailbox: permission denied")
	// ErrRateLimited means the provider throttled the request; prefer the
	// richer *RateLimitError to convey retry-after.
	ErrRateLimited = errors.New("mailbox: rate limited")
	// ErrTimeout means the request exceeded its deadline or was cancelled.
	ErrTimeout = errors.New("mailbox: request timed out")
	// ErrNotFound means the requested thread/message does not exist or is not
	// visible to this account.
	ErrNotFound = errors.New("mailbox: not found")
	// ErrProvider is a catch-all for an unclassified provider failure. It never
	// wraps the raw provider error text (which may carry secrets); adapters log
	// details internally and return this sanitized sentinel.
	ErrProvider = errors.New("mailbox: provider request failed")
)

// RateLimitError carries the provider's retry-after hint. It unwraps to
// ErrRateLimited so callers can match either the class or the detail.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e == nil {
		return ErrRateLimited.Error()
	}
	return "mailbox: rate limited, retry after " + e.RetryAfter.String()
}

func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// IsTransient reports whether err is worth retrying later (rate limit or
// timeout) versus a terminal state (disconnected/expired/permission).
func IsTransient(err error) bool {
	return errors.Is(err, ErrRateLimited) || errors.Is(err, ErrTimeout)
}

// HealthForError maps a read error to the account Health the UI should show, so
// the mapping lives in one place. A nil error maps to HealthHealthy.
func HealthForError(err error) Health {
	switch {
	case err == nil:
		return HealthHealthy
	case errors.Is(err, ErrExpired):
		return HealthExpired
	case errors.Is(err, ErrDisconnected):
		return HealthDisconnected
	default:
		// Rate limits, timeouts, provider errors, and permission denials do not
		// change the account's connection health — the account is still
		// connected; the request just failed.
		return HealthHealthy
	}
}
