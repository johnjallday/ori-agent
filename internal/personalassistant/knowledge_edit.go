package personalassistant

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// EditApproved changes only a reviewed, uniquely matched canonical HQ fact.
// Preparing immediately removes eligibility; the new revision becomes active
// only after a verified exact-line mutation and a durable finalization receipt.
func (s *KnowledgeLearningService) EditApproved(ctx context.Context, userID, itemID string, expectedVersion int64, requestID, text string) (KnowledgeItem, error) {
	if s == nil || s.store == nil || s.memory == nil || s.now == nil {
		return KnowledgeItem{}, ErrRepairNeeded
	}
	if itemID == "" || expectedVersion < 1 || !validInterviewID(requestID) {
		return KnowledgeItem{}, ErrConflict
	}
	text, err := workspace.ValidateMemoryText(text)
	if err != nil {
		return KnowledgeItem{}, err
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return KnowledgeItem{}, err
	}
	for range knowledgeRetryLimit {
		doc, err := s.store.Read(ctx, userID)
		if err != nil {
			return KnowledgeItem{}, err
		}
		item, ok := knowledgeItemByID(doc, itemID)
		if !ok {
			return KnowledgeItem{}, ErrNotFound
		}
		if receipt, found := knowledgeReceiptByID(doc, requestID); found {
			if receipt.ItemID == itemID && receipt.Result == "edited" && item.State == KnowledgeApproved && item.Target != nil &&
				((receipt.RevisionID != "" && receipt.RevisionID == item.CurrentRevisionID) ||
					(receipt.RevisionID == "" && item.Version == expectedVersion+1)) {
				status, err := s.memory.InspectExact(binding.HQWorkspaceID, workspace.MemoryExactTarget{
					Provenance: item.Target.Marker, LineHash: item.Target.CanonicalHash,
				})
				if err == nil && status == workspace.MemoryExactUnchanged {
					return item, nil
				}
			}
			return KnowledgeItem{}, ErrConflict
		}
		if item.Version != expectedVersion || item.Target == nil || item.Target.Kind != "memory" {
			return KnowledgeItem{}, ErrConflict
		}
		if item.Prepared == nil {
			if item.State != KnowledgeApproved || len(item.Revisions) >= knowledgeMaxRevisions {
				return KnowledgeItem{}, ErrConflict
			}
			at := s.now().UTC()
			newRevision := KnowledgeRevision{ID: uuid.NewString(), Text: text, CreatedAt: at, EditedAt: &at}
			entry := workspace.MemoryEntry{Type: workspace.MemoryTypeFact, Date: at.Format("2006-01-02"),
				Provenance: "ori-hq:" + item.ID + ":" + newRevision.ID, Text: text}
			prepared := KnowledgeOperation{RequestID: requestID, Kind: "edit", OldHash: item.Target.CanonicalHash,
				NewHash: hashKnowledgeLine(entry.Render()), RevisionID: newRevision.ID, PreparedAt: at}
			_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
				for i := range latest.Items {
					current := &latest.Items[i]
					if current.ID != itemID {
						continue
					}
					if current.Version != expectedVersion || current.State != KnowledgeApproved || current.Prepared != nil ||
						current.Target == nil || current.Target.CanonicalHash != prepared.OldHash {
						return ErrConflict
					}
					current.State = KnowledgeNeedsReview // exclusion before canonical mutation
					current.Revisions = append(current.Revisions, newRevision)
					current.Prepared = &prepared
					current.UpdatedAt = at
					return nil
				}
				return ErrNotFound
			})
			if errors.Is(err, ErrConflict) {
				continue
			}
			if err != nil {
				return KnowledgeItem{}, err
			}
			continue
		}
		if item.State != KnowledgeNeedsReview || item.Prepared.Kind != "edit" || item.Prepared.RequestID != requestID ||
			item.Prepared.OldHash != item.Target.CanonicalHash || item.Prepared.PreparedAt.IsZero() {
			return KnowledgeItem{}, ErrConflict
		}
		newEntry, err := entryFromRevision(item, item.Prepared.RevisionID, item.Prepared.PreparedAt)
		if err != nil || newEntry.Text != text || hashKnowledgeLine(newEntry.Render()) != item.Prepared.NewHash {
			return KnowledgeItem{}, ErrConflict
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return KnowledgeItem{}, err
		}
		oldTarget := workspace.MemoryExactTarget{Provenance: item.Target.Marker, LineHash: item.Target.CanonicalHash}
		newTarget := workspace.MemoryExactTarget{Provenance: newEntry.Provenance, LineHash: item.Prepared.NewHash}
		oldStatus, err := s.memory.InspectExact(binding.HQWorkspaceID, oldTarget)
		if err != nil {
			return KnowledgeItem{}, err
		}
		newStatus, err := s.memory.InspectExact(binding.HQWorkspaceID, newTarget)
		if err != nil {
			return KnowledgeItem{}, err
		}
		switch {
		case oldStatus == workspace.MemoryExactUnchanged && newStatus == workspace.MemoryExactMissing:
			snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
			if err != nil {
				return KnowledgeItem{}, err
			}
			oldTarget.FileHash = snapshot.FileHash
			if err := s.memory.EditExact(binding.HQWorkspaceID, oldTarget, &newEntry); err != nil {
				return KnowledgeItem{}, err // leave prepared record inert for retry
			}
		case oldStatus == workspace.MemoryExactMissing && newStatus == workspace.MemoryExactUnchanged:
			// A prior attempt committed the exact revision but did not finalize.
		default:
			return KnowledgeItem{}, s.markEditNeedsReview(ctx, userID, doc, itemID, requestID)
		}
		oldStatus, err = s.memory.InspectExact(binding.HQWorkspaceID, oldTarget)
		if err != nil || oldStatus != workspace.MemoryExactMissing {
			return KnowledgeItem{}, memoryVerificationError(err)
		}
		newStatus, err = s.memory.InspectExact(binding.HQWorkspaceID, newTarget)
		if err != nil || newStatus != workspace.MemoryExactUnchanged {
			return KnowledgeItem{}, memoryVerificationError(err)
		}
		snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
		if err != nil {
			return KnowledgeItem{}, err
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return KnowledgeItem{}, err
		}
		var approved KnowledgeItem
		_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			for i := range latest.Items {
				current := &latest.Items[i]
				if current.ID != itemID {
					continue
				}
				if current.Version != expectedVersion || current.State != KnowledgeNeedsReview || current.Prepared == nil ||
					current.Prepared.RequestID != requestID || current.Prepared.Kind != "edit" {
					return ErrConflict
				}
				at := s.now().UTC()
				current.CurrentRevisionID = current.Prepared.RevisionID
				current.Target = &KnowledgeTarget{Kind: "memory", Marker: newTarget.Provenance,
					CanonicalHash: newTarget.LineHash, Version: snapshot.FileHash}
				current.Prepared = nil
				current.State = KnowledgeApproved
				current.Version++
				current.UpdatedAt = at
				for revision := range current.Revisions {
					if current.Revisions[revision].ID == current.CurrentRevisionID {
						current.Revisions[revision].ApprovedAt = &at
					}
				}
				if len(latest.Receipts) >= knowledgeMaxReceipts {
					latest.Receipts = latest.Receipts[1:]
				}
				latest.Receipts = append(latest.Receipts, KnowledgeReceipt{RequestID: requestID, ItemID: itemID, Result: "edited", RevisionID: current.CurrentRevisionID, CreatedAt: at})
				approved = *current
				return nil
			}
			return ErrNotFound
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		return approved, err
	}
	return KnowledgeItem{}, ErrConflict
}

func (s *KnowledgeLearningService) markEditNeedsReview(ctx context.Context, userID string, doc KnowledgeDocument, itemID, requestID string) error {
	_, err := s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
		for i := range latest.Items {
			item := &latest.Items[i]
			if item.ID == itemID && item.State == KnowledgeNeedsReview && item.Prepared != nil && item.Prepared.RequestID == requestID {
				item.Prepared = nil
				item.Version++
				item.UpdatedAt = s.now().UTC()
				return nil
			}
		}
		return ErrConflict
	})
	if err != nil {
		return err
	}
	return ErrConflict
}
