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

// HQOnboardingState is the durable Personal HQ onboarding status for a user.
// It is stored independently from PersonalWorkspaceID (the designation
// itself) so that clearing or losing the designated workspace never resets
// the user's onboarding history back to unseen.
type HQOnboardingState string

const (
	// HQOnboardingUnseen is the default state for a profile that has never
	// been shown the guided first-launch Personal HQ experience.
	HQOnboardingUnseen HQOnboardingState = "unseen"
	// HQOnboardingInProgress marks a user who started the Build My HQ setup
	// flow but has not yet completed or skipped it.
	HQOnboardingInProgress HQOnboardingState = "in_progress"
	// HQOnboardingCompleted marks a user who finished HQ setup or explicitly
	// designated an existing workspace as their HQ.
	HQOnboardingCompleted HQOnboardingState = "completed"
	// HQOnboardingSkipped marks a user who deferred HQ setup. Product
	// features and progression tiers remain fully available in this state.
	HQOnboardingSkipped HQOnboardingState = "skipped"
)

// ParseHQOnboardingState validates an external (API) onboarding-state value.
// Unlike NormalizeHQOnboardingState, it rejects unknown input instead of
// silently defaulting, since callers here are explicit state-transition
// requests rather than tolerant reads of persisted data.
func ParseHQOnboardingState(value string) (HQOnboardingState, bool) {
	switch HQOnboardingState(strings.TrimSpace(value)) {
	case HQOnboardingUnseen:
		return HQOnboardingUnseen, true
	case HQOnboardingInProgress:
		return HQOnboardingInProgress, true
	case HQOnboardingCompleted:
		return HQOnboardingCompleted, true
	case HQOnboardingSkipped:
		return HQOnboardingSkipped, true
	default:
		return "", false
	}
}

// NormalizeHQOnboardingState defaults unknown or empty persisted values to
// HQOnboardingUnseen rather than failing, so a stray or pre-migration value
// never breaks a read.
func NormalizeHQOnboardingState(value string) HQOnboardingState {
	if state, ok := ParseHQOnboardingState(value); ok {
		return state
	}
	return HQOnboardingUnseen
}

// PersonalHQState is the read-time view of a user's Personal HQ designation
// and onboarding status, as persisted on the user profile row.
type PersonalHQState struct {
	PersonalWorkspaceID string            `json:"personal_workspace_id,omitempty"`
	OnboardingState     HQOnboardingState `json:"hq_onboarding_state"`
	OnboardingUpdatedAt time.Time         `json:"hq_onboarding_updated_at,omitempty"`
}

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
