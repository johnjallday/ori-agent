// Package wakeclient is the helper's side of Ori's shared wake coordinator.
//
// The helper never programs a macOS wake. It writes a candidate to the shared
// store and then waits to be told, by the one process that runs `pmset`, that
// the wake actually exists. That distinction is the whole design: a helper may
// only claim wake coverage on evidence it did not produce itself.
//
// If Ori's server is not running, nothing picks the candidate up, verification
// never succeeds. An Overnight Run stays awake, and a wake-required one-time
// continuation fails before it can claim readiness. Those are the correct
// outcomes, reached by doing nothing rather than by guessing.
package wakeclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/wakecoord"
)

const (
	// DefaultVerifyTimeout bounds how long the helper waits for Ori to program
	// a registered candidate. The server's scheduler loop runs on its own
	// cadence, so this is a wait, not a round trip.
	DefaultVerifyTimeout = 90 * time.Second
	// DefaultVerifyInterval is how often the shared record is re-read.
	DefaultVerifyInterval = 2 * time.Second
	// MaxSkew is how far the programmed wake may differ from the requested one
	// and still count as the same wake. The owner applies its own lead time, so
	// an earlier wake is expected; a later one is not the wake that was asked
	// for.
	MaxSkew = 30 * time.Minute
)

// ErrUnavailable means the shared coordinator could not be used at all.
var ErrUnavailable = errors.New("Ori's wake coordinator is unavailable")

// ErrNotProgrammed means no wake matching the candidate has been programmed.
var ErrNotProgrammed = errors.New("the requested wake has not been programmed")

// Client registers and verifies this helper's wake candidates.
type Client struct {
	// Store is the shared coordinator; nil resolves the default location.
	Store *wakecoord.Store
	// Source scopes registration, verification, and cancellation to one
	// subsystem. Existing callers default to Overnight Runs.
	Source string
	// Now supplies the clock.
	Now func() time.Time
	// VerifyTimeout and VerifyInterval bound waiting for the owner.
	VerifyTimeout  time.Duration
	VerifyInterval time.Duration
}

// New builds a Client over an explicit coordinator directory.
func New(dir string) *Client {
	return NewForSource(dir, wakecoord.SourceOvernightRun)
}

// NewForSource builds a client whose candidate operations are isolated to one
// wake source.
func NewForSource(dir, source string) *Client {
	return &Client{Store: wakecoord.New(dir), Source: source}
}

// Default builds a Client over the shared location both processes compute.
func Default() (*Client, error) {
	return DefaultForSource(wakecoord.SourceOvernightRun)
}

// DefaultForSource builds a source-scoped client over the shared location both
// processes compute.
func DefaultForSource(source string) (*Client, error) {
	dir, err := wakecoord.DefaultDir()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, err)
	}
	return NewForSource(dir, source), nil
}

func (c *Client) source() string {
	if c.Source != "" {
		return c.Source
	}
	return wakecoord.SourceOvernightRun
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *Client) timeout() time.Duration {
	if c.VerifyTimeout > 0 {
		return c.VerifyTimeout
	}
	return DefaultVerifyTimeout
}

func (c *Client) interval() time.Duration {
	if c.VerifyInterval > 0 {
		return c.VerifyInterval
	}
	return DefaultVerifyInterval
}

// Register asks Ori to program a wake at wakeAt for one run.
//
// Registering is not scheduling. It records a request; whether a wake exists is
// a separate question, answered only by Verify.
func (c *Client) Register(runID string, wakeAt time.Time, detail string) error {
	if c.Store == nil {
		return ErrUnavailable
	}
	now := c.now()
	candidate := wakecoord.Candidate{
		ID:     runID,
		Source: c.source(),
		WakeAt: wakeAt.UTC(),
		Detail: detail,
		// An unclaimed candidate expires shortly after the wake it asked for,
		// so a helper that dies mid-run cannot leave the Mac waking up for a
		// run nobody is supervising.
		ExpiresAt: wakeAt.Add(time.Hour).UTC(),
	}
	if err := c.Store.Register(candidate, now); err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err)
	}
	return nil
}

