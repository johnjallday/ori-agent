package learningmissions

import "context"

// RecentReviews returns only decided local receipts in the selected workspace.
// It is a bounded prerequisite lookup, not observation consent or model context.
func (s *SQLiteStore) RecentReviews(ctx context.Context, owner, workspaceID string, limit int) ([]Review, error) {
	if s == nil || s.db == nil || !validID(owner) || !validID(workspaceID) || limit < 1 || limit > MaxCandidates {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM learning_reviews WHERE owner_id=?
 AND json_extract(record,'$.result.studio_id')=? AND COALESCE(json_extract(record,'$.decision'),'')<>'' AND COALESCE(json_extract(record,'$.compacted'),0)=0
 ORDER BY created_at_ms DESC,id LIMIT ?`, owner, workspaceID, limit)
	if err != nil {
		return nil, ErrUnavailable
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, ErrUnavailable
		}
		ids = append(ids, id)
	}
	rowErr := rows.Err()
	closeErr := rows.Close()
	if rowErr != nil || closeErr != nil {
		return nil, ErrUnavailable
	}
	out := make([]Review, 0, len(ids))
	for _, id := range ids {
		review, err := s.Review(ctx, owner, id)
		if err != nil {
			return nil, err
		}
		out = append(out, review)
	}
	return out, nil
}
