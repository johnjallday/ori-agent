// Package claudeusage models the structured Claude usage evidence an Overnight
// Run is allowed to act on, and nothing else.
//
// An Overnight Run may put the whole Mac to sleep. The only evidence worth that
// consequence is a structured statement that this exact Claude session
// exhausted its included five-hour window, together with the absolute reset
// timestamp Claude itself reported. Terminal text, a generic blocked status, a
// weekly cap, a context-window exhaustion, a billing error, and "detection time
// plus five hours" are all explicitly not that.
//
// The interfaces this package reads, their guarantees, and what they cannot
// tell us are documented in `docs/herdr-devflow-claude-usage-signal.md`.
// Everything here fails closed: an absent, stale, malformed, unsupported, or
// mismatched value produces an explicit reason and never an assumption.
package claudeusage

import "time"

// RecordVersion is the schema version of the records this package persists.
// A record written by a newer helper is not decoded on a guess.
const RecordVersion = 1

// SourceStatusLine and SourceStopFailure name the two supported interfaces the
// evidence can come from. They are recorded on every sample so a reader can
// tell which contract produced a value.
const (
	SourceStatusLine  = "claude:statusline"
	SourceStopFailure = "claude:stopfailure"
)

// LimitClass names which exhaustion a signal describes. Only
// LimitIncludedSession may ever drive the sleep/wake cycle; every other class
// stops unattended continuation instead.
type LimitClass string

const (
	// LimitNone means nothing is exhausted.
	LimitNone LimitClass = "none"
	// LimitIncludedSession is the included five-hour window: the one repeatable
	// limit with an authoritative reset that Ori may wait out.
	LimitIncludedSession LimitClass = "included_session"
	// LimitWeekly is the seven-day cap. Its reset is days away, so a run must
	// end rather than schedule a wake for it.
	LimitWeekly LimitClass = "weekly"
	// LimitContext is context-window exhaustion. Waiting does not fix it; only
	// a human deciding how to continue the conversation does.
	LimitContext LimitClass = "context"
	// LimitUnknown means exhaustion was reported but could not be classified.
	// It is never treated as the included-session limit.
	LimitUnknown LimitClass = "unknown"
)

// Label is the operator-facing name for one class.
func (c LimitClass) Label() string {
	switch c {
	case LimitNone:
		return "no limit"
	case LimitIncludedSession:
		return "included five-hour session limit"
	case LimitWeekly:
		return "weekly limit"
	case LimitContext:
		return "context window exhausted"
	default:
		return "unrecognized limit"
	}
}

// AuthMode is the billing posture of a Claude session. Overnight Runs are
// permitted only on plan-backed capacity.
type AuthMode string

const (
	// AuthPlanBacked means the session is running on an included Claude.ai
	// subscription allowance. It is proved positively: Claude reports the
	// subscription rate-limit windows only for Pro/Max sessions.
	AuthPlanBacked AuthMode = "plan_backed"
	// AuthUnknown means the posture could not be established — an API-key
	// session, an unsupported version, or a session that has not yet made an
	// API call all land here, and all of them are ineligible.
	AuthUnknown AuthMode = "unknown"
)

// Label is the operator-facing name for one billing posture.
func (m AuthMode) Label() string {
	if m == AuthPlanBacked {
		return "included plan capacity"
	}
	return "not established"
}

// FailureClass names why a turn stopped, as reported by the StopFailure hook's
// matcher. Only FailureRateLimit is a candidate for the sleep cycle, and even
// then only once a statusline sample says which window was exhausted.
type FailureClass string

const (
	FailureRateLimit            FailureClass = "rate_limit"
	FailureOverloaded           FailureClass = "overloaded"
	FailureAuthenticationFailed FailureClass = "authentication_failed"
	FailureOAuthOrgNotAllowed   FailureClass = "oauth_org_not_allowed"
	FailureBillingError         FailureClass = "billing_error"
	FailureInvalidRequest       FailureClass = "invalid_request"
	FailureModelNotFound        FailureClass = "model_not_found"
	FailureServerError          FailureClass = "server_error"
	FailureMaxOutputTokens      FailureClass = "max_output_tokens"
	FailureUnknown              FailureClass = "unknown"
)

// Window is one rate-limit window as Claude reported it.
//
// Present distinguishes "Claude reported this window" from a zero-valued
// window, which is the difference between a fresh allowance and no evidence
// at all.
type Window struct {
	// Present is true only when Claude actually reported this window.
	Present bool `json:"present"`
	// UsedPercentage is 0–100 consumption of the window.
	UsedPercentage float64 `json:"used_percentage"`
	// ResetsAt is the absolute time Claude reported for the window's reset.
	// It is never derived by adding a duration to anything.
	ResetsAt time.Time `json:"resets_at,omitzero"`
}

// Exhausted reports whether the window has no allowance left. The threshold is
// deliberately the reported maximum rather than a margin below it: acting early
// would sleep the Mac while usable capacity remained.
func (w Window) Exhausted() bool { return w.Present && w.UsedPercentage >= 100 }

