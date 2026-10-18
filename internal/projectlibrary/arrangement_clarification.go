package projectlibrary

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

const arrangementClarification = "clarification"

// SaveArrangementClarification completes a discussion-only planning turn in the
// same canonical envelope/CAS as proposals. The host supplies a source-bound
// record carrying the goal and provenance, with no proposed effects or selected
// members. This is not a proposal tool and creates no separate chat transcript.
func (s *Store) SaveArrangementClarification(ctx context.Context, owner string, authority ManagerAuthority, expected int64, allowUpgrade bool, record workspacearrangement.Record, answer string) (Arrangement, bool, error) {
	if record.State != "needs_more_evidence" || !validClarification(answer) {
		return Arrangement{}, false, ErrCorrupt
	}
	return s.saveArrangementOutcome(ctx, owner, authority, expected, allowUpgrade, record, arrangementClarification, answer)
}

func validClarification(answer string) bool {
	if strings.TrimSpace(answer) == "" || len(answer) > workspacearrangement.MaxDraftBytes || !utf8.ValidString(answer) {
		return false
	}
	for _, r := range answer {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

func (a Arrangement) validOutcome() bool {
	if a.Kind == "" {
		return a.Clarification == ""
	}
	if a.Kind != arrangementClarification || a.Record.Origin != "model" {
		return false
	}
	r, d := a.Record, a.Record.Draft
	if (r.State != "needs_more_evidence" && r.State != "stale") || len(d.Operations) != 0 || len(d.NeedsReview) != 0 ||
		len(d.Unchanged.IDs) != 0 || d.Unchanged.Handle != "" || len(r.ItemRevisions) != 0 || len(r.CollectionRevisions) != 0 ||
		d.Rationale != "" || len(d.Unknowns) != 0 {
		return false
	}
	// Forget preserves the outcome kind but erases the model's free text.
	return validClarification(a.Clarification) || (a.Clarification == "" && r.State == "stale")
}

func (a Arrangement) outcomeKind() string {
	if a.Kind == arrangementClarification {
		return arrangementClarification
	}
	return "proposal"
}
