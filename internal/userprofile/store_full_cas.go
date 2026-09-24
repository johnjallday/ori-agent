package userprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// UpsertIfVersion applies an explicit full-form edit only if the profile has
// not changed since it was displayed. Legacy Upsert remains for existing
// callers; this opt-in path prevents the dossier's full editor from bypassing
// a failed single-field CAS or overwriting another tab's newer preference.
func (s *SQLiteStore) UpsertIfVersion(ctx context.Context, profile *UserProfile, expected time.Time) error {
	if s == nil || s.db == nil {
		return ErrProfileConflict
	}
	if expected.IsZero() {
		return ErrProfileConflict
	}
	normalized, err := Normalize(profile)
	if err != nil {
		return err
	}
	specializations, err := json.Marshal(emptySlice(normalized.Specializations))
	if err != nil {
		return fmt.Errorf("encode profile specializations: %w", err)
	}
	preferences, err := json.Marshal(emptyMap(normalized.Preferences))
	if err != nil {
		return fmt.Errorf("encode profile preferences: %w", err)
	}
	now := time.Now().UTC()
	if !now.After(expected) {
		now = expected.Add(time.Nanosecond)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE users SET display_name = ?, email = ?, timezone = ?, locale = ?, role_category = ?,
			specializations = ?, preferences = ?, about = ?, updated_at = ?
		WHERE id = ? AND updated_at = ?
	`, normalized.DisplayName, normalized.Email, normalized.Timezone, normalized.Locale, normalized.RoleCategory,
		string(specializations), string(preferences), normalized.About, now, normalized.ID, expected)
	if err != nil {
		return fmt.Errorf("update user profile: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect user profile update: %w", err)
	}
	if count != 1 {
		return ErrProfileConflict
	}
	return nil
}
