package projectlibrary

import "github.com/johnjallday/ori-agent/internal/workspacearrangement"

func collectionWitnessesMatch(doc Document, saved Arrangement) bool {
	if saved.Record.State == "awaiting_user_review" {
		found := false
		for _, review := range doc.Reviews {
			if review.Action == collectionReviewAction && review.TargetID == saved.Record.ID && review.Collection != nil &&
				review.Collection.ArrangementRevision == saved.Record.Revision && review.Collection.BindingDigest == saved.BindingDigest && review.ConsumedAt == nil {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if receipt := saved.CollectionReceipt; receipt != nil {
		consumed := false
		for _, review := range doc.Reviews {
			if review.Action == collectionReviewAction && review.TargetID == saved.Record.ID && review.Digest == receipt.Digest && review.Collection != nil && review.ConsumedAt != nil && review.ConsumedAt.Equal(receipt.AppliedAt) {
				consumed = true
			}
		}
		if !consumed {
			return false
		}
		found := false
		for _, op := range doc.Operations {
			if op.Action == collectionReviewAction && op.ConsequenceID == saved.Record.ID && op.Digest == receipt.Digest && op.Revision == receipt.CatalogRevision && !op.RecordedAt.Before(receipt.AppliedAt) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func validCollectionReview(review ReviewReceipt, schema int) bool {
	witness := review.Collection
	if witness == nil {
		return review.Action != collectionReviewAction
	}
	return schema == ArrangementSchemaVersion && review.Action == collectionReviewAction && collectionLabel(review.TargetID, 160) &&
		validDigest(review.Digest) && review.Revision > 0 && witness.ArrangementRevision > 0 && validDigest(witness.BindingDigest) &&
		!witness.PreparedAt.IsZero() && review.ExpiresAt.After(witness.PreparedAt) &&
		(review.ConsumedAt == nil || !review.ConsumedAt.Before(witness.PreparedAt))
}

func validCollectionReceipt(saved Arrangement, catalogRevision int64) bool {
	receipt := saved.CollectionReceipt
	if receipt == nil {
		return saved.Record.State != "completed"
	}
	if saved.outcomeKind() != "proposal" || (saved.Record.State != "completed" && saved.Record.State != "stale") ||
		!validDigest(receipt.Digest) || receipt.CatalogRevision < 1 || receipt.CatalogRevision > catalogRevision ||
		receipt.AppliedAt.Before(saved.Record.CreatedAt) || receipt.AppliedAt.After(saved.Record.UpdatedAt) || len(receipt.Effects) > workspacearrangement.MaxOperations {
		return false
	}
	seen := map[string]bool{}
	queued := false
	for _, effect := range receipt.Effects {
		if effect.Kind == workspacearrangement.SelectActivation {
			if queued || !collectionLabel(effect.QueueID, 160) || effect.CollectionID != "" || effect.SelectedCount < 1 || effect.SelectedCount > 100 || effect.BeforeRevision != 0 || effect.AfterRevision != 0 {
				return false
			}
			queued = true
			continue
		}
		if effect.QueueID != "" || effect.SelectedCount != 0 {
			return false
		}
		if !collectionLabel(effect.CollectionID, 160) || seen[effect.CollectionID] {
			return false
		}
		seen[effect.CollectionID] = true
		switch effect.Kind {
		case workspacearrangement.CreateCollection:
			if effect.BeforeRevision != 0 || effect.AfterRevision != 1 {
				return false
			}
		case workspacearrangement.RenameCollection, workspacearrangement.SetMembers:
			if effect.BeforeRevision < 1 || effect.AfterRevision != effect.BeforeRevision+1 {
				return false
			}
		case workspacearrangement.RemoveCollection:
			if effect.BeforeRevision < 1 || effect.AfterRevision != 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
