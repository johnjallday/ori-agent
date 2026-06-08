package agenthttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// LoggingHomeAskTraceEmitter records home-harness outcomes as structured logs
// under the home_assistant.ask scope, mirroring the route-intake trace logging in
// TraceHandler. It deliberately does not persist into the route-intake table,
// which feeds workspace-correction learning and would be polluted by inline asks.
type LoggingHomeAskTraceEmitter struct{}

// NewLoggingHomeAskTraceEmitter returns the default home-harness telemetry emitter.
func NewLoggingHomeAskTraceEmitter() *LoggingHomeAskTraceEmitter {
	return &LoggingHomeAskTraceEmitter{}
}

// RecordAskOutcome emits one structured telemetry line per home-harness outcome.
func (e *LoggingHomeAskTraceEmitter) RecordAskOutcome(_ context.Context, trace HomeAskTrace) {
	logger.Info("Home assistant ask", logger.Fields{
		"scope":          "home_assistant.ask",
		"prompt":         trace.Prompt,
		"intent":         trace.Intent,
		"window":         trace.Window,
		"outcome":        trace.Outcome,
		"action_count":   trace.ActionCount,
		"confirmed_type": trace.ConfirmedType,
		"degraded":       append([]string(nil), trace.Degraded...),
	})
}
