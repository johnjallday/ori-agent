package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Bulk field edits (library-upkeep FR16-FR21): one owner action sets stage,
// status or priority for up to MaxBulkEntries songs. Each song succeeds or is
// skipped on its own, inside one fenced Home update; there is no review step,
// because the patch is the owner's explicit choice and every song is still
// checked against the fields revision the shelf showed.
const (
	MaxBulkEntries       = 100 // The shelf's selection cap.
	maxBulkFieldReceipts = 16
)

// Skip reasons a bulk edit reports per song.
const (
	BulkSkipRevisionChanged = "revision_changed"
	BulkSkipNotFound        = "not_found"
)

type BulkFieldsEntry struct {
	EntryID          string `json:"entry_id"`
	IfFieldsRevision int64  `json:"if_fields_revision"`
}

type BulkFieldsUpdated struct {
	EntryID string `json:"entry_id"`
	Fields  Fields `json:"fields"`
}

type BulkFieldsSkipped struct {
	EntryID string `json:"entry_id"`
	Reason  string `json:"reason"`
}

type BulkFieldsResult struct {
	Updated []BulkFieldsUpdated `json:"updated"`
	Skipped []BulkFieldsSkipped `json:"skipped"`
	Replay  bool                `json:"replay"`
}

// BulkFieldReceipt is the persisted outcome of one bulk edit, IDs only.
type BulkFieldReceipt struct {
	Key     string              `json:"key"`
	Updated []string            `json:"updated,omitempty"`
	Skipped []BulkFieldsSkipped `json:"skipped,omitempty"`
}

func validBulkFieldReceipts(receipts []BulkFieldReceipt) bool {
	if len(receipts) > maxBulkFieldReceipts {
		return false
	}
	keys := map[string]bool{}
	for _, receipt := range receipts {
		if receipt.Key == "" || !validText(receipt.Key, 160) || keys[receipt.Key] ||
			len(receipt.Updated)+len(receipt.Skipped) == 0 || len(receipt.Updated)+len(receipt.Skipped) > MaxBulkEntries {
			return false
		}
		keys[receipt.Key] = true
		for _, id := range receipt.Updated {
			if id == "" || !validText(id, 160) {
				return false
			}
		}
		for _, skipped := range receipt.Skipped {
			if skipped.EntryID == "" || !validText(skipped.EntryID, 160) ||
				(skipped.Reason != BulkSkipRevisionChanged && skipped.Reason != BulkSkipNotFound) {
				return false
			}
		}
	}
	return true
}

// BulkPatchAllowed reports whether a patch names only the fields a bulk edit
// may set (stage, status, priority or clearing it) and at least one of them.
func BulkPatchAllowed(p FieldsPatch) bool {
	if p.DisplayName != nil || p.Purpose != nil || p.NextAction != nil || p.SessionDate != nil ||
		p.ReleaseDate != nil || p.Milestones != nil || p.Blockers != nil || p.Deliverables != nil ||
		p.ArchiveReviewState != nil {
		return false
	}
	return (p.Stage != nil || p.Status != nil || p.Priority != nil || p.ClearPriority) &&
		(p.Priority == nil || !p.ClearPriority)
}

