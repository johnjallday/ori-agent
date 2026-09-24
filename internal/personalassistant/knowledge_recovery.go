package personalassistant

import "context"

// ResumePreparedOperation continues an already-confirmed approval, edit or
// source-contradiction suspension using only server-owned prepared metadata.
// It never accepts replacement text or a browser retry key. Source approvals
// still revalidate current authority.
func (s *KnowledgeLearningService) ResumePreparedOperation(ctx context.Context, userID, itemID string) (KnowledgeItem, error) {
	if s == nil || s.store == nil || itemID == "" {
		return KnowledgeItem{}, ErrRepairNeeded
	}
	if _, err := s.store.resolve(ctx, userID); err != nil {
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
	if item.Prepared == nil || !validInterviewID(item.Prepared.RequestID) {
		return KnowledgeItem{}, ErrConflict
	}
	switch item.Prepared.Kind {
	case "approve":
		if item.State != KnowledgeCandidate || item.Prepared.RevisionID != item.CurrentRevisionID {
			return KnowledgeItem{}, ErrConflict
		}
		return s.ApproveCandidate(ctx, userID, itemID, item.Version, item.Prepared.RequestID)
	case "edit":
		if item.State != KnowledgeNeedsReview || item.Target == nil || item.Target.Kind != "memory" {
			return KnowledgeItem{}, ErrConflict
		}
		for _, revision := range item.Revisions {
			if revision.ID == item.Prepared.RevisionID && revision.Text != "" {
				return s.EditApproved(ctx, userID, itemID, item.Version, item.Prepared.RequestID, revision.Text)
			}
		}
		return KnowledgeItem{}, ErrKnowledgeCorrupt
	case "suspend":
		if item.State != KnowledgeNeedsReview || item.Target == nil || item.Target.Kind != "memory" ||
			item.SourceKind != "file_janitor" || item.Prepared.OldHash != item.Target.CanonicalHash {
			return KnowledgeItem{}, ErrConflict
		}
		return s.SuspendApproved(ctx, userID, itemID, item.Version, item.Prepared.RequestID)
	default:
		return KnowledgeItem{}, ErrConflict
	}
}

// ResumePreparedForget resumes only the exact previously prepared Forget
// operation. The request key comes from the validated server-owned HQ sidecar,
// not the browser. It never guesses a current target or restores old plaintext
// after a browser disconnect or process restart.
func (s *KnowledgeLearningService) ResumePreparedForget(ctx context.Context, userID, itemID string) (KnowledgeItem, error) {
	if s == nil || s.store == nil || itemID == "" {
		return KnowledgeItem{}, ErrRepairNeeded
	}
	if _, err := s.store.resolve(ctx, userID); err != nil {
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
	if item.State != KnowledgeNeedsReview || item.Target == nil || item.Prepared == nil ||
		(item.Prepared.Kind != "forget" && item.Prepared.Kind != "forget_profile") ||
		!validInterviewID(item.Prepared.RequestID) {
		return KnowledgeItem{}, ErrConflict
	}
	return s.ForgetItem(ctx, userID, itemID, item.Version, item.Prepared.RequestID)
}
