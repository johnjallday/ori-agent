package userprofile

import (
	"context"
	"testing"
)

func TestPreferenceRevisionSurvivesUnrelatedChangesAndDetectsSameWordingReentry(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.Upsert(ctx, &UserProfile{ID: "local", Preferences: map[string]string{
		"response_style": "concise", "language": "English",
	}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.PreferenceRevision(ctx, "local", "response_style")
	if err != nil || before < 1 {
		t.Fatalf("initial generation: %d %v", before, err)
	}
	profile, err := store.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	profile.Preferences["language"] = "French"
	if err := store.Upsert(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if revision, err := store.PreferenceRevision(ctx, "local", "response_style"); err != nil || revision != before {
		t.Fatalf("unrelated full-profile edit changed the review generation: %d, %v", revision, err)
	}
	profile, err = store.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := store.UpdateFieldCAS(ctx, "local", "preferences.response_style", profile.UpdatedAt, "concise", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateFieldCAS(ctx, "local", "preferences.response_style", cleared.UpdatedAt, "", "concise"); err != nil {
		t.Fatal(err)
	}
	if revision, err := store.PreferenceRevision(ctx, "local", "response_style"); err != nil || revision != before+2 {
		t.Fatalf("identical re-entry reused an old review generation: %d -> %d, %v", before, revision, err)
	}
	profile, err = store.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	delete(profile.Preferences, "response_style")
	if err := store.Upsert(ctx, profile); err != nil {
		t.Fatal(err)
	}
	profile.Preferences["response_style"] = "concise"
	if err := store.Upsert(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if revision, err := store.PreferenceRevision(ctx, "local", "response_style"); err != nil || revision != before+4 {
		t.Fatalf("legacy whole-profile edit reused old review: %d -> %d, %v", before, revision, err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE users SET preferences = ? WHERE id = ?`, `{"response_style":"other","language":"French"}`, "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE users SET preferences = ? WHERE id = ?`, `{"response_style":"concise","language":"French"}`, "local"); err != nil {
		t.Fatal(err)
	}
	if revision, err := store.PreferenceRevision(ctx, "local", "response_style"); err != nil || revision != before+6 {
		t.Fatalf("outside SQL re-entry escaped the canonical field trigger: %d -> %d, %v", before, revision, err)
	}
	if _, err := store.PreferenceRevision(ctx, "foreign", "response_style"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PreferenceRevision(ctx, "local", "not_allowed"); err == nil {
		t.Fatal("arbitrary preference was versioned")
	}
}
