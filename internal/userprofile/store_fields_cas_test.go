package userprofile

import (
	"context"
	"errors"
	"testing"
)

func TestSetFieldsIfVersionCannotRestorePreferenceAfterConcurrentForget(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.Upsert(ctx, &UserProfile{ID: "local", Preferences: map[string]string{"response_style": "concise", "language": "English"}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateFieldCAS(ctx, "local", "preferences.response_style", before.UpdatedAt, "concise", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetFieldsIfVersion(ctx, "local", map[string]any{
		"preferences.response_style": "concise", "about": "A tool attempted an old write",
	}, before.UpdatedAt); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("stale agent write should conflict atomically: %v", err)
	}
	current, err := store.Get(ctx, "local")
	if err != nil || current.Preferences["response_style"] != "" || current.About != "" || current.Preferences["language"] != "English" {
		t.Fatalf("stale multi-field tool wrote through the Forget: %+v %v", current, err)
	}
	if _, err := store.SetFieldsIfVersion(ctx, "local", map[string]any{"about": "Safe new wording"}, current.UpdatedAt); err != nil {
		t.Fatalf("fresh tool update: %v", err)
	}
}
