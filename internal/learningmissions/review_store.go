package learningmissions

import (
	"context"
	"encoding/json"
)

func readReview(ctx context.Context, q queryer, owner, id string) (Review, error) {
	var data string
	var epoch int64
	err := q.QueryRowContext(ctx, `SELECT record,epoch FROM learning_reviews WHERE owner_id=? AND id=?`, owner, id).Scan(&data, &epoch)
	var r Review
	if err != nil || len(data) > MaxResponseBytes || json.Unmarshal([]byte(data), &r) != nil || r.ID != id || r.Epoch != epoch || r.Validate() != nil {
		return Review{}, ErrUnavailable
	}
	return r, nil
}

func (s *SQLiteStore) Review(ctx context.Context, owner, id string) (Review, error) {
	if s == nil || s.db == nil || !validID(owner) || !validID(id) {
		return Review{}, ErrUnavailable
	}
	return readReview(ctx, s.db, owner, id)
}

func (m *Mutation) Review(id string) (Review, error) {
	return readReview(m.ctx, m.tx, m.owner.ID, id)
}

func (m *Mutation) ConsumeReview(r Review) error {
	if r.Epoch != m.owner.Epoch || r.Decision == "" || r.Validate() != nil {
		return ErrInvalid
	}
	before, err := m.Review(r.ID)
	if err != nil {
		return err
	}
	if before.Decision != "" {
		return ErrConflict
	}
	// Only owner decision fields may change; every reference/support field is
	// the original host-issued challenge, not the caller's reconstruction.
	before.Decision, before.Reason, before.Note, before.Change, before.DecidedAt = r.Decision, r.Reason, r.Note, r.Change, r.DecidedAt
	want, err := encode(before, MaxResponseBytes)
	if err != nil {
		return err
	}
	data, err := encode(r, MaxResponseBytes)
	if err != nil {
		return err
	}
	if data != want {
		return ErrConflict
	}
	_, err = m.tx.ExecContext(m.ctx, `UPDATE learning_reviews SET record=? WHERE owner_id=? AND id=? AND epoch=?`, data, m.owner.ID, r.ID, r.Epoch)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
