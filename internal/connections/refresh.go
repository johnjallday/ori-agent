package connections

import (
	"context"
	"time"

	"golang.org/x/sync/singleflight"
)

// Single-flight token refresh (FR 45). Concurrent workspace/task usage of one
// shared grant must not launch overlapping refreshes: two refreshes racing can
// each rotate the other's refresh token into a revoked state and lock the
// account out. The coordinator coalesces concurrent refreshes for the same
// grant onto a single execution.

// RefreshedToken is the outcome of a token refresh. The rotated RefreshToken (if
// Google issued a new one) MUST be persisted atomically before the old token
// stops being usable — a crash between receiving and storing it must not strand
// the grant. That atomic persistence is the store's job and is performed inside
// the RefreshFunc, i.e. within the single-flight critical section.
type RefreshedToken struct {
	AccessToken  string // never logged; never returned to the browser
	RefreshToken string // rotated value when present; "" means unchanged
	Expiry       time.Time
}

// RefreshFunc performs the token exchange and the atomic persistence of any
// rotated refresh token. It runs at most once per key per concurrent burst.
type RefreshFunc func(ctx context.Context) (RefreshedToken, error)

// RefreshCoordinator guarantees single-flight refresh per grant key.
//
// Note: like all single-flight, the shared call runs under the first caller's
// context; a later caller cancelling its own context does not cancel the shared
// refresh. Callers that need independent cancellation must not share a key.
type RefreshCoordinator struct {
	group singleflight.Group
}

// Do runs fn for key, coalescing concurrent callers onto one execution and
// returning the shared result to all of them.
func (rc *RefreshCoordinator) Do(ctx context.Context, key string, fn RefreshFunc) (RefreshedToken, error) {
	v, err, _ := rc.group.Do(key, func() (any, error) {
		return fn(ctx)
	})
	if err != nil {
		return RefreshedToken{}, err
	}
	return v.(RefreshedToken), nil
}
