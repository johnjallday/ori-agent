package userprofile

import (
	"context"
	"errors"
	"time"
)

// SetFieldsIfVersion keeps an agent's multi-field profile_set atomic with a
// server-owned review guard that inspected this exact canonical version. If a
// user clears or edits a preference between that check and the SQL write, the
// tool returns a conflict instead of restoring stale wording.
func (s *SQLiteStore) SetFieldsIfVersion(ctx context.Context, id string, fields map[string]any, expected time.Time) (*UserProfile, error) {
	if expected.IsZero() {
		return nil, ErrProfileConflict
	}
	id = normalizeUserID(id)
	current, err := s.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrProfileConflict
	}
	if err != nil {
		return nil, err
	}
	if !current.UpdatedAt.Equal(expected) {
		return nil, ErrProfileConflict
	}
	if err := applyProfileFields(current, fields); err != nil {
		return nil, err
	}
	if err := s.UpsertIfVersion(ctx, current, expected); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}
