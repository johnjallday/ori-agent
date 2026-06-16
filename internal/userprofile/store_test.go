package userprofile

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteStore(db)
}

func TestSQLiteStoreCRUDAndSetFields(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Upsert(ctx, &UserProfile{
		ID:              "local",
		DisplayName:     "Jules",
		Email:           "jules@example.com",
		Timezone:        "America/New_York",
		Locale:          "en-US",
		RoleCategory:    "developer",
		Specializations: []string{"Go", "SQLite"},
		Preferences:     map[string]string{"response_style": "concise"},
		About:           "Works on developer tools.",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.Get(ctx, "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DisplayName != "Jules" || got.Preferences["response_style"] != "concise" || len(got.Specializations) != 2 {
		t.Fatalf("profile did not round-trip: %#v", got)
	}

	got, err = store.SetFields(ctx, "local", map[string]any{
		"preferences.units": "metric",
		"about":             "Prefers direct implementation notes.",
	})
	if err != nil {
		t.Fatalf("SetFields: %v", err)
	}
	if got.Preferences["units"] != "metric" || got.About != "Prefers direct implementation notes." {
		t.Fatalf("behavioral fields were not updated: %#v", got)
	}
	if got.DisplayName != "Jules" {
		t.Fatalf("identity fields should be preserved, got %#v", got)
	}

	got, err = store.SetFields(ctx, "local", map[string]any{
		"preferences.units": nil,
		"about":             nil,
	})
	if err != nil {
		t.Fatalf("SetFields nil clears: %v", err)
	}
	if got.About != "" {
		t.Fatalf("nil about should clear the field, got %#v", got.About)
	}
	if _, ok := got.Preferences["units"]; ok {
		t.Fatalf("nil preference should clear the key, got %#v", got.Preferences)
	}
}

func TestSQLiteStoreRejectsUnknownIdentityAndSecretFields(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.SetFields(ctx, "local", map[string]any{"display_name": "Agent Set"}); !errors.Is(err, ErrIdentityField) {
		t.Fatalf("identity write should be rejected, got %v", err)
	}
	if _, err := store.SetFields(ctx, "local", map[string]any{"preferences.color": "blue"}); !errors.Is(err, ErrUnknownPreference) {
		t.Fatalf("unknown preference should be rejected, got %v", err)
	}
	if _, err := store.SetFields(ctx, "local", map[string]any{"about": "secret sk-abc1234567890"}); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("secret-looking about text should be rejected, got %v", err)
	}
}

func TestRenderUserProfileSection(t *testing.T) {
	out := RenderUserProfileSection(&UserProfile{
		DisplayName:     "Jules",
		Timezone:        "America/New_York",
		RoleCategory:    "developer",
		Specializations: []string{"Go", "SQLite"},
		Preferences: map[string]string{
			"response_style": "concise",
			"units":          "metric",
		},
		About: "Works on developer tools.",
	})

	for _, want := range []string{
		"## About You",
		"- Name: Jules",
		"- Timezone: America/New_York",
		"- Role: developer",
		"- Specializations: Go, SQLite",
		"- Response style: concise",
		"- Units: metric",
		"- About: Works on developer tools.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected render output to contain %q, got:\n%s", want, out)
		}
	}
	if RenderUserProfileSection(&UserProfile{}) != "" {
		t.Fatal("empty profile should render as an empty string")
	}
}

func TestLocalUserProvider(t *testing.T) {
	got, err := (LocalUserProvider{}).CurrentUserID(context.Background())
	if err != nil {
		t.Fatalf("CurrentUserID: %v", err)
	}
	if got != LocalUserID {
		t.Fatalf("expected local user id, got %q", got)
	}
}
