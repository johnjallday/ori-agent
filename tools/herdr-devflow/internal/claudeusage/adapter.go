package claudeusage

import (
	"errors"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

const (
	// MinimumClaudeVersion is the oldest Claude Code version whose statusLine
	// payload this adapter claims to understand. The contract in
	// `docs/herdr-devflow-claude-usage-signal.md` was verified against it. An
	// older version is refused rather than assumed to carry the same shape.
	MinimumClaudeVersion = "2.1.220"
	// DefaultFreshness bounds how old a usage sample may be and still describe
	// the current window. Claude re-renders its status line continuously while a
	// session is alive, so a sample older than this means the recorder stopped
	// reporting — which is a reason to stay awake, not to act on stale numbers.
	DefaultFreshness = 15 * time.Minute
)

// Adapter answers two questions about one exact Claude session: may it be run
// unattended, and has it hit the one limit worth sleeping the Mac for.
//
// Every answer is derived from records the Claude-side recorder persisted. The
// adapter never contacts Claude, never inspects credentials, never reads a
// transcript, and never spends anything.
type Adapter struct {
	// Store reads the persisted records.
	Store *Store
	// Freshness bounds sample age; zero uses DefaultFreshness.
	Freshness time.Duration
	// MinimumVersion overrides the supported-version floor in tests.
	MinimumVersion string
}

// NewAdapter builds an Adapter over a usage directory.
func NewAdapter(dir string) *Adapter { return &Adapter{Store: NewStore(dir)} }

func (a *Adapter) freshness() time.Duration {
	if a.Freshness > 0 {
		return a.Freshness
	}
	return DefaultFreshness
}

func (a *Adapter) minimumVersion() string {
	if a.MinimumVersion != "" {
		return a.MinimumVersion
	}
	return MinimumClaudeVersion
}

// BuildPlanProof snapshots only positive, fresh, supported Claude.ai plan
// evidence for one exact session. It never contacts Claude or refreshes a
// status line, so confirmation cannot consume credits.
func (a *Adapter) BuildPlanProof(sessionID string, now, expiresAt time.Time) (model.PlanProof, error) {
	readiness := a.Readiness(sessionID, now)
	if !readiness.Ready || readiness.AuthMode != AuthPlanBacked {
		return model.PlanProof{}, fmt.Errorf("cannot create included-plan proof: %s", readiness.Reason)
	}
	if expiresAt.IsZero() || !expiresAt.After(now) {
		return model.PlanProof{}, errors.New("included-plan proof must expire at a future run deadline")
	}
	return model.PlanProof{
		FormatVersion: PlanProofVersion, SessionID: sessionID, ClaudeVersion: readiness.ClaudeVersion,
		ObservedAt: readiness.ObservedAt, ExpiresAt: expiresAt.UTC(), PlanBacked: true,
	}, nil
}

// ValidatePlanProof permits a scheduled run to retain its confirmation-time
// authorization when its current window sample has become stale. Only expiry,
// identity drift, or newer contradictory billing/authentication evidence can
// invalidate it; absence of a newer status line is not a contradiction.
func (a *Adapter) ValidatePlanProof(proof model.PlanProof, sessionID string, now time.Time) error {
	if proof.FormatVersion != PlanProofVersion || !proof.PlanBacked || proof.SessionID == "" || proof.SessionID != sessionID {
		return errors.New("the stored included-plan proof does not match this exact native Claude session")
	}
	if proof.ExpiresAt.IsZero() || !now.Before(proof.ExpiresAt) {
		return errors.New("the stored included-plan proof expired at the run deadline")
	}
	if a.Store == nil {
		return nil
	}
	if sample, err := a.Store.Sample(sessionID); err == nil && sample.ObservedAt.After(proof.ObservedAt) {
		if !a.supportedVersion(sample.ClaudeVersion) || !sample.PlanBacked() {
			return errors.New("newer Claude usage evidence contradicts the stored included-plan proof")
		}
	}
	if failure, err := a.Store.Failure(sessionID); err == nil && failure.ObservedAt.After(proof.ObservedAt) {
		switch failure.Class {
		case FailureBillingError, FailureAuthenticationFailed, FailureOAuthOrgNotAllowed:
			return errors.New("newer Claude billing or authentication evidence contradicts the stored included-plan proof")
		}
	}
	return nil
}

// Installed reports whether the Claude-side recorder has ever written a record.
func (a *Adapter) Installed() bool { return a.Store != nil && a.Store.Installed() }

// Readiness reports whether sessionID may be run unattended.
//
// It is deliberately a refusal by default. Every path that cannot positively
// establish a fresh, supported, plan-backed record for this exact session
// returns a reason instead of a benefit of the doubt.
func (a *Adapter) Readiness(sessionID string, now time.Time) Readiness {
	if a.Store == nil || sessionID == "" {
		return Readiness{
			Code:     ReadyRecorderMissing,
			Reason:   "No Claude usage recorder is configured for this session.",
			AuthMode: AuthUnknown,
		}
	}
	sample, err := a.Store.Sample(sessionID)
	if err != nil {
		return notReady(err)
	}
	if !sample.Fresh(now, a.freshness()) {
		return Readiness{
			Code: ReadySampleStale,
			Reason: "The newest Claude usage record for this session is too old to describe the current window; " +
				"the recorder may have stopped reporting.",
			AuthMode:      AuthUnknown,
			ObservedAt:    sample.ObservedAt,
			ClaudeVersion: sample.ClaudeVersion,
		}
	}
	if !a.supportedVersion(sample.ClaudeVersion) {
		return Readiness{
			Code: ReadyUnsupportedVersion,
			Reason: "This Claude Code version is not one the usage adapter claims to understand; " +
				"unattended execution needs " + a.minimumVersion() + " or newer.",
			AuthMode:      AuthUnknown,
			ObservedAt:    sample.ObservedAt,
			ClaudeVersion: sample.ClaudeVersion,
		}
	}
	if !sample.PlanBacked() {
		return Readiness{
			Code: ReadyNotPlanBacked,
			Reason: "This session reported no Claude.ai subscription window, so included-plan capacity " +
				"could not be established; an Overnight Run never spends usage or API credits.",
			AuthMode:      AuthUnknown,
			ObservedAt:    sample.ObservedAt,
			ClaudeVersion: sample.ClaudeVersion,
		}
	}
	return Readiness{
		Ready:         true,
		Code:          ReadyOK,
		AuthMode:      AuthPlanBacked,
		ObservedAt:    sample.ObservedAt,
		ClaudeVersion: sample.ClaudeVersion,
	}
}

// notReady maps a store error onto a refusal with a stable code.
func notReady(err error) Readiness {
	if errors.Is(err, ErrNoRecord) {
		return Readiness{
			Code: ReadyRecorderMissing,
			Reason: "No Claude usage record exists for this session, so its limit and billing posture " +
				"are unknown.",
			AuthMode: AuthUnknown,
		}
	}
	return Readiness{
		Code:     ReadySampleMalformed,
		Reason:   "The Claude usage record for this session could not be read.",
		AuthMode: AuthUnknown,
	}
}

func (a *Adapter) supportedVersion(raw string) bool {
	observed, err := config.ParseVersion(raw)
	if err != nil {
		return false
	}
	minimum, err := config.ParseVersion(a.minimumVersion())
	if err != nil {
		return false
	}
	return observed.AtLeast(minimum)
}

// Classify decides what, if anything, this session has run out of.
//
// lastHandledReset is the reset boundary this participant already consumed. A
// signal repeating an old boundary is stale evidence, and acting on it is how a
// wake/sleep loop would form after a restart, so it is never sleepable.
func (a *Adapter) Classify(sessionID string, now time.Time, lastHandledReset time.Time) Signal {
	signal := Signal{SessionID: sessionID, Class: LimitUnknown, AuthMode: AuthUnknown, DetectedAt: now}

	readiness := a.Readiness(sessionID, now)
	if !readiness.Ready {
		signal.Class = LimitUnknown
		signal.Reason = readiness.Reason
		signal.ClaudeVersion = readiness.ClaudeVersion
		return signal
	}
	signal.AuthMode = readiness.AuthMode
	signal.ClaudeVersion = readiness.ClaudeVersion

	sample, err := a.Store.Sample(sessionID)
	if err != nil {
		signal.Reason = "The Claude usage record for this session could not be read."
		return signal
	}

	// A stopped turn is what makes exhaustion an event rather than a reading.
	// Its class comes from the matcher the recorder was registered under, so a
	// billing error or an authentication failure cannot arrive here disguised
	// as a rate limit.
	failure, failureErr := a.Store.Failure(sessionID)
	stoppedOnRateLimit := failureErr == nil &&
		failure.Class == FailureRateLimit &&
		!failure.ObservedAt.Before(sample.ObservedAt.Add(-a.freshness()))

	switch {
	case failureErr == nil && failure.Class == FailureBillingError:
		signal.Class = LimitUnknown
		signal.DetectedAt = failure.ObservedAt
		signal.Reason = "This session stopped on a billing error. An Overnight Run never resolves one, " +
			"because doing so would mean spending credits."
		return signal
	case failureErr == nil && isBlockingFailure(failure.Class):
		signal.Class = LimitUnknown
		signal.DetectedAt = failure.ObservedAt
		signal.Reason = "This session stopped on a " + string(failure.Class) +
			" failure, which is not a usage limit and is not waited out."
		return signal
	}

	if sample.FiveHour.Exhausted() {
		signal.Class = LimitIncludedSession
		signal.ResetAt = sample.FiveHour.ResetsAt
		if stoppedOnRateLimit {
			signal.DetectedAt = failure.ObservedAt
		}
		signal.Sleepable, signal.Reason = a.sleepable(signal, stoppedOnRateLimit, now, lastHandledReset)
		return signal
	}
	if sample.SevenDay.Exhausted() {
		signal.Class = LimitWeekly
		signal.ResetAt = sample.SevenDay.ResetsAt
		signal.Reason = "The weekly limit is exhausted. Its reset is days away, so unattended " +
			"continuation ends instead of scheduling a wake."
		return signal
	}
	if sample.ContextPresent && sample.ContextUsedPercentage >= 100 {
		signal.Class = LimitContext
		signal.Reason = "This session exhausted its context window. Waiting does not restore it, so it " +
			"needs a decision rather than a sleep."
		return signal
	}
	if stoppedOnRateLimit {
		// A rate limit stopped the turn but no window reports itself exhausted.
		// That disagreement is exactly the case to refuse: something is limited
		// and the adapter cannot say which window or when it returns.
		signal.Class = LimitUnknown
		signal.DetectedAt = failure.ObservedAt
		signal.Reason = "This session stopped on a rate limit, but no reported window is exhausted, " +
			"so the limit could not be identified."
		return signal
	}
	signal.Class = LimitNone
	signal.Reason = "No reported window is exhausted."
	return signal
}

// sleepable applies every remaining condition to an included-session limit.
// Each returns its own refusal, because "not sleepable" without a reason is
// indistinguishable from a bug.
func (a *Adapter) sleepable(signal Signal, stoppedOnRateLimit bool, now, lastHandledReset time.Time) (bool, string) {
	switch {
	case !stoppedOnRateLimit:
		return false, "The included five-hour window reports itself exhausted, but no rate-limited turn " +
			"was recorded for this session, so the limit is not confirmed."
	case signal.ResetAt.IsZero():
		return false, "Claude reported no reset time for the exhausted window, and a reset is never " +
			"calculated from the detection time."
	case !signal.ResetAt.After(now):
		return false, "The reported reset time has already passed, so it describes an older window."
	case !lastHandledReset.IsZero() && !signal.ResetAt.After(lastHandledReset):
		return false, "This reset boundary was already handled, so acting on it again would repeat a " +
			"sleep and wake cycle."
	default:
		return true, ""
	}
}

// isBlockingFailure reports whether a failure class stops unattended work
// outright. A rate limit is deliberately absent: it is the one class that may
// continue, and only after a window says which limit it was.
func isBlockingFailure(class FailureClass) bool {
	switch class {
	case FailureAuthenticationFailed, FailureOAuthOrgNotAllowed, FailureBillingError,
		FailureInvalidRequest, FailureModelNotFound, FailureMaxOutputTokens:
		return true
	default:
		// Overloaded and server errors are transient and unknown is unclassified;
		// none of them is a limit to wait out, but none is a permanent stop
		// either, so they fall through to the window evidence.
		return false
	}
}
