package workspacerun

import (
	"strconv"
	"time"

	"github.com/google/uuid"
)

const DefaultTraceTailLimit = 50
const DefaultTracePageLimit = 500
const MaxTracePageLimit = 500

type TracePage struct {
	Events    []TraceEvent `json:"events"`
	NextSince int64        `json:"next_since"`
	HasMore   bool         `json:"has_more"`
}

func NewTraceEvent(runID string, kind TraceEventKind, opts ...TraceOption) TraceEvent {
	event := TraceEvent{
		ID:        uuid.New().String(),
		RunID:     runID,
		Kind:      kind,
		CreatedAt: time.Now(),
	}
	for _, opt := range opts {
		opt(&event)
	}
	return event
}

type TraceOption func(*TraceEvent)

func TraceSource(source string) TraceOption {
	return func(e *TraceEvent) {
		e.Source = source
	}
}

func TraceMessageText(message string) TraceOption {
	return func(e *TraceEvent) {
		e.Message = message
	}
}

func TraceStatus(status RunStatus) TraceOption {
	return func(e *TraceEvent) {
		e.Status = string(status)
	}
}

func TraceToolName(name string) TraceOption {
	return func(e *TraceEvent) {
		e.ToolName = name
	}
}

func TraceArtifactID(id string) TraceOption {
	return func(e *TraceEvent) {
		e.ArtifactID = id
	}
}

func TraceData(data map[string]interface{}) TraceOption {
	return func(e *TraceEvent) {
		e.Data = cloneMap(data)
	}
}

func StatusTrace(runID string, status RunStatus, message string) TraceEvent {
	return NewTraceEvent(runID, TraceStatusChange, TraceSource("lifecycle"), TraceStatus(status), TraceMessageText(message))
}

func ErrorTrace(runID, source, message string) TraceEvent {
	return NewTraceEvent(runID, TraceError, TraceSource(source), TraceMessageText(message))
}

func ArtifactCapturedTrace(runID, artifactID string, kind ArtifactKind) TraceEvent {
	return NewTraceEvent(runID, TraceArtifactCaptured, TraceSource("artifact"), TraceArtifactID(artifactID), TraceData(map[string]interface{}{"kind": string(kind)}))
}

func ValidationTrace(runID, checkName, status string) TraceEvent {
	return NewTraceEvent(runID, TraceValidationCheck, TraceSource("validator"), TraceMessageText(checkName), TraceData(map[string]interface{}{"status": status}))
}

func CloneTraceEvent(event TraceEvent) TraceEvent {
	event.Data = cloneMap(event.Data)
	return event
}

func CloneTraceEvents(events []TraceEvent) []TraceEvent {
	if events == nil {
		return nil
	}
	out := make([]TraceEvent, len(events))
	for i, event := range events {
		out[i] = CloneTraceEvent(event)
	}
	return out
}

func TraceTail(events []TraceEvent, limit int) []TraceEvent {
	if limit <= 0 {
		limit = DefaultTraceTailLimit
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	return CloneTraceEvents(events)
}

func TracePageAfter(events []TraceEvent, since int64, limit int) TracePage {
	if limit <= 0 || limit > MaxTracePageLimit {
		limit = DefaultTracePageLimit
	}
	filtered := make([]TraceEvent, 0, minInt(len(events), limit+1))
	for _, event := range events {
		if event.Sequence > since {
			filtered = append(filtered, event)
			if len(filtered) > limit {
				break
			}
		}
	}
	hasMore := len(filtered) > limit
	if hasMore {
		filtered = filtered[:limit]
	}
	nextSince := since
	if len(filtered) > 0 {
		nextSince = filtered[len(filtered)-1].Sequence
	}
	return TracePage{
		Events:    CloneTraceEvents(filtered),
		NextSince: nextSince,
		HasMore:   hasMore,
	}
}

func ParseSince(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.ParseInt(value, 10, 64)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
