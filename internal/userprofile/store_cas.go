package userprofile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrProfileConflict = errors.New("user profile changed; refresh before editing")

// UpdateFieldCAS changes only one explicitly reviewed canonical field. This
// user-initiated API differs from SetFields, which deliberately refuses agent
// changes to identity. The SQL UPDATE checks the last-observed version and
// value atomically; unrelated profile fields and Personal HQ designation are
// never round-tripped through a stale struct.
func (s *SQLiteStore) UpdateFieldCAS(ctx context.Context, userID, field string, expectedUpdatedAt time.Time, expectedValue, newValue string) (*UserProfile, error) {
	now := time.Now().UTC()
	if !now.After(expectedUpdatedAt) {
		now = expectedUpdatedAt.Add(time.Nanosecond)
	}
	return s.UpdateFieldCASAt(ctx, userID, field, expectedUpdatedAt, expectedValue, newValue, now)
}

// UpdateFieldCASAt uses a server-chosen, persisted version for a prepared
// interview operation. Exact version+value checks make a retry distinguish its
// own earlier SQL write from an unrelated edit with identical wording.
func (s *SQLiteStore) UpdateFieldCASAt(ctx context.Context, userID, field string, expectedUpdatedAt time.Time, expectedValue, newValue string, writtenAt time.Time) (*UserProfile, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("user profile store is not configured")
	}
	if expectedUpdatedAt.IsZero() || !writtenAt.After(expectedUpdatedAt) {
		return nil, ErrProfileConflict
	}
	userID = normalizeUserID(userID)
	newValue = strings.TrimSpace(newValue)
	if err := ValidateFreeText(newValue); err != nil {
		return nil, err
	}
	if strings.HasPrefix(field, "preferences.") {
		key := strings.TrimPrefix(field, "preferences.")
		if _, allowed := allowedPreferenceKeys[key]; !allowed {
			return nil, fmt.Errorf("%w: %s", ErrUnknownPreference, key)
		}
		newValue = strings.Join(strings.Fields(newValue), " ")
		path := "$." + key // key is checked against the closed allowlist above
		var query string
		if newValue == "" {
			query = `UPDATE users SET preferences = json_remove(COALESCE(preferences, '{}'), ?), updated_at = ?
				WHERE id = ? AND updated_at = ? AND COALESCE(json_extract(preferences, ?), '') = ?`
		} else {
			query = `UPDATE users SET preferences = json_set(COALESCE(preferences, '{}'), ?, ?), updated_at = ?
				WHERE id = ? AND updated_at = ? AND COALESCE(json_extract(preferences, ?), '') = ?`
		}
		var resultArgs []any
		if newValue == "" {
			resultArgs = []any{path, writtenAt, userID, expectedUpdatedAt, path, expectedValue}
		} else {
			resultArgs = []any{path, newValue, writtenAt, userID, expectedUpdatedAt, path, expectedValue}
		}
		return s.executeFieldCAS(ctx, userID, query, resultArgs...)
	}
	column := ""
	switch field {
	case "display_name":
		column = "display_name"
	case "email":
		column = "email"
	case "timezone":
		column = "timezone"
	case "locale":
		column = "locale"
	case "role_category":
		column = "role_category"
	case "about":
		column = "about"
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownField, field)
	}
	// column is chosen only from the static switch above, never request input.
	query := `UPDATE users SET ` + column + ` = ?, updated_at = ? WHERE id = ? AND updated_at = ? AND ` + column + ` = ?` // #nosec G201 -- static allowlisted SQL column
	return s.executeFieldCAS(ctx, userID, query, newValue, writtenAt, userID, expectedUpdatedAt, expectedValue)
}

func (s *SQLiteStore) executeFieldCAS(ctx context.Context, userID, query string, args ...any) (*UserProfile, error) {
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update user profile field: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("inspect user profile field update: %w", err)
	}
	if count != 1 {
		return nil, ErrProfileConflict
	}
	return s.Get(ctx, userID)
}