// Sample is one observation of a Claude session's usage, captured from the
// statusLine payload. It carries no prompt text, no terminal content, no
// account identity, and no credentials.
type Sample struct {
	// Version is the record schema version.
	Version int `json:"version"`
	// Source names the interface this sample came from.
	Source string `json:"source"`
	// SessionID is the exact native Claude session the sample describes.
	SessionID string `json:"session_id"`
	// ClaudeVersion is the Claude Code version that produced the payload, which
	// is what the supported-version gate checks.
	ClaudeVersion string `json:"claude_version,omitempty"`
	// ObservedAt is when the sample was captured.
	ObservedAt time.Time `json:"observed_at"`
	// FiveHour is the included-session window, and SevenDay the weekly cap.
	FiveHour Window `json:"five_hour"`
	SevenDay Window `json:"seven_day"`
	// ContextUsedPercentage is context-window consumption, reported separately
	// so context exhaustion can never be mistaken for a session limit.
	ContextUsedPercentage float64 `json:"context_used_percentage"`
	// ContextPresent distinguishes an unreported context window from an empty one.
	ContextPresent bool `json:"context_present"`
}

// PlanBacked reports whether this sample proves included-plan capacity. Claude
// reports the subscription windows only for Pro/Max sessions, so their presence
// is the proof and their absence is a refusal, never an inference.
func (s Sample) PlanBacked() bool { return s.FiveHour.Present || s.SevenDay.Present }

// AuthMode maps the sample onto the billing posture vocabulary.
func (s Sample) AuthMode() AuthMode {
	if s.PlanBacked() {
		return AuthPlanBacked
	}
	return AuthUnknown
}

// Fresh reports whether the sample is recent enough to describe the current
// window. A sample older than the window it claims to describe is evidence
// about the past.
func (s Sample) Fresh(now time.Time, window time.Duration) bool {
	if s.ObservedAt.IsZero() || window <= 0 {
		return false
	}
	if s.ObservedAt.After(now) {
		// A sample from the future means a clock moved; refuse rather than
		// treat it as the freshest evidence available.
		return false
	}
	return !s.ObservedAt.Add(window).Before(now)
}

// Failure is one turn-stopped event captured from the StopFailure hook.
type Failure struct {
	Version int    `json:"version"`
	Source  string `json:"source"`
	// SessionID is the exact native Claude session whose turn stopped.
	SessionID string `json:"session_id"`
	// Class is the error class the hook matcher selected. A recorder registered
	// for one matcher writes that class; it is never inferred from a message.
	Class FailureClass `json:"class"`
	// ObservedAt is when the turn stopped.
	ObservedAt time.Time `json:"observed_at"`
	// ClaudeVersion is the Claude Code version that fired the hook, when known.
	ClaudeVersion string `json:"claude_version,omitempty"`
}

// ReadinessCode is a stable machine-readable reason a session is not ready for
// unattended execution.
type ReadinessCode string

const (
	// ReadyOK means every checked requirement passed.
	ReadyOK ReadinessCode = "ready"
	// ReadyRecorderMissing means no usage record exists for the session, so the
	// Claude-side recorder is probably not installed.
	ReadyRecorderMissing ReadinessCode = "recorder_missing"
	// ReadySampleStale means the newest record is too old to describe the
	// current window.
	ReadySampleStale ReadinessCode = "sample_stale"
	// ReadySampleMalformed means a record existed but could not be decoded.
	ReadySampleMalformed ReadinessCode = "sample_malformed"
	// ReadySessionMismatch means the newest record describes a different
	// Claude session than the one enrolled.
	ReadySessionMismatch ReadinessCode = "session_mismatch"
	// ReadyUnsupportedVersion means the record came from a Claude Code version
	// this adapter does not claim to understand.
	ReadyUnsupportedVersion ReadinessCode = "unsupported_version"
	// ReadyNotPlanBacked means the session did not report subscription windows,
	// so included-plan capacity could not be proved.
	ReadyNotPlanBacked ReadinessCode = "not_plan_backed"
)

// Readiness is the adapter's answer about one session, with a reason that is
// safe to print. It never carries account identity or credentials.
type Readiness struct {
	// Ready is true only when every requirement was checked and passed.
	Ready bool `json:"ready"`
	// Code is the stable reason, ReadyOK when ready.
	Code ReadinessCode `json:"code"`
	// Reason is a plain-language sentence naming what is missing.
	Reason string `json:"reason,omitempty"`
	// AuthMode is the established billing posture.
	AuthMode AuthMode `json:"auth_mode"`
	// ObservedAt is when the evidence behind this answer was captured.
	ObservedAt time.Time `json:"observed_at,omitzero"`
	// ClaudeVersion is the version that produced that evidence.
	ClaudeVersion string `json:"claude_version,omitempty"`
}

// Signal is a classified exhaustion for one session: what ran out, when it
// resets, and whether that combination may drive a sleep cycle.
type Signal struct {
	// SessionID is the exact native Claude session the signal describes.
	SessionID string `json:"session_id"`
	// Class is which exhaustion was recognized.
	Class LimitClass `json:"class"`
	// AuthMode is the billing posture established alongside it.
	AuthMode AuthMode `json:"auth_mode"`
	// DetectedAt is when the exhaustion was observed.
	DetectedAt time.Time `json:"detected_at"`
	// ResetAt is Claude's own absolute reset timestamp for the exhausted
	// window. It is zero unless Claude reported one.
	ResetAt time.Time `json:"reset_at,omitzero"`
	// Sleepable is true only for an included-session limit whose reset is a
	// trustworthy future time on a plan-backed session. It is the single field
	// the sleep sequence is allowed to read.
	Sleepable bool `json:"sleepable"`
	// Reason explains the classification, and in particular why a signal that
	// looks like a limit is not sleepable.
	Reason string `json:"reason,omitempty"`
	// ClaudeVersion is the version that produced the evidence.
	ClaudeVersion string `json:"claude_version,omitempty"`
}
