package userprofile

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProfileFieldCASPreservesUnrelatedFieldsAndRejectsStaleVersion(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.Upsert(ctx, &UserProfile{
		ID: "local", DisplayName: "Jules", RoleCategory: "developer",
		Preferences: map[string]string{"response_style": "brief", "language": "English"},
		About:       "Works on a music project.",
	}); err != nil {
		t.Fatal(err)
	}
	original, err := store.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := store.UpdateFieldCAS(ctx, "local", "preferences.response_style", original.UpdatedAt, "brief", "detailed")
	if err != nil || changed.Preferences["response_style"] != "detailed" || changed.Preferences["language"] != "English" || changed.DisplayName != "Jules" || changed.About != original.About {
		t.Fatalf("field CAS clobbered unrelated values: %+v, %v", changed, err)
	}
	if _, err := store.UpdateFieldCAS(ctx, "local", "about", original.UpdatedAt, original.About, "Overwrote newer profile"); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("stale profile version was allowed: %v", err)
	}
	if _, err := store.UpdateFieldCAS(ctx, "local", "preferences.response_style", changed.UpdatedAt, "stale value", "another value"); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("stale field content was allowed: %v", err)
	}
	cleared, err := store.UpdateFieldCAS(ctx, "local", "preferences.response_style", changed.UpdatedAt, "detailed", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cleared.Preferences["response_style"]; exists || cleared.Preferences["language"] != "English" {
		t.Fatalf("clearing one preference lost unrelated data: %+v", cleared)
	}
}

func TestProfileFieldCASAtUsesExactlyPreparedVersion(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.Upsert(ctx, &UserProfile{ID: "local", Preferences: map[string]string{"response_style": "brief"}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	planned := before.UpdatedAt.Add(7 * time.Nanosecond)
	if _, err := store.UpdateFieldCASAt(ctx, "local", "preferences.response_style", before.UpdatedAt, "brief", "concise", before.UpdatedAt); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("non-incrementing prepared version accepted: %v", err)
	}
	got, err := store.UpdateFieldCASAt(ctx, "local", "preferences.response_style", before.UpdatedAt, "brief", "concise", planned)
	if err != nil || !got.UpdatedAt.Equal(planned) || got.Preferences["response_style"] != "concise" {
		t.Fatalf("prepared SQL revision not exact: %+v %v", got, err)
	}
	if _, err := store.UpdateFieldCASAt(ctx, "local", "preferences.response_style", before.UpdatedAt, "brief", "concise", planned); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("prepared SQL replay duplicated the write: %v", err)
	}
}

func TestProfileFieldCASAllowlistAndIdentityClear(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.Upsert(ctx, &UserProfile{ID: "local", DisplayName: "Jules", About: "Known preference"}); err != nil {
		t.Fatal(err)
	}
	profile, err := store.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"preferences.hobby", "personal_workspace_id", "hq_onboarding_state", "specializations", "preferences."} {
		if _, err := store.UpdateFieldCAS(ctx, "local", field, profile.UpdatedAt, "", "unsafe"); err == nil {
			t.Fatalf("unapproved field %q changed", field)
		}
	}
	if _, err := store.UpdateFieldCAS(ctx, "local", "about", profile.UpdatedAt, profile.About, "token sk-abcdefghijklmnopqrstuv"); err == nil {
		t.Fatal("secret-looking value accepted")
	}
	cleared, err := store.UpdateFieldCAS(ctx, "local", "about", profile.UpdatedAt, "Known preference", "")
	if err != nil || cleared.About != "" || cleared.DisplayName != "Jules" {
		t.Fatalf("field clear=%+v err=%v", cleared, err)
	}
}
