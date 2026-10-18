package workspace

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Dismissed-suggestion ledger (workspace-goal-suggestions FR20).
//
// When a suggested ticket leaves the Backlog without being kept — Not useful,
// or deleted by any other path — its title is remembered here so goal runs
// never suggest it again (FR18) and the prompt can name it.
//
// The ledger lives on the workspace record rather than in a sidecar file:
// every one of the task-deletion paths (Backlog, Tickets, the task API,
// orchestration, agent communication) goes through Workspace.DeleteTask inside
// a store update, so recording it there is atomic with the delete and cannot be
// skipped by a path that forgot a hook. The record persists to workspace.json,
// so the ledger still travels with the folder.

// SuggestionLedgerLimit bounds how many dismissed suggestions a workspace keeps.
const SuggestionLedgerLimit = 200

// SuggestionReasonMaxLength bounds the optional Not useful reason.
const SuggestionReasonMaxLength = 200

// ErrNotASuggestion is returned when Not useful targets a ticket a goal run did
// not suggest.
var ErrNotASuggestion = errors.New("only a suggested ticket can be marked not useful")

// DismissedSuggestion is one ledger entry.
type DismissedSuggestion struct {
	NormalizedTitle string    `json:"normalized_title"`
	Title           string    `json:"title"`
	Reason          string    `json:"reason,omitempty"`
	DismissedAt     time.Time `json:"dismissed_at"`
	RunID           string    `json:"run_id,omitempty"`
}

// recordDismissedSuggestionLocked adds task to the ledger when a goal run
// suggested it. A title already in the ledger is refreshed in place. Callers
// hold w.mu.
func (w *Workspace) recordDismissedSuggestionLocked(task Task, reason string, at time.Time) {
	if task.SourceType != TicketSourceGoalSuggestion {
		return
	}
	key := NormalizeSuggestionTitle(task.Description)
	if key == "" {
		return
	}
	entry := DismissedSuggestion{
		NormalizedTitle: key,
		Title:           strings.TrimSpace(task.Description),
		Reason:          normalizeSuggestionReason(reason),
		DismissedAt:     at.UTC(),
		RunID:           strings.TrimSpace(task.SourceID),
	}
	kept := make([]DismissedSuggestion, 0, len(w.DismissedSuggestions)+1)
	kept = append(kept, entry)
	for _, existing := range w.DismissedSuggestions {
		if existing.NormalizedTitle != key {
			kept = append(kept, existing)
		}
	}
	if len(kept) > SuggestionLedgerLimit {
		kept = kept[:SuggestionLedgerLimit]
	}
	w.DismissedSuggestions = kept
}

// normalizeSuggestionReason keeps a reason to one bounded line.
func normalizeSuggestionReason(reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	runes := []rune(reason)
	if len(runes) > SuggestionReasonMaxLength {
		reason = string(runes[:SuggestionReasonMaxLength])
	}
	return reason
}

// DismissSuggestion removes a suggested ticket as not useful, recording the
// optional reason in the ledger atomically with the delete.
func (w *Workspace) DismissSuggestion(taskID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	idx := w.findTaskIdxLocked(taskID)
	if idx == -1 {
		return fmt.Errorf("task %q not found in workspace", taskID)
	}
	if w.Tasks[idx].SourceType != TicketSourceGoalSuggestion {
		return ErrNotASuggestion
	}
	return w.deleteTaskLocked(taskID, reason)
}

// DismissedSuggestionKeys returns the ledger's normalized titles.
func (w *Workspace) DismissedSuggestionKeys() map[string]struct{} {
	keys := map[string]struct{}{}
	if w == nil {
		return keys
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, entry := range w.DismissedSuggestions {
		keys[entry.NormalizedTitle] = struct{}{}
	}
	return keys
}

// DismissedSuggestionTitles returns the ledger's titles, most recent first.
func (w *Workspace) DismissedSuggestionTitles() []string {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	titles := make([]string, 0, len(w.DismissedSuggestions))
	for _, entry := range w.DismissedSuggestions {
		titles = append(titles, entry.Title)
	}
	return titles
}
