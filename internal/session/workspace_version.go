package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// ErrWorkspaceVersionConflict reports a conditional workspace update whose
// stored version no longer equals the version the writer read. The row is
// left untouched; the caller must reload the workspace and decide again.
var ErrWorkspaceVersionConflict = errors.New("workspace version conflict")

// workspaceVersionUpdater is the optional conditional-update capability of a
// HybridStore. WorkspaceStoreAdapter uses it for fenced saves and falls back
// to the plain update when a store (a test double, say) lacks it.
type workspaceVersionUpdater interface {
	UpdateWorkspaceExpecting(ctx context.Context, workspace *Workspace, expectedVersion int64) error
}

// assistantProgramEnvelopeProtected reports whether a stored
// assistant_program_json column carries an Assistant Home state or a split
// project link. Such rows refuse a stale generic update; an unreadable
// non-empty envelope counts as protected so it is never overwritten blindly.
func assistantProgramEnvelopeProtected(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return false
	}
	var envelope struct {
		State json.RawMessage `json:"state"`
		Link  json.RawMessage `json:"link"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return true
	}
	return rawJSONPresent(envelope.State) || rawJSONPresent(envelope.Link)
}

func rawJSONPresent(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}
