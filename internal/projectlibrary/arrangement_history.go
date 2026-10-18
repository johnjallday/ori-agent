package projectlibrary

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// ArrangementRef is owner-visible history, not Manager or source authority.
// CatalogCurrent describes only the library revision, not provider readiness.
type ArrangementRef struct {
	RevisionDiscussionPending bool   `json:"revision_discussion_pending,omitempty"`
	Kind                      string `json:"kind"`
	ID                        string `json:"id"`
	Revision                  int64  `json:"revision"`
	State                     string `json:"state"`
	CatalogCurrent            bool   `json:"catalog_current"`
	CatalogRevision           int64  `json:"catalog_revision"`
	ReviewAvailable           bool   `json:"review_available"`
}

type ArrangementHistory struct {
	RevisionDiff         *ArrangementRevisionDiff     `json:"revision_diff,omitempty"`
	RevisionDiscussion   *RevisionDiscussion          `json:"revision_discussion,omitempty"`
	CollectionCorrection *CollectionCorrection        `json:"collection_correction,omitempty"`
	CollectionReceipt    *CollectionCommitReceipt     `json:"collection_receipt,omitempty"`
	Kind                 string                       `json:"kind"`
	Clarification        string                       `json:"clarification,omitempty"`
	Arrangement          workspacearrangement.Summary `json:"arrangement"`
	CatalogCurrent       bool                         `json:"catalog_current"`
}

func arrangementHistory(saved Arrangement, current int64) (*ArrangementHistory, error) {
	if !saved.validOutcome() {
		return nil, ErrCorrupt
	}
	summary, err := saved.Record.Summarize()
	if err != nil {
		return nil, err
	}
	var receipt *CollectionCommitReceipt
	if saved.CollectionReceipt != nil {
		copy := *saved.CollectionReceipt
		copy.Effects = slices.Clone(copy.Effects)
		receipt = &copy
	}
	out := &ArrangementHistory{Kind: saved.outcomeKind(), Clarification: saved.Clarification, CollectionReceipt: receipt, Arrangement: summary, CatalogCurrent: saved.CatalogRevision == current}
	if saved.RevisionDiff != nil {
		diff := *saved.RevisionDiff
		out.RevisionDiff = &diff
	}
	if saved.RevisionDiscussion != nil {
		discussion := *saved.RevisionDiscussion
		out.RevisionDiscussion = &discussion
	}
	if saved.CollectionCorrection != nil {
		correction := *saved.CollectionCorrection
		out.CollectionCorrection = &correction
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > workspacearrangement.MaxSummaryBytes {
		return nil, ErrLimit
	}
	return out, nil
}

// ArrangementRefs is intrinsically bounded by the 32-record envelope limit.
// It reads no source directory and does not require or impersonate a Manager.
func (s *Store) ArrangementRefs(ctx context.Context, scope Scope) ([]ArrangementRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	doc, err := s.Read(scope)
	if err != nil {
		return nil, err
	}
	out := make([]ArrangementRef, 0, len(doc.Arrangements))
	for _, saved := range doc.Arrangements {
		out = append(out, ArrangementRef{Kind: saved.outcomeKind(), ID: saved.Record.ID, RevisionDiscussionPending: saved.RevisionDiscussion != nil && saved.Record.State != "cancelled", Revision: saved.Record.Revision, State: saved.Record.State, CatalogRevision: saved.CatalogRevision, CatalogCurrent: saved.CatalogRevision == doc.Revision})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ReadArrangementHistory(ctx context.Context, scope Scope, id string) (*ArrangementHistory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !collectionLabel(id, 160) {
		return nil, ErrUnavailable
	}
	doc, err := s.Read(scope)
	if err != nil {
		return nil, err
	}
	for _, saved := range doc.Arrangements {
		if saved.Record.ID != id {
			continue
		}
		history, err := arrangementHistory(saved, doc.Revision)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return history, nil
	}
	return nil, ErrUnavailable
}
