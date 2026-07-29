package claudeusage

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// maxPayloadBytes bounds what will be decoded from a Claude payload. The
// statusLine and hook payloads are small structured objects; anything far
// larger is not the contract this package understands, and decoding it would
// only make a mistake more expensive.
const maxPayloadBytes = 256 * 1024

// ErrPayloadTooLarge means the payload exceeded the bounded size.
var ErrPayloadTooLarge = errors.New("claude payload is larger than this adapter accepts")

// statusLinePayload is the subset of Claude Code's statusLine JSON this package
// reads. Every other field the payload carries — prompts, transcripts, costs,
// git state, workspace paths — is deliberately not decoded: what is never read
// cannot be persisted, logged, or leaked.
type statusLinePayload struct {
	SessionID  string `json:"session_id"`
	Version    string `json:"version"`
	RateLimits *struct {
		FiveHour *windowPayload `json:"five_hour"`
		SevenDay *windowPayload `json:"seven_day"`
	} `json:"rate_limits"`
	ContextWindow *struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
}

type windowPayload struct {
	UsedPercentage *float64 `json:"used_percentage"`
	// ResetsAt is Unix epoch seconds. Claude reports an absolute instant; this
	// package never computes one.
	ResetsAt *int64 `json:"resets_at"`
}

// DecodeStatusLine converts one raw statusLine payload into a Sample.
//
// Absence is preserved rather than defaulted. A missing `rate_limits` object is
// what an API-key session looks like, and a missing window is what a session
// that has not yet made an API call looks like; turning either into a
// zero-valued window would manufacture the very evidence this package exists to
// demand.
func DecodeStatusLine(raw []byte, observedAt time.Time) (Sample, error) {
	if len(raw) > maxPayloadBytes {
		return Sample{}, ErrPayloadTooLarge
	}
	var payload statusLinePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Sample{}, fmt.Errorf("decode Claude statusline payload: %w", err)
	}
	if payload.SessionID == "" {
		return Sample{}, errors.New("Claude statusline payload carried no session id")
	}
	sample := Sample{
		Version:       RecordVersion,
		Source:        SourceStatusLine,
		SessionID:     payload.SessionID,
		ClaudeVersion: payload.Version,
		ObservedAt:    observedAt.UTC(),
	}
	if payload.RateLimits != nil {
		sample.FiveHour = decodeWindow(payload.RateLimits.FiveHour)
		sample.SevenDay = decodeWindow(payload.RateLimits.SevenDay)
	}
	if payload.ContextWindow != nil && payload.ContextWindow.UsedPercentage != nil {
		sample.ContextPresent = true
		sample.ContextUsedPercentage = *payload.ContextWindow.UsedPercentage
	}
	return sample, nil
}

func decodeWindow(payload *windowPayload) Window {
	if payload == nil || payload.UsedPercentage == nil {
		return Window{}
	}
	window := Window{Present: true, UsedPercentage: *payload.UsedPercentage}
	if payload.ResetsAt != nil && *payload.ResetsAt > 0 {
		window.ResetsAt = time.Unix(*payload.ResetsAt, 0).UTC()
	}
	return window
}

// stopFailurePayload is the subset of a StopFailure hook's common input this
// package reads. The error class is not taken from the payload: a recorder is
// registered against one matcher, so the class is known from the registration
// and cannot be spoofed by an undocumented field changing shape.
type stopFailurePayload struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	Version       string `json:"version"`
}

// DecodeStopFailure converts one raw StopFailure hook payload into a Failure.
// class comes from the matcher the recorder was registered under.
func DecodeStopFailure(raw []byte, class FailureClass, observedAt time.Time) (Failure, error) {
	if len(raw) > maxPayloadBytes {
		return Failure{}, ErrPayloadTooLarge
	}
	if !knownFailureClass(class) {
		return Failure{}, fmt.Errorf("unrecognized Claude failure class %q", class)
	}
	var payload stopFailurePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Failure{}, fmt.Errorf("decode Claude stop-failure payload: %w", err)
	}
	if payload.SessionID == "" {
		return Failure{}, errors.New("Claude stop-failure payload carried no session id")
	}
	if payload.HookEventName != "" && payload.HookEventName != "StopFailure" {
		return Failure{}, fmt.Errorf("payload is a %s event, not StopFailure", payload.HookEventName)
	}
	return Failure{
		Version:       RecordVersion,
		Source:        SourceStopFailure,
		SessionID:     payload.SessionID,
		Class:         class,
		ObservedAt:    observedAt.UTC(),
		ClaudeVersion: payload.Version,
	}, nil
}

func knownFailureClass(class FailureClass) bool {
	switch class {
	case FailureRateLimit, FailureOverloaded, FailureAuthenticationFailed,
		FailureOAuthOrgNotAllowed, FailureBillingError, FailureInvalidRequest,
		FailureModelNotFound, FailureServerError, FailureMaxOutputTokens, FailureUnknown:
		return true
	default:
		return false
	}
}