// Verify waits until Ori reports having programmed this run's wake.
//
// It returns the instant that was actually programmed, which may be earlier
// than requested because the owner applies its own lead time. A wake programmed
// later than requested is not this wake and is refused.
func (c *Client) Verify(ctx context.Context, runID string, wakeAt time.Time) (time.Time, error) {
	if c.Store == nil {
		return time.Time{}, ErrUnavailable
	}
	// The wait is bounded by real elapsed time, not by the injected clock.
	// c.Now exists to make decisions about wake times deterministic; using it
	// here would mean a frozen test clock waits forever for another process.
	expired := time.NewTimer(c.timeout())
	defer expired.Stop()
	ticker := time.NewTicker(c.interval())
	defer ticker.Stop()

	for {
		programmed, found, err := c.Store.Programmed()
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: %s", ErrUnavailable, err)
		}
		if found && matches(programmed, c.source(), runID, wakeAt) {
			return programmed.WakeAt, nil
		}
		select {
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		case <-expired.C:
			return time.Time{}, ErrNotProgrammed
		case <-ticker.C:
		}
	}
}

// matches reports whether a programmed record describes this run's wake.
func matches(programmed wakecoord.Programmed, source, runID string, wakeAt time.Time) bool {
	if programmed.Source != source || programmed.CandidateID != runID {
		return false
	}
	if programmed.WakeAt.After(wakeAt) {
		// Programmed later than asked for: the machine would still be asleep
		// when the reset arrives.
		return false
	}
	return !programmed.WakeAt.Before(wakeAt.Add(-MaxSkew))
}

// Cancel withdraws this run's candidate and nothing else.
func (c *Client) Cancel(runID string) error {
	if c.Store == nil {
		return ErrUnavailable
	}
	if err := c.Store.Cancel(c.source(), runID, c.now()); err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err)
	}
	return nil
}

// Available reports whether the coordinator can be read at all, which is what
// doctor asks before claiming an Overnight Run could ever sleep.
func (c *Client) Available() bool {
	if c.Store == nil {
		return false
	}
	_, err := c.Store.Candidates(c.now())
	return err == nil
}

// OwnerReadiness is what the single pmset owner says it can do.
type OwnerReadiness struct {
	// Running is true when the owner published recently enough to be believed.
	Running bool
	// Ready is true when it could program a wake if asked.
	Ready bool
	// Detail explains a refusal in operator language.
	Detail string
}

// OwnerFreshness is how recently the owner must have reported to be believed.
// The Ori server publishes on every scheduler pass, so a report older than this
// means it is not running.
const OwnerFreshness = 10 * time.Minute

// Owner reports whether Ori is running and able to program a wake.
//
// The helper does not go looking for Ori's settings file — it would have to
// guess where that server keeps its state. The owner states its own capability
// in the shared store, and silence is a complete answer.
func (c *Client) Owner() OwnerReadiness {
	if c.Store == nil {
		return OwnerReadiness{Detail: "Ori's wake coordinator is unavailable"}
	}
	owner, found, err := c.Store.Owner()
	if err != nil || !found {
		return OwnerReadiness{Detail: "Ori has not reported that it can program wake events; it may not be running"}
	}
	if !owner.Fresh(c.now(), OwnerFreshness) {
		return OwnerReadiness{Detail: "Ori last reported its wake capability too long ago; it may not be running"}
	}
	switch {
	case !owner.Supported:
		return OwnerReadiness{Running: true, Detail: "this platform cannot program macOS wake events"}
	case !owner.Enabled:
		return OwnerReadiness{Running: true, Detail: "Mac wake scheduling is turned off in Ori's settings"}
	case !owner.ApprovalGranted:
		return OwnerReadiness{Running: true, Detail: "Ori has not been granted macOS approval to program wake events"}
	default:
		return OwnerReadiness{Running: true, Ready: true}
	}
}
