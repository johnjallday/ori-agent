package personalassistant

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SuspendApproved is called only after an authorized source adapter has
// established a real contradiction (e.g. a durable UndoDone). It excludes the
// old approved revision before canonical deletion; the inactive revision is
// retained for explicit evidence-backed re-review, not prompt projection.
func (s *KnowledgeLearningService) SuspendApproved(ctx context.Context, userID, itemID string, expectedVersion int64, requestID string) (KnowledgeItem, error) {
	if s == nil || s.store == nil || s.memory == nil || s.now == nil {
		return KnowledgeItem{}, ErrRepairNeeded
	}
	if itemID == "" || expectedVersion < 1 || !validInterviewID(requestID) {
		return KnowledgeItem{}, ErrConflict
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
		item, found := knowledgeItemByID(doc, itemID)
		if !found {
			return KnowledgeItem{}, ErrNotFound
		}
		if receipt, found := knowledgeReceiptByID(doc, requestID); found {
			if receipt.ItemID == itemID && receipt.Result == "suspended" && item.State == KnowledgeNeedsReview {
				return item, nil
			}
			return KnowledgeItem{}, ErrConflict
		}
		if item.Version != expectedVersion || item.Target == nil || item.Target.Kind != "memory" {
			return KnowledgeItem{}, ErrConflict
		}
		if item.Prepared == nil {
			if item.State != KnowledgeApproved {
				return KnowledgeItem{}, ErrConflict
			}
			_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
				for i := range latest.Items {
					current := &latest.Items[i]
					if current.ID == itemID && current.Version == expectedVersion && current.State == KnowledgeApproved && current.Prepared == nil && current.Target != nil {
						current.State = KnowledgeNeedsReview
						current.Prepared = &KnowledgeOperation{RequestID: requestID, Kind: "suspend", OldHash: current.Target.CanonicalHash}
						current.UpdatedAt = s.now().UTC()
						return nil
					}
				}
				return ErrConflict
			})
			if errors.Is(err, ErrConflict) {
				continue
			}
			if err != nil {
				return KnowledgeItem{}, err
			}
			continue
		}
		if item.State != KnowledgeNeedsReview || item.Prepared.Kind != "suspend" || item.Prepared.RequestID != requestID || item.Prepared.OldHash != item.Target.CanonicalHash {
			return KnowledgeItem{}, ErrConflict
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return KnowledgeItem{}, err
		}
		target := workspace.MemoryExactTarget{Provenance: item.Target.Marker, LineHash: item.Target.CanonicalHash}
		status, err := s.memory.InspectExact(binding.HQWorkspaceID, target)
		if err != nil {
			return KnowledgeItem{}, err
		}
		switch status {
		case workspace.MemoryExactUnchanged:
			snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
			if err != nil {
				return KnowledgeItem{}, err
			}
			target.FileHash = snapshot.FileHash
			if err := s.memory.EditExact(binding.HQWorkspaceID, target, nil); err != nil {
				return KnowledgeItem{}, err
			}
		case workspace.MemoryExactMissing:
			// A previous attempt or outside edit already removed this marker.
		default:
			return KnowledgeItem{}, s.markEditNeedsReview(ctx, userID, doc, itemID, requestID)
		}
		status, err = s.memory.InspectExact(binding.HQWorkspaceID, target)
		if err != nil || status != workspace.MemoryExactMissing {
			return KnowledgeItem{}, memoryVerificationError(err)
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return KnowledgeItem{}, err
		}
		var suspended KnowledgeItem
		_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			for i := range latest.Items {
				current := &latest.Items[i]
				if current.ID != itemID {
					continue
				}
				if current.Version != expectedVersion || current.State != KnowledgeNeedsReview || current.Prepared == nil ||
					current.Prepared.RequestID != requestID || current.Prepared.Kind != "suspend" {
					return ErrConflict
				}
				at := s.now().UTC()
				current.Prepared = nil
				current.Target = nil
				current.Version++
				current.UpdatedAt = at
				if len(latest.Receipts) >= knowledgeMaxReceipts {
					latest.Receipts = latest.Receipts[1:]
				}
				latest.Receipts = append(latest.Receipts, KnowledgeReceipt{RequestID: requestID, ItemID: itemID, Result: "suspended", CreatedAt: at})
				suspended = *current
				return nil
			}
			return ErrNotFound
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		return suspended, err
	}
	return KnowledgeItem{}, ErrConflict
}
