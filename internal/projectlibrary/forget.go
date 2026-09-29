package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ForgetReview is a destructive Home-record impact disclosure, not a source
// file operation. A project still linked to a child or observed under an
// active grant cannot be forgotten through this endpoint.
type ForgetReview struct {
	Token        string    `json:"token"`
	EntryID      string    `json:"entry_id"`
	ProjectName  string    `json:"project_name"`
	SourceCount  int       `json:"source_count"`
	SessionCount int       `json:"session_count"`
	QueuedAt     int       `json:"queued_at,omitempty"` // 1-based pending position, never queue authority
	ExpiresAt    time.Time `json:"expires_at"`
}

func forgetImpact(doc Document, state *workspace.AssistantProgramState, entryID string) (ForgetReview, string, error) {
	entry := sessionEntry(doc, entryID)
	if entry == nil || entry.Link != nil || len(entry.Observations) == 0 || state == nil {
		return ForgetReview{}, "", ErrConflict
	}
	for _, source := range entry.Observations {
		inactive := false
		for _, root := range doc.Roots {
			if root.ID == source.RootID {
				inactive = root.RevokedAt != nil
				for _, id := range state.ProjectLibraryInactiveRoots {
					inactive = inactive || id == root.ID
				}
			}
		}
		if !inactive {
			return ForgetReview{}, "", ErrConflict
		}
	}
	impact := ForgetReview{EntryID: entry.ID, ProjectName: entry.Fields.DisplayName,
		SourceCount: len(entry.Observations)}
	if impact.ProjectName == "" {
		for _, source := range entry.Observations {
			if source.RelativeFolder != "" {
				impact.ProjectName = filepath.Base(source.RelativeFolder)
				break
			}
			for _, root := range doc.Roots {
				if root.ID == source.RootID {
					impact.ProjectName = filepath.Base(root.Path)
				}
			}
		}
	}
	if impact.ProjectName == "" || !validText(impact.ProjectName, 160) {
		impact.ProjectName = "Saved project"
	}
	for _, session := range doc.Sessions {
		if session.EntryID == entry.ID {
			impact.SessionCount++
		}
	}
	if doc.Queue != nil && doc.Queue.Status == "active" {
		for i := doc.Queue.Index; i < len(doc.Queue.IDs); i++ {
			if doc.Queue.IDs[i] == entry.ID {
				impact.QueuedAt = i + 1
				break
			}
		}
	}
	// Hash the entire record and related sessions: both user notes and history
	// must be identical between review and the final Home CAS.
	rows := make([]StudioSession, 0, impact.SessionCount)
	for _, session := range doc.Sessions {
		if session.EntryID == entry.ID {
			rows = append(rows, session)
		}
	}
	encoded, err := json.Marshal(struct {
		Entry    Entry           `json:"entry"`
		Sessions []StudioSession `json:"sessions"`
	}{*entry, rows})
	if err != nil {
		return ForgetReview{}, "", ErrCorrupt
	}
	sum := sha256.Sum256(encoded)
	return impact, hex.EncodeToString(sum[:]), nil
}

func (s *Store) ReviewForget(scope Scope, entryID string, expected int64) (ForgetReview, error) {
	if s == nil || entryID == "" || !validText(entryID, 160) || expected < 1 {
		return ForgetReview{}, ErrConflict
	}
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	doc, state, err := s.readSnapshot(scope)
	if err != nil || doc.Revision != expected {
		return ForgetReview{}, ErrConflict
	}
	impact, digest, err := forgetImpact(doc, state, entryID)
	if err != nil {
		return ForgetReview{}, err
	}
	impact.Token, impact.ExpiresAt = newID(), s.now().UTC().Add(10*time.Minute)
	review := ReviewReceipt{Token: impact.Token, Action: "forget_entry", TargetID: entryID,
		Digest: digest, Revision: doc.Revision + 1, ExpiresAt: impact.ExpiresAt}
	_, _, err = s.mutate(scope, doc.Revision, operation{key: impact.Token, action: "review_forget_entry", digest: digest}, func(current *Document) (string, error) {
		if len(current.Reviews) >= maxReviews {
			return "", ErrLimit
		}
		current.Reviews = append(current.Reviews, review)
		return impact.Token, nil
	})
	if err != nil {
		return ForgetReview{}, err
	}
	return impact, nil
}

func (s *Store) CommitForget(scope Scope, entryID, token, key string) (bool, error) {
	if s == nil || entryID == "" || token == "" || key == "" || !validText(entryID, 160) ||
		!validText(token, 160) || !validText(key, 160) {
		return false, ErrConflict
	}
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return false, err
	}
	review, ok := findReview(doc, token, "forget_entry")
	if !ok || review.TargetID != entryID {
		return false, ErrConflict
	}
	for _, prior := range doc.Operations {
		if prior.Key == key && prior.Action == "forget_entry" && prior.Digest == review.Digest && prior.ConsequenceID == entryID {
			if sessionEntry(doc, entryID) == nil {
				return true, nil
			}
			return false, ErrConflict
		}
	}
	_, digest, err := forgetImpact(doc, state, entryID)
	if err != nil || digest != review.Digest || review.ConsumedAt != nil || review.Revision != doc.Revision ||
		!review.ExpiresAt.After(s.now().UTC()) {
		return false, ErrConflict
	}
	_, replay, err := s.mutateWithHomePolicy(scope, doc.Revision, operation{key: key, action: "forget_entry", digest: digest},
		func(current *workspace.AssistantProgramState, _ *workspace.Workspace) bool {
			_, verified, check := forgetImpact(doc, current, entryID)
			return check == nil && verified == digest
		}, func(current *Document) (string, error) {
			_, verified, check := forgetImpact(*current, state, entryID)
			if check != nil || verified != digest {
				return "", ErrConflict
			}
			for i := range current.Reviews {
				receipt := &current.Reviews[i]
				if receipt.Token == token && receipt.Action == "forget_entry" && receipt.Digest == digest &&
					receipt.Revision == current.Revision && receipt.ConsumedAt == nil && receipt.ExpiresAt.After(s.now().UTC()) {
					at := s.now().UTC()
					receipt.ConsumedAt = &at
					for i := range current.Entries {
						if current.Entries[i].ID == entryID {
							current.Entries = append(current.Entries[:i], current.Entries[i+1:]...)
							break
						}
					}
					if current.Queue != nil && current.Queue.Status != "active" {
						for _, id := range current.Queue.IDs {
							if id == entryID {
								archiveTerminalQueue(current)
								break
							}
						}
					} else {
						redactHandledQueueEntry(current, entryID, s.now().UTC())
					}
					kept := current.Sessions[:0]
					for _, session := range current.Sessions {
						if session.EntryID != entryID {
							kept = append(kept, session)
						}
					}
					current.Sessions = kept
					remaining := current.Proposals[:0]
					for _, proposal := range current.Proposals {
						if proposal.EntryID != entryID {
							remaining = append(remaining, proposal)
						}
					}
					current.Proposals = remaining
					return entryID, nil
				}
			}
			return "", ErrConflict
		})
	return replay, err
}
