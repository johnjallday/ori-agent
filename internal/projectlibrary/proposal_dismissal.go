package projectlibrary

import (
	"errors"
	"time"
)

const (
	maxDismissals = 256
	// dismissalMemory is how long a dismissed (entry, kind) is not proposed
	// again by a scan-review turn (PRD FR 9). Fixed for v1.
	dismissalMemory = 7 * 24 * time.Hour
)

var (
	// ErrProposalNotFound is an unknown suggestion ID for this Home.
	ErrProposalNotFound = errors.New("suggestion not found")
	// ErrRecentlyDismissed refuses a scan-review suggestion for an entry and
	// kind the owner dismissed within the last seven days.
	ErrRecentlyDismissed = errors.New("the owner dismissed a matching suggestion recently")
)

// ProposalDismissal remembers an owner dismissal after the suggestion itself
// has expired and been pruned. It holds IDs and a kind only.
type ProposalDismissal struct {
	ProposalID string    `json:"proposal_id"`
	EntryID    string    `json:"entry_id,omitempty"` // empty for discovery-folder navigation
	Kind       string    `json:"kind,omitempty"`     // empty: next action
	At         time.Time `json:"at"`
}

func knownProposalKind(kind string) bool {
	switch kind {
	case "", "root_review", "project_review", "session_goal", "session_recap":
		return true
	}
	return false
}

func dismissalsValid(dismissals []ProposalDismissal) bool {
	if len(dismissals) > maxDismissals {
		return false
	}
	for _, dismissal := range dismissals {
		if dismissal.ProposalID == "" || !validText(dismissal.ProposalID, 160) || !validText(dismissal.EntryID, 160) ||
			!knownProposalKind(dismissal.Kind) || dismissal.At.IsZero() {
			return false
		}
	}
	return true
}

// proposalWaiting is a suggestion the owner could still act on, judged from
// the document alone: not dismissed, not expired, not stale.
func proposalWaiting(doc Document, proposal ManagerProposal, at time.Time) bool {
	return proposal.DismissedAt == nil && proposal.ExpiresAt.After(at) && !proposalStaleInDocument(doc, proposal)
}

func recentlyDismissed(doc Document, entryID, kind string, at time.Time) bool {
	for _, dismissal := range doc.Dismissals {
		if dismissal.EntryID == entryID && dismissal.Kind == kind && at.Sub(dismissal.At) < dismissalMemory {
			return true
		}
	}
	return false
}

// DismissManagerProposal is the owner's separate, reductive answer to one
// waiting suggestion. It marks the suggestion dismissed and remembers the
// (entry, kind) for seven days; it changes no notes, sessions, grants or
// workspaces, and it is allowed after provider loss. An exact retry with the
// same key replays; dismissing a suggestion that is no longer waiting — for
// example one already confirmed — is a conflict.
func (s *Store) DismissManagerProposal(scope Scope, proposalID, key string) (ManagerProposal, bool, error) {
	if proposalID == "" || !validText(proposalID, 160) || key == "" || !validText(key, 160) {
		return ManagerProposal{}, false, ErrConflict
	}
	digest := proposalRunOpDigest(scope, proposalID, "dismiss")
	for attempt := 0; ; attempt++ {
		doc, err := s.Read(scope)
		if err != nil {
			return ManagerProposal{}, false, err
		}
		target := -1
		for i := range doc.Proposals {
			if doc.Proposals[i].ID == proposalID {
				target = i
				break
			}
		}
		recorded := false
		for _, op := range doc.Operations {
			recorded = recorded || op.Key == key
		}
		if target < 0 && !recorded {
			return ManagerProposal{}, false, ErrProposalNotFound
		}
		if !recorded {
			// Authority loss leaves a suggestion "unavailable", which the owner
			// may still dismiss; anything no longer waiting may not be.
			status := s.managerProposalStatus(scope, doc, doc.Proposals[target])
			if status != "ready" && status != "unavailable" {
				return ManagerProposal{}, false, ErrConflict
			}
		}
		var dismissed ManagerProposal
		_, replay, err := s.mutateWithHomePolicy(scope, doc.Revision,
			operation{key: key, action: "dismiss_proposal", digest: digest}, nil,
			func(current *Document) (string, error) {
				at := s.now().UTC()
				for i := range current.Proposals {
					proposal := &current.Proposals[i]
					if proposal.ID != proposalID {
						continue
					}
					if !proposalWaiting(*current, *proposal, at) {
						return "", ErrConflict
					}
					proposal.DismissedAt = &at
					kept := current.Dismissals[:0]
					for _, old := range current.Dismissals {
						if at.Sub(old.At) < dismissalMemory {
							kept = append(kept, old)
						}
					}
					if len(kept) >= maxDismissals {
						kept = kept[1:]
					}
					current.Dismissals = append(kept, ProposalDismissal{ProposalID: proposal.ID,
						EntryID: proposal.EntryID, Kind: proposal.Kind, At: at})
					dismissed = *proposal
					return proposal.ID, nil
				}
				return "", ErrConflict
			})
		if errors.Is(err, ErrConflict) && attempt < 2 && !recorded {
			continue // Another Home write landed first; recheck against it.
		}
		if err != nil {
			return ManagerProposal{}, false, err
		}
		if replay {
			fresh, readErr := s.Read(scope)
			if readErr != nil {
				return ManagerProposal{}, false, readErr
			}
			for _, proposal := range fresh.Proposals {
				if proposal.ID == proposalID && proposal.DismissedAt != nil {
					return proposal, true, nil
				}
			}
			for _, dismissal := range fresh.Dismissals {
				if dismissal.ProposalID == proposalID {
					// The suggestion expired and was pruned; the dismissal stands.
					at := dismissal.At
					return ManagerProposal{ID: proposalID, EntryID: dismissal.EntryID, Kind: dismissal.Kind,
						DismissedAt: &at}, true, nil
				}
			}
			return ManagerProposal{}, false, ErrConflict
		}
		return dismissed, false, nil
	}
}
