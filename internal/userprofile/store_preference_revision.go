package userprofile

import (
	"context"
	"database/sql"
	"errors"
)

// PreferenceRevision reports a SQL-owned, monotonic generation for a single
// canonical preference. It has no value authority: callers must read the
// current profile as well. A missing generation fails closed for review use.
func (s *SQLiteStore) PreferenceRevision(ctx context.Context, id, field string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("user profile store is not configured")
	}
	if _, ok := allowedPreferenceKeys[field]; !ok {
		return 0, ErrUnknownPreference
	}
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM user_preference_revisions WHERE user_id = ? AND field = ?`,
		normalizeUserID(id), field).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return revision, err
}
