package personalassistant

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ForgetApproved first durably removes approval and proposal plaintext from
// the sidecar, preserving semantic suppression. Only then does it delete the
// exact canonical line. A failure leaves an inert prepared record; success is
// reported only after canonical absence and sidecar finalization are verified.
func (s *KnowledgeLearningService) ForgetApproved(ctx context.Context, userID, itemID string, expectedVersion int64, requestID string) (KnowledgeItem, error) {
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
		item, ok := knowledgeItemByID(doc, itemID)
		if !ok {
			return KnowledgeItem{}, ErrNotFound
		}
		if receipt, found := knowledgeReceiptByID(doc, requestID); found {
			if receipt.ItemID == itemID && receipt.Result == "forgotten" && item.State == KnowledgeForgotten {
				return item, nil
			}
			return KnowledgeItem{}, ErrConflict
		}
		if item.Target != nil && item.Target.Kind == "profile" {
			return s.forgetApprovedProfile(ctx, userID, itemID, expectedVersion, requestID)
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
					if current.ID != itemID {
						continue
					}
					if current.Version != expectedVersion || current.State != KnowledgeApproved || current.Prepared != nil || current.Target == nil {
						return ErrConflict
					}
					keys := knowledgeSuppressionKeys(*current)
					if !hasKnowledgeTombstoneCapacity(*latest, keys) {
						return ErrKnowledgeLimit
					}
					at := s.now().UTC()
					for _, key := range keys {
						if !keySuppressed(*latest, key) {
							latest.Tombstones = append(latest.Tombstones, KnowledgeTombstone{SemanticKey: key, ItemID: itemID, CreatedAt: at})
						}
					}
					current.Revisions = nil // all source text, edited text and evidence are erased before canonical delete
					current.CurrentRevisionID = ""
					current.State = KnowledgeNeedsReview // excluded even if the canonical write fails
					current.Prepared = &KnowledgeOperation{RequestID: requestID, Kind: "forget", OldHash: current.Target.CanonicalHash}
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
		if item.State != KnowledgeNeedsReview || item.Prepared.Kind != "forget" || item.Prepared.RequestID != requestID ||
			item.Target.CanonicalHash != item.Prepared.OldHash {
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
				return KnowledgeItem{}, err // prepared exclusion remains for retry
			}
		case workspace.MemoryExactMissing:
			// A preceding attempt or an outside delete already removed the
			// exact managed marker. No stale sidecar value is ever recreated.
		default:
			return KnowledgeItem{}, s.markForgetNeedsReview(ctx, userID, doc, itemID, requestID)
		}
		status, err = s.memory.InspectExact(binding.HQWorkspaceID, target)
		if err != nil || status != workspace.MemoryExactMissing {
			return KnowledgeItem{}, memoryVerificationError(err)
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return KnowledgeItem{}, err
		}
		var forgotten KnowledgeItem
		_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			for i := range latest.Items {
				current := &latest.Items[i]
				if current.ID != itemID {
					continue
				}
				if current.Version != expectedVersion || current.State != KnowledgeNeedsReview || current.Prepared == nil ||
					current.Prepared.RequestID != requestID || current.Prepared.Kind != "forget" {
					return ErrConflict
				}
				at := s.now().UTC()
				current.State = KnowledgeForgotten
				current.Prepared = nil
				current.Target = nil
				current.Version++
				current.UpdatedAt = at
				if len(latest.Receipts) >= knowledgeMaxReceipts {
					latest.Receipts = latest.Receipts[1:]
				}
				latest.Receipts = append(latest.Receipts, KnowledgeReceipt{RequestID: requestID, ItemID: itemID, Result: "forgotten", CreatedAt: at})
				forgotten = *current
				return nil
			}
			return ErrNotFound
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		return forgotten, err
	}
	return KnowledgeItem{}, ErrConflict
}

func (s *KnowledgeLearningService) markForgetNeedsReview(ctx context.Context, userID string, doc KnowledgeDocument, itemID, requestID string) error {
	_, err := s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
		for i := range latest.Items {
			item := &latest.Items[i]
			if item.ID == itemID && item.State == KnowledgeNeedsReview && item.Prepared != nil && item.Prepared.RequestID == requestID {
				item.Prepared = nil // never remove an outside-edited value using an old hash
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
