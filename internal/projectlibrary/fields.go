package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// FieldsPatch is a user-authored, explicitly sparse update. Nil means leave
// the existing value alone; a pointer to an empty slice clears that list.
// Priority zero is a real choice; ClearPriority is the separate unset action.
type FieldsPatch struct {
	DisplayName        *string      `json:"display_name,omitempty"`
	Purpose            *string      `json:"purpose,omitempty"`
	Stage              *string      `json:"stage,omitempty"`
	Status             *string      `json:"status,omitempty"`
	NextAction         *string      `json:"next_action,omitempty"`
	Priority           *int         `json:"priority,omitempty"`
	ClearPriority      bool         `json:"clear_priority,omitempty"`
	SessionDate        *string      `json:"session_date,omitempty"`
	ReleaseDate        *string      `json:"release_date,omitempty"`
	Milestones         *[]Milestone `json:"milestones,omitempty"`
	Blockers           *[]string    `json:"blockers,omitempty"`
	Deliverables       *[]string    `json:"deliverables,omitempty"`
	ArchiveReviewState *string      `json:"archive_review_state,omitempty"`
}

func (p FieldsPatch) apply(previous Fields, author string, at time.Time) (Fields, error) {
	if p.Priority != nil && p.ClearPriority ||
		p.DisplayName == nil && p.Purpose == nil && p.Stage == nil && p.Status == nil &&
			p.NextAction == nil && p.Priority == nil && !p.ClearPriority && p.SessionDate == nil &&
			p.ReleaseDate == nil && p.Milestones == nil && p.Blockers == nil && p.Deliverables == nil &&
			p.ArchiveReviewState == nil || !validText(author, 160) || author == "" {
		return Fields{}, ErrConflict
	}
	result := previous
	if p.DisplayName != nil {
		result.DisplayName = *p.DisplayName
	}
	if p.Purpose != nil {
		result.Purpose = *p.Purpose
	}
	if p.Stage != nil {
		result.Stage = *p.Stage
	}
	if p.Status != nil {
		result.Status = *p.Status
	}
	if p.NextAction != nil {
		result.NextAction = *p.NextAction
	}
	if p.Priority != nil {
		value := *p.Priority
		result.Priority = &value
	}
	if p.ClearPriority {
		result.Priority = nil
	}
	if p.SessionDate != nil {
		result.SessionDate = *p.SessionDate
	}
	if p.ReleaseDate != nil {
		result.ReleaseDate = *p.ReleaseDate
	}
	if p.Milestones != nil {
		result.Milestones = append([]Milestone(nil), (*p.Milestones)...)
	}
	if p.Blockers != nil {
		result.Blockers = append([]string(nil), (*p.Blockers)...)
	}
	if p.Deliverables != nil {
		result.Deliverables = append([]string(nil), (*p.Deliverables)...)
	}
	if p.ArchiveReviewState != nil {
		result.ArchiveReviewState = *p.ArchiveReviewState
	}
	result.Revision++
	result.Source, result.Author, result.UpdatedAt = "reviewed_user", author, at
	if !result.valid() {
		return Fields{}, ErrConflict
	}
	return result, nil
}

