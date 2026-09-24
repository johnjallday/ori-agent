package personalassistant

import (
	"context"
	"testing"
)

func TestConfirmedInterviewPreferenceRequiresCompletedReceiptAndCurrentCanonicalValue(t *testing.T) {
	ctx := context.Background()
	f, interview, version := interviewSaveFixture(t)
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	request := KnowledgeInterviewSaveRequest{
		StateVersion: version, RequestID: "preference-only", Rows: []KnowledgeInterviewReviewedRow{{
			RowID: "communication", Category: "how_you_work", Destination: "profile",
			Text: "concise", Preference: "response_style", ExpectedProfileValue: profile.Preferences["response_style"],
			ExpectedProfileUpdatedAt: profile.UpdatedAt,
		}},
	}
	if result, err := interview.SaveReviewed(ctx, "local", request); err != nil || result.Status != KnowledgeInterviewCompleted {
		t.Fatalf("reviewed preference save: %+v %v", result, err)
	}
	status, err := interview.Read(ctx, "local")
	if err != nil || status == nil || len(status.RowReceipts) != 1 {
		t.Fatalf("review receipt missing: %+v %v", status, err)
	}
	preferences, err := interview.ConfirmedProfilePreferences(ctx, "local")
	if err != nil || len(preferences) != 1 || preferences[0].Text != "concise" || !preferences[0].ReviewedAt.Equal(status.RowReceipts[0].SavedAt) {
		t.Fatalf("canonical confirmed preference did not use the actual review date: %+v %v", preferences, err)
	}
	current, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.profileStore.UpdateFieldCAS(ctx, "local", "preferences.language", current.UpdatedAt, "English", "Spanish"); err != nil {
		t.Fatal(err)
	}
	preferences, err = interview.ConfirmedProfilePreferences(ctx, "local")
	if err != nil || len(preferences) != 1 || !preferences[0].ReviewedAt.Equal(status.RowReceipts[0].SavedAt) {
		t.Fatalf("unrelated profile edit falsified the confirmation date: %+v %v", preferences, err)
	}
	current, err = f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.profileStore.UpdateFieldCAS(ctx, "local", "preferences.response_style", current.UpdatedAt, "concise", "verbose"); err != nil {
		t.Fatal(err)
	}
	preferences, err = interview.ConfirmedProfilePreferences(ctx, "local")
	if err != nil || len(preferences) != 0 {
		t.Fatalf("stale interview text appeared after outside profile edit: %+v %v", preferences, err)
	}
	current, err = f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := f.profileStore.UpdateFieldCAS(ctx, "local", "preferences.response_style", current.UpdatedAt, "verbose", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.profileStore.UpdateFieldCAS(ctx, "local", "preferences.response_style", cleared.UpdatedAt, "", "concise"); err != nil {
		t.Fatal(err)
	}
	restarted := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	restarted.SetCanonicalSavers(interview.learning, f.profileStore)
	preferences, err = restarted.ConfirmedProfilePreferences(ctx, "local")
	if err != nil || len(preferences) != 0 {
		t.Fatalf("same wording re-entry inherited its old confirmation date after restart: %+v %v", preferences, err)
	}
	state, err := f.relationships.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	state.Status = StatusPaused
	if _, err := f.relationships.UpdateState(ctx, state, state.StateVersion); err != nil {
		t.Fatal(err)
	}
	preferences, err = interview.ConfirmedProfilePreferences(ctx, "local")
	if err != nil || len(preferences) != 0 {
		t.Fatalf("paused preference appeared: %+v %v", preferences, err)
	}
}
