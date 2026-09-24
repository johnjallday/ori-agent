package personalassistant

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SaveExplicit records a user's exact confirmed HQ statement. It shares the
// same canonical exact-write/prepare/retry path as reviewed suggestions, but
// bypasses autonomous source evidence and never consumes proposal quota.
// A request ID identifies one user action, not the meaning: after Forget the
// user may explicitly state the same fact again under a new request ID.
func (s *KnowledgeLearningService) SaveExplicit(ctx context.Context, userID string, stateVersion int64, requestID, category, text string) (KnowledgeItem, error) {
	if s == nil || s.store == nil || s.memory == nil || s.now == nil ||
		!validInterviewID(requestID) || stateVersion < 1 {
		return KnowledgeItem{}, ErrConflict
	}
	switch category {
	case "how_you_work", "people", "projects", "routines":
	default:
		return KnowledgeItem{}, ErrValidation
	}
	clean, err := workspace.ValidateMemoryText(text)
	if err != nil || clean != text {
		return KnowledgeItem{}, ErrValidation
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return KnowledgeItem{}, err
	}
	if binding.StateVersion != stateVersion {
		return KnowledgeItem{}, ErrConflict
	}
	// Use a separate deterministic approval ID, so the creation receipt and
	// finalization receipt cannot be confused with a source review operation.
	approvalID := reconfirmApprovalID("explicit:" + requestID)
	for range knowledgeRetryLimit {
		doc, err := s.store.Read(ctx, userID)
		if err != nil {
			return KnowledgeItem{}, err
		}
		if receipt, found := knowledgeReceiptByID(doc, requestID); found {
			if receipt.Result != "explicit_started" {
				return KnowledgeItem{}, ErrConflict
			}
			item, ok := knowledgeItemByID(doc, receipt.ItemID)
			if !ok || item.SourceKind != "explicit" || item.Category != category ||
				item.CurrentRevisionID != receipt.RevisionID {
				return KnowledgeItem{}, ErrConflict
			}
			for _, revision := range item.Revisions {
				if revision.ID == receipt.RevisionID && revision.Text == text {
					return s.ApproveCandidate(ctx, userID, item.ID, 1, approvalID)
				}
			}
			return KnowledgeItem{}, ErrConflict
		}
		if len(doc.Items) >= knowledgeMaxItems {
			return KnowledgeItem{}, ErrKnowledgeLimit
		}
		// An exact statement already held in canonical memory should not be
		// duplicated with a new marker, even if it predates this sidecar.
		snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
		if err != nil {
			return KnowledgeItem{}, err
		}
		for _, entry := range snapshot.Entries {
			if entry.Entry.Text == text {
				return KnowledgeItem{}, ErrConflict
			}
		}
		for _, existing := range doc.Items {
			if existing.State == KnowledgeRejected || existing.State == KnowledgeForgotten {
				continue
			}
			for _, revision := range existing.Revisions {
				if revision.Text == text {
					return KnowledgeItem{}, ErrConflict
				}
			}
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return KnowledgeItem{}, err
		}
		at := s.now().UTC()
		item := KnowledgeItem{
			ID: uuid.NewString(), Version: 1, State: KnowledgeCandidate,
			SourceKind: "explicit", Category: category, CreatedAt: at, UpdatedAt: at,
			SemanticKey: proposalSemanticKey(binding, KnowledgeProposal{
				SourceKind: "explicit", ScopeID: binding.HQWorkspaceID,
				SubjectID: requestID, Predicate: "confirmed statement", Value: category,
			}),
		}
		item.Revisions = []KnowledgeRevision{{ID: uuid.NewString(), Text: text, CreatedAt: at}}
		item.CurrentRevisionID = item.Revisions[0].ID
		_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			if _, reused := knowledgeReceiptByID(*latest, requestID); reused {
				return ErrConflict
			}
			if len(latest.Items) >= knowledgeMaxItems {
				return ErrKnowledgeLimit
			}
			latest.Items = append(latest.Items, item)
			if len(latest.Receipts) >= knowledgeMaxReceipts {
				latest.Receipts = latest.Receipts[1:]
			}
			latest.Receipts = append(latest.Receipts, KnowledgeReceipt{
				RequestID: requestID, ItemID: item.ID, Result: "explicit_started", RevisionID: item.CurrentRevisionID, CreatedAt: at,
			})
			return nil
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return KnowledgeItem{}, err
		}
		return s.ApproveCandidate(ctx, userID, item.ID, 1, approvalID)
	}
	return KnowledgeItem{}, ErrConflict
}

func (s *KnowledgeLearningService) CurrentStateVersion(ctx context.Context, userID string) (int64, error) {
	if s == nil || s.store == nil {
		return 0, ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	return binding.StateVersion, err
}
