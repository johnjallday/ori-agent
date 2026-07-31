// Package systempower inspects the machine's power source and, on macOS only,
// puts it to sleep.
//
// Sleeping the whole Mac is the single most consequential thing this feature
// does, so the operation is behind an interface with a fake in every test. No
// test in this repository may sleep a developer's machine, and the way that is
// guaranteed is that the real runner is never constructed unless a caller asks
// for it explicitly on a Darwin build.
package systempower

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrUnsupported means this platform cannot be put to sleep by Ori.
//
// It is a refusal, not a fallback: there is no substitute for system sleep, and
// pretending otherwise would leave a run believing it had slept.
var ErrUnsupported = errors.New("system sleep is supported on macOS only")

// Source is where the machine's power comes from.
type Source string

const (
	// SourceAC means external power. An Overnight Run requires it: a Mac that
	// runs out of battery while asleep does not wake up for a Claude reset.
	SourceAC Source = "ac"
	// SourceBattery means the machine is running on battery.
	SourceBattery Source = "battery"
	// SourceUnknown means the power source could not be established, which is
	// treated exactly like battery: not good enough to sleep on.
	SourceUnknown Source = "unknown"
)

// External reports whether the machine is on external power.
func (s Source) External() bool { return s == SourceAC }

// Label is the operator-facing name.
func (s Source) Label() string {
	switch s {
	case SourceAC:
		return "external power"
	case SourceBattery:
		return "battery"
	default:
		return "unknown"
	}
}

// Runner executes the bounded platform commands this package needs. Injecting
// it is what keeps the test suite from ever touching real power state.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Service answers power questions and performs sleep.
type Service struct {
	// GOOS is the platform; empty uses the build's own.
	GOOS string
	// Run executes a command; nil uses the real one.
	Run Runner
	// Timeout bounds one command.
	Timeout time.Duration
	// Assertion hooks are test seams for the user-level caffeinate assertion.
	AcquireAssertion func(context.Context, string) (string, error)
	CheckAssertion   func(context.Context, string) bool
	ReleaseAssertion func(context.Context, string) error
	assertions       sync.Map
}

// DefaultTimeout bounds one power command.
const DefaultTimeout = 15 * time.Second

func (s *Service) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultTimeout
}
