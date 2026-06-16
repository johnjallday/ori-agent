package userprofile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

type SQLiteStore struct {
	db *database.DB
}

func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*UserProfile, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("user profile store is not configured")
	}
	id = normalizeUserID(id)
	var profile UserProfile
	var specializationsJSON, preferencesJSON sql.NullString
	var createdAtRaw, updatedAtRaw any
	err := s.db.QueryRowContext(ctx, `
		SELECT id, display_name, email, timezone, locale, role_category, specializations, preferences, about, created_at, updated_at
		FROM users
		WHERE id = ?
	`, id).Scan(
		&profile.ID,
		&profile.DisplayName,
		&profile.Email,
		&profile.Timezone,
		&profile.Locale,
		&profile.RoleCategory,
		&specializationsJSON,
		&preferencesJSON,
		&profile.About,
		&createdAtRaw,
		&updatedAtRaw,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user profile: %w", err)
	}
	if specializationsJSON.Valid && strings.TrimSpace(specializationsJSON.String) != "" {
		_ = json.Unmarshal([]byte(specializationsJSON.String), &profile.Specializations)
	}
	if preferencesJSON.Valid && strings.TrimSpace(preferencesJSON.String) != "" {
		_ = json.Unmarshal([]byte(preferencesJSON.String), &profile.Preferences)
	}
	if createdAt, err := parseTime(createdAtRaw); err == nil {
		profile.CreatedAt = createdAt
	}
	if updatedAt, err := parseTime(updatedAtRaw); err == nil {
		profile.UpdatedAt = updatedAt
	}
	return &profile, nil
}

func (s *SQLiteStore) Upsert(ctx context.Context, profile *UserProfile) error {
	if s == nil || s.db == nil {
		return errors.New("user profile store is not configured")
	}
	normalized, err := Normalize(profile)
	if err != nil {
		return err
	}
	specJSON, err := json.Marshal(emptySlice(normalized.Specializations))
	if err != nil {
		return fmt.Errorf("failed to encode specializations: %w", err)
	}
	prefsJSON, err := json.Marshal(emptyMap(normalized.Preferences))
	if err != nil {
		return fmt.Errorf("failed to encode preferences: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users (id, display_name, email, timezone, locale, role_category, specializations, preferences, about, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			display_name = excluded.display_name,
			email = excluded.email,
			timezone = excluded.timezone,
			locale = excluded.locale,
			role_category = excluded.role_category,
			specializations = excluded.specializations,
			preferences = excluded.preferences,
			about = excluded.about,
			updated_at = excluded.updated_at
	`, normalized.ID, normalized.DisplayName, normalized.Email, normalized.Timezone, normalized.Locale, normalized.RoleCategory,
		string(specJSON), string(prefsJSON), normalized.About, now, now)
	if err != nil {
		return fmt.Errorf("failed to upsert user profile: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SetFields(ctx context.Context, id string, fields map[string]any) (*UserProfile, error) {
	if len(fields) == 0 {
		return s.Get(ctx, id)
	}
	id = normalizeUserID(id)
	current, err := s.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		current = &UserProfile{ID: id}
	} else if err != nil {
		return nil, err
	}
	if current.Preferences == nil {
		current.Preferences = map[string]string{}
	}

	for rawField, rawValue := range fields {
		field := strings.TrimSpace(rawField)
		switch {
		case field == "about":
			value := profileFieldValue(rawValue)
			if err := ValidateFreeText(value); err != nil {
				return nil, err
			}
			current.About = value
		case strings.HasPrefix(field, "preferences."):
			key := strings.TrimPrefix(field, "preferences.")
			if _, ok := allowedPreferenceKeys[key]; !ok {
				return nil, fmt.Errorf("%w: %s", ErrUnknownPreference, key)
			}
			value := strings.Join(strings.Fields(profileFieldValue(rawValue)), " ")
			if value == "" {
				delete(current.Preferences, key)
				continue
			}
			if err := ValidateFreeText(value); err != nil {
				return nil, fmt.Errorf("%s: %w", field, err)
			}
			current.Preferences[key] = value
		case isIdentityField(field):
			return nil, fmt.Errorf("%w: %s", ErrIdentityField, field)
		default:
			return nil, fmt.Errorf("%w: %s", ErrUnknownField, field)
		}
	}
	if len(current.Preferences) == 0 {
		current.Preferences = nil
	}
	if err := s.Upsert(ctx, current); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func profileFieldValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func isIdentityField(field string) bool {
	switch field {
	case "display_name", "email", "timezone", "locale":
		return true
	default:
		return false
	}
}

func normalizeUserID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return LocalUserID
	}
	return id
}

func emptySlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func emptyMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func parseTime(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case string:
		return time.Parse(time.RFC3339Nano, v)
	case []byte:
		return time.Parse(time.RFC3339Nano, string(v))
	default:
		return time.Time{}, fmt.Errorf("unsupported time type %T", value)
	}
}

var _ UserStore = (*SQLiteStore)(nil)
