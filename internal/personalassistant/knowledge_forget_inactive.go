package personalassistant

import (
	"context"
	"strings"
)

// ForgetItem extends canonical Forget to a successfully suspended item whose
// active line has already been removed. No previous revision/evidence plaintext
// survives. The exact reader still refuses a stale manually reintroduced
// marker; such a conflict needs an explicit canonical repair, not a false
// successful Forget receipt.
func (s *KnowledgeLearningService) ForgetItem(ctx context.Context, userID, itemID string, expectedVersion int64, requestID string) (KnowledgeItem, error) {
	if s == nil || s.store == nil || s.memory == nil || s.now == nil ||
		itemID == "" || expectedVersion < 1 || !validInterviewID(requestID) {
		return KnowledgeItem{}, ErrConflict
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return KnowledgeItem{}, err
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return KnowledgeItem{}, err
	}
	item, found := knowledgeItemByID(doc, itemID)
	if !found {
		return KnowledgeItem{}, ErrNotFound
	}
	if item.State == KnowledgeForgotten {
		if receipt, found := knowledgeReceiptByID(doc, requestID); found && receipt.ItemID == itemID && receipt.Result == "forgotten" {
			return item, nil
		}
		return KnowledgeItem{}, ErrConflict
	}
	if item.State != KnowledgeNeedsReview || item.Target != nil || item.Prepared != nil {
		return s.ForgetApproved(ctx, userID, itemID, expectedVersion, requestID)
	}
	if item.Version != expectedVersion {
		return KnowledgeItem{}, ErrConflict
	}
	snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
	if err != nil {
		return KnowledgeItem{}, err
	}
	prefix := "ori-hq:" + item.ID + ":"
	for _, entry := range snapshot.Entries {
		if strings.HasPrefix(entry.Entry.Provenance, prefix) {
			return KnowledgeItem{}, ErrConflict
		}
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
			if current.Version != expectedVersion || current.State != KnowledgeNeedsReview || current.Target != nil || current.Prepared != nil {
				return ErrConflict
			}
			keys := knowledgeSuppressionKeys(*current)
			if !hasKnowledgeTombstoneCapacity(*latest, keys) {
				return ErrKnowledgeLimit
			}
			if _, used := knowledgeReceiptByID(*latest, requestID); used {
				return ErrConflict
			}
			at := s.now().UTC()
			for _, key := range keys {
				if !keySuppressed(*latest, key) {
					latest.Tombstones = append(latest.Tombstones, KnowledgeTombstone{SemanticKey: key, ItemID: itemID, CreatedAt: at})
				}
			}
			current.Revisions = nil
			current.CurrentRevisionID = ""
			current.State = KnowledgeForgotten
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
	return forgotten, err
}