func bulkFieldsDigest(scope Scope, entries []BulkFieldsEntry, patch FieldsPatch, author string) (string, error) {
	encoded, err := json.Marshal(struct {
		Scope   Scope             `json:"scope"`
		Entries []BulkFieldsEntry `json:"entries"`
		Patch   FieldsPatch       `json:"patch"`
		Author  string            `json:"author"`
	}{scope, entries, patch, author})
	if err != nil {
		return "", ErrCorrupt
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// CommitBulkFields applies one stage/status/priority patch to each listed song
// whose fields are still at the revision the owner saw. A song edited since,
// or no longer in the library, is skipped with its reason; the others are
// updated with the same author, source and time a single edit records, so
// suggestions based on their old revision go stale the same way. A retry with
// the same key returns the first outcome (with each song's current fields).
func (s *Store) CommitBulkFields(scope Scope, entries []BulkFieldsEntry, patch FieldsPatch, key, author string) (BulkFieldsResult, error) {
	if s == nil || key == "" || !validText(key, 160) || len(entries) == 0 || len(entries) > MaxBulkEntries ||
		!BulkPatchAllowed(patch) {
		return BulkFieldsResult{}, ErrConflict
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.EntryID == "" || !validText(entry.EntryID, 160) || entry.IfFieldsRevision < 0 || seen[entry.EntryID] {
			return BulkFieldsResult{}, ErrConflict
		}
		seen[entry.EntryID] = true
	}
	// An invalid value (an unknown stage, a priority out of range) refuses the
	// whole request before anything is written.
	if _, err := patch.apply(Fields{}, author, s.now().UTC()); err != nil {
		return BulkFieldsResult{}, err
	}
	digest, err := bulkFieldsDigest(scope, entries, patch, author)
	if err != nil {
		return BulkFieldsResult{}, err
	}
	doc, err := s.Read(scope)
	if err != nil {
		return BulkFieldsResult{}, err
	}
	var result BulkFieldsResult
	_, replay, err := s.mutateWithPolicy(scope, doc.Revision,
		operation{key: key, action: "bulk_edit_fields", digest: digest},
		func(state *workspace.AssistantProgramState) bool { return state.PluginAvailable },
		func(current *Document) (string, error) {
			result = BulkFieldsResult{Updated: []BulkFieldsUpdated{}, Skipped: []BulkFieldsSkipped{}}
			receipt := BulkFieldReceipt{Key: key}
			now := s.now().UTC()
			for _, requested := range entries {
				index := -1
				for i := range current.Entries {
					if current.Entries[i].ID == requested.EntryID {
						index = i
						break
					}
				}
				reason := ""
				switch {
				case index < 0:
					reason = BulkSkipNotFound
				case current.Entries[index].Fields.Revision != requested.IfFieldsRevision:
					reason = BulkSkipRevisionChanged
				}
				if reason != "" {
					skipped := BulkFieldsSkipped{EntryID: requested.EntryID, Reason: reason}
					result.Skipped = append(result.Skipped, skipped)
					receipt.Skipped = append(receipt.Skipped, skipped)
					continue
				}
				entry := &current.Entries[index]
				fields, applyErr := patch.apply(entry.Fields, author, now)
				if applyErr != nil {
					return "", applyErr
				}
				entry.Fields, entry.Revision = fields, entry.Revision+1
				result.Updated = append(result.Updated, BulkFieldsUpdated{EntryID: entry.ID, Fields: fields})
				receipt.Updated = append(receipt.Updated, entry.ID)
			}
			current.BulkFieldReceipts = append(current.BulkFieldReceipts, receipt)
			if len(current.BulkFieldReceipts) > maxBulkFieldReceipts {
				current.BulkFieldReceipts = current.BulkFieldReceipts[len(current.BulkFieldReceipts)-maxBulkFieldReceipts:]
			}
			return "bulk-fields:" + digest[:40], nil
		})
	if err != nil {
		return BulkFieldsResult{}, err
	}
	if !replay {
		return result, nil
	}
	return s.replayBulkFields(scope, key)
}

// replayBulkFields rebuilds a retried bulk edit's first outcome from its
// receipt. A receipt that has aged out cannot be rebuilt honestly: conflict.
func (s *Store) replayBulkFields(scope Scope, key string) (BulkFieldsResult, error) {
	doc, err := s.Read(scope)
	if err != nil {
		return BulkFieldsResult{}, err
	}
	for _, receipt := range doc.BulkFieldReceipts {
		if receipt.Key != key {
			continue
		}
		result := BulkFieldsResult{Updated: []BulkFieldsUpdated{}, Skipped: append([]BulkFieldsSkipped{}, receipt.Skipped...),
			Replay: true}
		for _, id := range receipt.Updated {
			for _, entry := range doc.Entries {
				if entry.ID == id {
					result.Updated = append(result.Updated, BulkFieldsUpdated{EntryID: id, Fields: entry.Fields})
					break
				}
			}
		}
		return result, nil
	}
	return BulkFieldsResult{}, ErrConflict
}
