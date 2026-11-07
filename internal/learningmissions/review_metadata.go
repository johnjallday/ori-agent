package learningmissions

import (
	"context"
	"slices"
)

// ReviewedResultMetadata is availability metadata, not an evidence proof or a
// review challenge. Reading it never decodes decision notes or original input.
type ReviewedResultMetadata struct {
	ReviewID    string
	InputDigest string
	Result      ResultRef
	Decision    string
}

func (s *SQLiteStore) ReviewMetadata(ctx context.Context, owner, id string) (ReviewedResultMetadata, error) {
	var out ReviewedResultMetadata
	if s == nil || s.db == nil || !validID(owner) || !validID(id) {
		return out, ErrUnavailable
	}
	err := s.db.QueryRowContext(ctx, `SELECT id,json_extract(record,'$.input_digest'),json_extract(record,'$.decision'),
 json_extract(record,'$.result.studio_id'),json_extract(record,'$.result.incarnation'),json_extract(record,'$.result.ticket_id'),
 json_extract(record,'$.result.run_id'),json_extract(record,'$.result.artifact_id'),json_extract(record,'$.result.digest')
 FROM learning_reviews WHERE owner_id=? AND id=? AND json_extract(record,'$.decided_at_ms')>0 AND COALESCE(json_extract(record,'$.compacted'),0)=0`, owner, id).Scan(&out.ReviewID, &out.InputDigest, &out.Decision,
		&out.Result.WorkspaceID, &out.Result.Incarnation, &out.Result.TicketID, &out.Result.RunID, &out.Result.ArtifactID, &out.Result.Digest)
	if err != nil || out.ReviewID != id || !validDigest(out.InputDigest) || out.Result.Validate() != nil || !slices.Contains([]string{"use", "revise", "reject"}, out.Decision) {
		return ReviewedResultMetadata{}, ErrUnavailable
	}
	return out, nil
}