func fieldDigest(scope Scope, entryID string, revision int64, patch FieldsPatch, author string) (string, error) {
	encoded, err := json.Marshal(struct {
		Scope    Scope       `json:"scope"`
		EntryID  string      `json:"entry_id"`
		Revision int64       `json:"revision"`
		Patch    FieldsPatch `json:"patch"`
		Author   string      `json:"author"`
	}{scope, entryID, revision, patch, author})
	if err != nil {
		return "", ErrCorrupt
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// FieldReview has no edit effect; it can be displayed for explicit approval.
type FieldReview struct {
	Token     string    `json:"token"`
	EntryID   string    `json:"entry_id"`
	Before    Fields    `json:"before"`
	After     Fields    `json:"after"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Store) ReviewFields(scope Scope, entryID string, expectedFieldsRevision int64,
	patch FieldsPatch, author string) (FieldReview, error) {
	if s == nil || entryID == "" || expectedFieldsRevision < 0 {
		return FieldReview{}, ErrConflict
	}
	doc, err := s.Read(scope)
	if err != nil {
		return FieldReview{}, err
	}
	var before Fields
	found := false
	for _, entry := range doc.Entries {
		if entry.ID == entryID {
			before, found = entry.Fields, true
			break
		}
	}
	if !found || before.Revision != expectedFieldsRevision {
		return FieldReview{}, ErrConflict
	}
	now := s.now().UTC()
	after, err := patch.apply(before, author, now)
	if err != nil {
		return FieldReview{}, err
	}
	digest, err := fieldDigest(scope, entryID, expectedFieldsRevision, patch, author)
	if err != nil {
		return FieldReview{}, err
	}
	review := ReviewReceipt{Token: newID(), Action: "edit_fields", Digest: digest,
		Revision: doc.Revision + 1, TargetID: entryID, FieldsRevision: expectedFieldsRevision,
		ExpiresAt: now.Add(10 * time.Minute)}
	_, _, err = s.mutateWithPolicy(scope, doc.Revision,
		operation{key: review.Token, action: "review_fields", digest: digest},
		func(state *workspace.AssistantProgramState) bool { return state.PluginAvailable },
		func(current *Document) (string, error) {
			if len(current.Reviews) >= maxReviews {
				return "", ErrLimit
			}
			current.Reviews = append(current.Reviews, review)
			return review.Token, nil
		})
	if err != nil {
		return FieldReview{}, err
	}
	return FieldReview{Token: review.Token, EntryID: entryID, Before: before, After: after,
		ExpiresAt: review.ExpiresAt}, nil
}

// CommitFields changes only reviewed user fields. It never reads a root or
// reassigns machine observations, and retains the source/author and receipt.
func (s *Store) CommitFields(scope Scope, entryID, token, key string,
	expectedFieldsRevision int64, patch FieldsPatch, author string) (Entry, bool, error) {
	return s.commitFields(scope, entryID, token, key, expectedFieldsRevision, patch, author, nil)
}

func (s *Store) commitFields(scope Scope, entryID, token, key string,
	expectedFieldsRevision int64, patch FieldsPatch, author string,
	livePolicy func(*workspace.AssistantProgramState) bool) (Entry, bool, error) {
	if s == nil || entryID == "" || token == "" || key == "" || expectedFieldsRevision < 0 ||
		!validText(key, 160) || !validText(token, 160) {
		return Entry{}, false, ErrConflict
	}
	digest, err := fieldDigest(scope, entryID, expectedFieldsRevision, patch, author)
	if err != nil {
		return Entry{}, false, err
	}
	doc, err := s.Read(scope)
	if err != nil {
		return Entry{}, false, err
	}
	var review *ReviewReceipt
	for i := range doc.Reviews {
		if doc.Reviews[i].Token == token && doc.Reviews[i].Action == "edit_fields" &&
			doc.Reviews[i].Digest == digest && doc.Reviews[i].TargetID == entryID &&
			doc.Reviews[i].FieldsRevision == expectedFieldsRevision {
			review = &doc.Reviews[i]
			break
		}
	}
	if review == nil {
		return Entry{}, false, ErrConflict
	}
	for _, prior := range doc.Operations {
		if prior.Key == key {
			if prior.Action != "edit_fields" || prior.Digest != digest || prior.ConsequenceID != entryID {
				return Entry{}, false, ErrConflict
			}
			for _, entry := range doc.Entries {
				if entry.ID == entryID {
					return entry, true, nil
				}
			}
			return Entry{}, false, ErrCorrupt
		}
	}
	if review.ConsumedAt != nil || !review.ExpiresAt.After(s.now().UTC()) || doc.Revision != review.Revision {
		return Entry{}, false, ErrConflict
	}
	var changed Entry
	var linkStale bool
	_, replay, err := s.mutateWithPolicy(scope, doc.Revision,
		operation{key: key, action: "edit_fields", digest: digest},
		func(state *workspace.AssistantProgramState) bool {
			if !state.PluginAvailable {
				return false
			}
			if livePolicy != nil && !livePolicy(state) {
				linkStale = true
				return false
			}
			return true
		},
		func(current *Document) (string, error) {
			for i := range current.Reviews {
				receipt := &current.Reviews[i]
				if receipt.Token != token || receipt.Action != "edit_fields" || receipt.Digest != digest ||
					receipt.TargetID != entryID || receipt.FieldsRevision != expectedFieldsRevision ||
					receipt.ConsumedAt != nil || !receipt.ExpiresAt.After(s.now().UTC()) ||
					receipt.Revision != current.Revision {
					continue
				}
				for j := range current.Entries {
					entry := &current.Entries[j]
					if entry.ID != entryID || entry.Fields.Revision != expectedFieldsRevision {
						continue
					}
					now := s.now().UTC()
					fields, applyErr := patch.apply(entry.Fields, author, now)
					if applyErr != nil {
						return "", applyErr
					}
					entry.Fields, entry.Revision = fields, entry.Revision+1
					receipt.ConsumedAt = &now
					changed = *entry
					return entryID, nil
				}
			}
			return "", ErrConflict
		})
	if err != nil {
		if linkStale && errors.Is(err, ErrUnavailable) {
			return Entry{}, false, ErrConflict
		}
		return Entry{}, false, err
	}
	return changed, replay, nil
}
