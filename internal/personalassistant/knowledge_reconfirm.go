package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// KnowledgeReconfirmationAuthority returns a *new*, bounded server-verified
// evidence checkpoint for an existing source meaning. It never authorizes
// changed scope or a browser-provided source. Old undone evidence cannot be
// copied forward to a new reviewed revision.
type KnowledgeReconfirmationAuthority interface {
	FreshEvidence(context.Context, KnowledgeBinding, KnowledgeItem) ([]KnowledgeEvidence, error)
}

func KnowledgeEvidenceCheckpoint(evidence []KnowledgeEvidence) string {
	values := make([][2]string, 0, len(evidence))
	for _, e := range evidence {
		values = append(values, [2]string{e.SourceKind + ":" + e.SourceID, e.ObservedAt.UTC().Format(time.RFC3339Nano)})
	}
	encoded, _ := json.Marshal(values)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func reconfirmApprovalID(requestID string) string {
	sum := sha256.Sum256([]byte("reconfirm:" + requestID))
	return "reconfirm-approve-" + hex.EncodeToString(sum[:16])
}

// Reconfirm records the exact visible revision and current source checkpoint
// as a fresh inert candidate, then runs the same crash-safe approval path as
// any other source-derived fact. The first sidecar write is a durable receipt:
// a retry after a lost response resumes approval, never creates another line.
// It does not spend the quota reserved for *new* automatic proposals.
func (s *KnowledgeLearningService) Reconfirm(ctx context.Context, userID, itemID string, expectedVersion int64, requestID, revisionID, checkpoint, text string) (KnowledgeItem, error) {
	if s == nil || s.store == nil || s.memory == nil || s.authority == nil || s.now == nil ||
		itemID == "" || expectedVersion < 1 || !validInterviewID(requestID) || revisionID == "" || len(checkpoint) != 64 {
		return KnowledgeItem{}, ErrConflict
	}
	clean, err := workspace.ValidateMemoryText(text)
	if err != nil || clean != text {
		return KnowledgeItem{}, ErrValidation
	}
	authority, ok := s.authority.(KnowledgeReconfirmationAuthority)
	if !ok {
		return KnowledgeItem{}, ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return KnowledgeItem{}, err
	}
	approvalID := reconfirmApprovalID(requestID)
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
			if receipt.ItemID != itemID || receipt.Result != "reconfirm_started" ||
				item.Version < expectedVersion+1 || receipt.RevisionID != item.CurrentRevisionID {
				return KnowledgeItem{}, ErrConflict
			}
			for _, revision := range item.Revisions {
				if revision.ID == receipt.RevisionID && revision.Text == text &&
					KnowledgeEvidenceCheckpoint(revision.Evidence) == checkpoint {
					return s.ApproveCandidate(ctx, userID, itemID, expectedVersion+1, approvalID)
				}
			}
			return KnowledgeItem{}, ErrConflict
		}
		if item.Version != expectedVersion || item.CurrentRevisionID != revisionID || item.SourceKind != "file_janitor" ||
			item.State != KnowledgeNeedsReview || item.Prepared != nil || item.Target != nil || len(item.Revisions) >= knowledgeMaxRevisions {
			return KnowledgeItem{}, ErrConflict
		}
		evidence, err := authority.FreshEvidence(ctx, binding, item)
		if err != nil || len(evidence) != 3 || KnowledgeEvidenceCheckpoint(evidence) != checkpoint {
			return KnowledgeItem{}, ErrConflict
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return KnowledgeItem{}, err
		}
		at := s.now().UTC()
		revision := KnowledgeRevision{ID: uuid.NewString(), Text: text, Evidence: evidence, CreatedAt: at, EditedAt: &at}
		_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			if _, reused := knowledgeReceiptByID(*latest, requestID); reused {
				return ErrConflict
			}
			for i := range latest.Items {
				current := &latest.Items[i]
				if current.ID != itemID {
					continue
				}
				if current.Version != expectedVersion || current.State != KnowledgeNeedsReview ||
					current.CurrentRevisionID != revisionID || current.Target != nil || current.Prepared != nil {
					return ErrConflict
				}
				current.Revisions = append(current.Revisions, revision)
				current.CurrentRevisionID = revision.ID
				current.Version++
				current.State = KnowledgeCandidate // still inert until exact canonical approval succeeds
				current.UpdatedAt = at
				if len(latest.Receipts) >= knowledgeMaxReceipts {
					latest.Receipts = latest.Receipts[1:]
				}
				latest.Receipts = append(latest.Receipts, KnowledgeReceipt{
					RequestID: requestID, ItemID: itemID, Result: "reconfirm_started", RevisionID: revision.ID, CreatedAt: at,
				})
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
		return s.ApproveCandidate(ctx, userID, itemID, expectedVersion+1, approvalID)
	}
	return KnowledgeItem{}, ErrConflict
}
