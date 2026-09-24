package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ApproveCandidate is an internal, explicitly confirmed transition. Until a
// real source-specific authority is wired, it fails closed. A prepared
// candidate is always excluded from context; the final approved state is
// written only after the exact canonical value is durably verified.
func (s *KnowledgeLearningService) ApproveCandidate(ctx context.Context, userID, itemID string, expectedVersion int64, requestID string) (KnowledgeItem, error) {
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
			if receipt.ItemID == itemID && receipt.Result == "approved" && item.State == KnowledgeApproved && item.Target != nil &&
				((receipt.RevisionID != "" && receipt.RevisionID == item.CurrentRevisionID) ||
					(receipt.RevisionID == "" && item.Version == expectedVersion+1)) {
				if item.SourceKind != "explicit" {
					if s.authority == nil {
						return KnowledgeItem{}, ErrRepairNeeded
					}
					if err := s.authority.Revalidate(ctx, binding, item); err != nil {
						return KnowledgeItem{}, err
					}
				}
				status, err := s.memory.InspectExact(binding.HQWorkspaceID, workspace.MemoryExactTarget{
					Provenance: item.Target.Marker, LineHash: item.Target.CanonicalHash,
				})
				if err != nil || status != workspace.MemoryExactUnchanged {
					return KnowledgeItem{}, ErrConflict
				}
				return item, nil
			}
			return KnowledgeItem{}, ErrConflict
		}
		if item.Version != expectedVersion {
			return KnowledgeItem{}, ErrConflict
		}
		if item.State != KnowledgeCandidate {
			return KnowledgeItem{}, ErrConflict
		}
		if item.Prepared == nil {
			if item.SourceKind != "explicit" {
				if s.authority == nil {
					return KnowledgeItem{}, ErrRepairNeeded
				}
				if err := s.authority.Revalidate(ctx, binding, item); err != nil {
					return KnowledgeItem{}, err
				}
			}
			at := s.now().UTC()
			entry, err := approvalEntry(item, at)
			if err != nil {
				return KnowledgeItem{}, err
			}
			snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
			if err != nil {
				return KnowledgeItem{}, err
			}
			for _, existing := range snapshot.Entries {
				if existing.Entry.Provenance == entry.Provenance {
					return KnowledgeItem{}, ErrConflict // never adopt a pre-existing marker
				}
			}
			prepared := KnowledgeOperation{
				RequestID: requestID, Kind: "approve", OldHash: snapshot.FileHash,
				NewHash: hashKnowledgeLine(entry.Render()), RevisionID: item.CurrentRevisionID,
				PreparedAt: at,
			}
			_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
				for i := range latest.Items {
					if latest.Items[i].ID != itemID {
						continue
					}
					if latest.Items[i].Version != expectedVersion || latest.Items[i].State != KnowledgeCandidate || latest.Items[i].Prepared != nil {
						return ErrConflict
					}
					latest.Items[i].Prepared = &prepared
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
			continue // reload the durable prepared record before touching MEMORY.md
		}
		if item.Prepared.RequestID != requestID || item.Prepared.Kind != "approve" ||
			item.Prepared.RevisionID != item.CurrentRevisionID || item.Prepared.PreparedAt.IsZero() {
			return KnowledgeItem{}, ErrConflict
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return KnowledgeItem{}, err
		}
		if item.SourceKind != "explicit" {
			if s.authority == nil {
				return KnowledgeItem{}, ErrRepairNeeded
			}
			if err := s.authority.Revalidate(ctx, binding, item); err != nil {
				return KnowledgeItem{}, err
			}
		}
		entry, err := approvalEntry(item, item.Prepared.PreparedAt)
		if err != nil || hashKnowledgeLine(entry.Render()) != item.Prepared.NewHash {
			return KnowledgeItem{}, ErrKnowledgeCorrupt
		}
		target := workspace.MemoryExactTarget{LineHash: item.Prepared.NewHash, Provenance: entry.Provenance}
		status, err := s.memory.InspectExact(binding.HQWorkspaceID, target)
		if err != nil {
			return KnowledgeItem{}, err
		}
		snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
		if err != nil {
			return KnowledgeItem{}, err
		}
		switch status {
		case workspace.MemoryExactMissing:
			if snapshot.FileHash != item.Prepared.OldHash {
				return KnowledgeItem{}, s.markApprovalNeedsReview(ctx, userID, doc, itemID, requestID)
			}
			if _, err := s.memory.AppendExact(binding.HQWorkspaceID, item.Prepared.OldHash, entry); err != nil {
				return KnowledgeItem{}, err // prepared remains inert; retry verifies disk
			}
		case workspace.MemoryExactUnchanged:
			if snapshot.FileHash == item.Prepared.OldHash {
				return KnowledgeItem{}, s.markApprovalNeedsReview(ctx, userID, doc, itemID, requestID)
			}
		default:
			return KnowledgeItem{}, s.markApprovalNeedsReview(ctx, userID, doc, itemID, requestID)
		}
		status, err = s.memory.InspectExact(binding.HQWorkspaceID, target)
		if err != nil || status != workspace.MemoryExactUnchanged {
			return KnowledgeItem{}, memoryVerificationError(err)
		}
		snapshot, err = s.memory.SnapshotExact(binding.HQWorkspaceID)
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
				if current.Version != expectedVersion || current.State != KnowledgeCandidate ||
					current.Prepared == nil || current.Prepared.RequestID != requestID {
					return ErrConflict
				}
				at := s.now().UTC()
				current.State = KnowledgeApproved
				current.Target = &KnowledgeTarget{Kind: "memory", Marker: entry.Provenance,
					CanonicalHash: target.LineHash, Version: snapshot.FileHash}
				current.Prepared = nil
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
				latest.Receipts = append(latest.Receipts, KnowledgeReceipt{RequestID: requestID, ItemID: itemID, Result: "approved", RevisionID: current.CurrentRevisionID, CreatedAt: at})
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

func approvalEntry(item KnowledgeItem, at time.Time) (workspace.MemoryEntry, error) {
	return entryFromRevision(item, item.CurrentRevisionID, at)
}

func entryFromRevision(item KnowledgeItem, revisionID string, at time.Time) (workspace.MemoryEntry, error) {
	for _, revision := range item.Revisions {
		if revision.ID == revisionID && revision.Text != "" {
			return workspace.MemoryEntry{
				Type: workspace.MemoryTypeFact, Date: at.UTC().Format("2006-01-02"),
				Provenance: "ori-hq:" + item.ID + ":" + revision.ID, Text: revision.Text,
			}, nil
		}
	}
	return workspace.MemoryEntry{}, ErrKnowledgeCorrupt
}

func hashKnowledgeLine(line string) string {
	sum := sha256.Sum256([]byte(line))
	return hex.EncodeToString(sum[:])
}

func knowledgeItemByID(doc KnowledgeDocument, id string) (KnowledgeItem, bool) {
	for _, item := range doc.Items {
		if item.ID == id {
			return item, true
		}
	}
	return KnowledgeItem{}, false
}

func knowledgeReceiptByID(doc KnowledgeDocument, id string) (KnowledgeReceipt, bool) {
	for _, receipt := range doc.Receipts {
		if receipt.RequestID == id {
			return receipt, true
		}
	}
	return KnowledgeReceipt{}, false
}

func (s *KnowledgeLearningService) markApprovalNeedsReview(ctx context.Context, userID string, doc KnowledgeDocument, itemID, requestID string) error {
	_, err := s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
		for i := range latest.Items {
			item := &latest.Items[i]
			if item.ID != itemID || item.Prepared == nil || item.Prepared.RequestID != requestID || item.State != KnowledgeCandidate {
				continue
			}
			item.State = KnowledgeNeedsReview
			item.Prepared = nil
			item.Version++
			item.UpdatedAt = s.now().UTC()
			return nil
		}
		return ErrConflict
	})
	if err != nil {
		return fmt.Errorf("approval reconciliation: %w", err)
	}
	return ErrConflict
}

func memoryVerificationError(err error) error {
	if err != nil {
		return fmt.Errorf("verify reviewed memory: %w", err)
	}
	return ErrConflict
}
