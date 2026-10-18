package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// collectionEffect is a host-compiled metadata consequence, not approval. IDs
// are derived from canonical record identity, never from collection names.
// The review/commit adapter must independently authorize the owner and current
// Manager/provider, pin this result, and fence the eventual write with CAS.
type collectionEffect struct {
	Kind           string `json:"kind"`
	CollectionID   string `json:"collection_id"`
	BeforeRevision int64  `json:"before_revision"`
	AfterRevision  int64  `json:"after_revision"`
	QueueID        string `json:"queue_id,omitempty"`
	SelectedCount  int    `json:"selected_count,omitempty"`
}

type collectionChanges struct {
	Collections []LogicalCollection
	Effects     []collectionEffect
	Queue       *ActivationQueue `json:"queue,omitempty"`
}

// compileCollectionChanges has no writer, filesystem access or model calls. It
// prepares logical collections and at most one activation-review queue atomically.
// Queue preparation never activates projects. Unsupported creator intent fails whole.
// The input must come from the canonical Home record, not browser/model JSON.
func compileCollectionChanges(doc Document, arrangementID string, at time.Time) (collectionChanges, error) {
	fail := collectionChanges{}
	var saved Arrangement
	found := false
	for _, candidate := range doc.Arrangements {
		if candidate.Record.ID != arrangementID {
			continue
		}
		if found {
			return fail, ErrCorrupt
		}
		saved, found = candidate, true
	}
	if !found {
		return fail, ErrConflict
	}
	r := saved.Record
	if at.IsZero() || at.Before(r.UpdatedAt) || doc.SchemaVersion != ArrangementSchemaVersion || saved.CatalogRevision != doc.Revision ||
		!saved.validOutcome() || saved.Kind == "clarification" || saved.RevisionDiscussion != nil || r.Validate() != nil ||
		r.SourceKind != workspacearrangement.HomeLibrary || r.SourceID != doc.HomeID ||
		(r.State != "draft" && r.State != "needs_more_evidence" && r.State != "awaiting_user_review") || saved.ManagerName == "" {
		return fail, ErrConflict
	}
	author := saved.ManagerName
	if r.Origin != "model" || saved.CollectionCorrection != nil {
		author = doc.OwnerUserID
	}
	entries := make(map[string]bool, len(doc.Entries))
	revisions := make(map[string]string, len(doc.Entries))
	for _, entry := range doc.Entries {
		entries[entry.ID] = true
		revisions[entry.ID] = fmt.Sprintf("%d-%d", entry.Revision, entry.Fields.Revision)
	}
	for id, revision := range r.ItemRevisions {
		if revisions[id] != revision {
			return fail, ErrConflict
		}
	}
	current := make(map[string]LogicalCollection, len(doc.Collections))
	result := collectionChanges{Collections: make([]LogicalCollection, 0, len(doc.Collections))}
	for _, collection := range doc.Collections {
		if !collection.valid(entries) {
			return fail, ErrCorrupt
		}
		if _, duplicate := current[collection.ID]; duplicate {
			return fail, ErrCorrupt
		}
		collection.EntryIDs = slices.Clone(collection.EntryIDs)
		current[collection.ID] = collection
		result.Collections = append(result.Collections, collection)
	}
	for id, revision := range r.CollectionRevisions {
		if current[id].Revision != revision {
			return fail, ErrConflict
		}
	}
	protected := map[string]bool{}
	for _, id := range r.Draft.Unchanged.IDs {
		protected[id] = true
	}
	for _, recommendation := range r.Draft.NeedsReview {
		for _, id := range recommendation.Items.IDs {
			protected[id] = true
		}
	}
	for index, op := range r.Draft.Operations {
		effect := collectionEffect{Kind: op.Kind, CollectionID: op.CollectionID}
		switch op.Kind {
		case workspacearrangement.SelectActivation:
			if result.Queue != nil {
				return fail, ErrUnavailable
			}
			sum := sha256.Sum256([]byte(fmt.Sprintf("activation-selection\x00%s\x00%d", r.ID, r.Revision)))
			ids := slices.Clone(op.Members.IDs)
			slices.Sort(ids) // Same stable order as the exact review pages.
			queue, err := activationQueueForSelection(doc, ids, "activation-"+hex.EncodeToString(sum[:]), at.UTC())
			if err != nil {
				return fail, err
			}
			result.Queue = &queue
			effect.QueueID, effect.SelectedCount = queue.ID, len(queue.IDs)
		case workspacearrangement.CreateCollection:
			sum := sha256.Sum256([]byte(fmt.Sprintf("collection\x00%s\x00%d\x00%d", r.ID, r.Revision, index)))
			effect.CollectionID = "collection-" + hex.EncodeToString(sum[:])
			if _, exists := current[effect.CollectionID]; exists {
				return fail, ErrConflict
			}
			collection := LogicalCollection{ID: effect.CollectionID, Name: op.Name, Revision: 1,
				EntryIDs: slices.Clone(op.Members.IDs), Author: author, Origin: r.Origin,
				ArrangementID: r.ID, CreatedAt: at.UTC(), UpdatedAt: at.UTC()}
			if !collection.valid(entries) {
				return fail, ErrConflict
			}
			current[collection.ID] = collection
			result.Collections = append(result.Collections, collection)
			effect.AfterRevision = 1
		case workspacearrangement.RenameCollection, workspacearrangement.SetMembers, workspacearrangement.RemoveCollection:
			collection, exists := current[op.CollectionID]
			if !exists || collection.Revision != op.CollectionRevision {
				return fail, ErrConflict
			}
			// An existing collection selector must not bypass explicit exclusions
			// by dropping membership indirectly through replacement or removal.
			if op.Kind != workspacearrangement.RenameCollection {
				for _, id := range collection.EntryIDs {
					if protected[id] && !slices.Contains(op.Members.IDs, id) {
						return fail, ErrConflict
					}
				}
			}
			effect.BeforeRevision = collection.Revision
			position := slices.IndexFunc(result.Collections, func(c LogicalCollection) bool { return c.ID == op.CollectionID })
			if position < 0 {
				return fail, ErrConflict
			}
			if op.Kind == workspacearrangement.RemoveCollection {
				delete(current, collection.ID)
				result.Collections = slices.Delete(result.Collections, position, position+1)
			} else {
				if op.Kind == workspacearrangement.RenameCollection {
					collection.Name = op.Name
				} else {
					collection.EntryIDs = slices.Clone(op.Members.IDs)
				}
				collection.Revision++
				collection.UpdatedAt = at.UTC()
				collection.Author, collection.Origin, collection.ArrangementID = author, r.Origin, r.ID
				if !collection.valid(entries) {
					return fail, ErrConflict
				}
				current[collection.ID] = collection
				result.Collections[position] = collection
				effect.AfterRevision = collection.Revision
			}
		default:
			return fail, ErrUnavailable
		}
		result.Effects = append(result.Effects, effect)
	}
	memberships := 0
	for _, collection := range result.Collections {
		memberships += len(collection.EntryIDs)
	}
	if len(result.Collections) > 100 || memberships > workspacearrangement.MaxMemberships {
		return fail, ErrLimit
	}
	return result, nil
}
