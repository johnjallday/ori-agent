package userprofile

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

const (
	LocalUserID = "local"
	AboutMaxLen = 1000
)

var (
	ErrNotFound          = errors.New("user profile not found")
	ErrUnknownField      = errors.New("unknown profile field")
	ErrIdentityField     = errors.New("identity profile fields are user-set and cannot be changed by the agent")
	ErrUnknownPreference = errors.New("unknown profile preference")
)

var allowedPreferenceKeys = map[string]struct{}{
	"response_style": {},
	"units":          {},
	"language":       {},
}

var preferenceRenderOrder = []string{"response_style", "units", "language"}

type UserProfile struct {
	ID              string            `json:"id"`
	DisplayName     string            `json:"display_name,omitempty"`
	Email           string            `json:"email,omitempty"`
	Timezone        string            `json:"timezone,omitempty"`
	Locale          string            `json:"locale,omitempty"`
	RoleCategory    string            `json:"role_category,omitempty"`
	Specializations []string          `json:"specializations,omitempty"`
	Preferences     map[string]string `json:"preferences,omitempty"`
	About           string            `json:"about,omitempty"`
	CreatedAt       time.Time         `json:"created_at,omitempty"`
	UpdatedAt       time.Time         `json:"updated_at,omitempty"`
}

type UserStore interface {
	Get(ctx context.Context, id string) (*UserProfile, error)
	Upsert(ctx context.Context, profile *UserProfile) error
	SetFields(ctx context.Context, id string, fields map[string]any) (*UserProfile, error)
}

type UserProvider interface {
	CurrentUserID(ctx context.Context) (string, error)
}

type LocalUserProvider struct{}

func (LocalUserProvider) CurrentUserID(context.Context) (string, error) {
	return LocalUserID, nil
}

func AllowedPreferenceKeys() []string {
	keys := make([]string, 0, len(allowedPreferenceKeys))
	for key := range allowedPreferenceKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func Normalize(profile *UserProfile) (*UserProfile, error) {
	if profile == nil {
		return nil, errors.New("user profile is required")
	}
	cp := *profile
	cp.ID = strings.TrimSpace(cp.ID)
	if cp.ID == "" {
		cp.ID = LocalUserID
	}
	cp.DisplayName = strings.TrimSpace(cp.DisplayName)
	cp.Email = strings.TrimSpace(cp.Email)
	cp.Timezone = strings.TrimSpace(cp.Timezone)
	cp.Locale = strings.TrimSpace(cp.Locale)
	cp.RoleCategory = strings.TrimSpace(cp.RoleCategory)
	cp.About = strings.TrimSpace(cp.About)
	if err := ValidateFreeText(cp.About); err != nil {
		return nil, err
	}
	cp.Specializations = normalizeStringList(cp.Specializations)
	prefs, err := NormalizePreferences(cp.Preferences)
	if err != nil {
		return nil, err
	}
	cp.Preferences = prefs
	return &cp, nil
}

func NormalizePreferences(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for rawKey, rawValue := range in {
		key := strings.TrimSpace(rawKey)
		if _, ok := allowedPreferenceKeys[key]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownPreference, key)
		}
		value := strings.Join(strings.Fields(rawValue), " ")
		if value == "" {
			continue
		}
		if err := ValidateFreeText(value); err != nil {
			return nil, fmt.Errorf("preferences.%s: %w", key, err)
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func ValidateFreeText(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if len(value) > AboutMaxLen {
		return fmt.Errorf("profile text is capped at %d characters", AboutMaxLen)
	}
	return sensitive.RejectSecretLikeText(value)
}

func normalizeStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		value := strings.Join(strings.Fields(item), " ")
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func IsBehavioralField(field string) bool {
	field = strings.TrimSpace(field)
	if field == "about" {
		return true
	}
	if strings.HasPrefix(field, "preferences.") {
		_, ok := allowedPreferenceKeys[strings.TrimPrefix(field, "preferences.")]
		return ok
	}
	return false
}
