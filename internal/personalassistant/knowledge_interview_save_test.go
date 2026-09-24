package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func interviewSaveFixture(t *testing.T) (*knowledgeFixture, *KnowledgeInterviewService, int64) {
	t.Helper()
	f := newKnowledgeFixture(t)
	ctx := context.Background()
	if err := f.profileStore.Upsert(ctx, &userprofile.UserProfile{
		ID: "local", Preferences: map[string]string{"response_style": "brief", "language": "English"},
	}); err != nil {
		t.Fatal(err)
	}
	knowledge := NewKnowledgeStore(f.resolver(), f.folder)
	s := NewKnowledgeInterviewService(knowledge)
	s.SetCanonicalSavers(NewKnowledgeLifecycleService(knowledge, f.memory, nil), f.profileStore)
	binding, err := knowledge.resolve(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	return f, s, binding.StateVersion
}

func TestKnowledgeInterviewReviewedSaveUsesCanonicalHQAndProfileAndIsReplaySafe(t *testing.T) {
	ctx := context.Background()
	f, s, version := interviewSaveFixture(t)
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	request := KnowledgeInterviewSaveRequest{StateVersion: version, RequestID: "interview-save-1", Rows: []KnowledgeInterviewReviewedRow{
		{RowID: "priority", Category: "projects", Destination: "personal_hq", Text: "Finish my portfolio"},
		{RowID: "communication", Category: "how_you_work", Destination: "profile", Text: "concise", Preference: "response_style",
			ExpectedProfileValue: "brief", ExpectedProfileUpdatedAt: profile.UpdatedAt},
	}}
	result, err := s.SaveReviewed(ctx, "local", request)
	if err != nil || result.Status != KnowledgeInterviewCompleted || len(result.SavedRows) != 2 {
		t.Fatalf("reviewed interview save=%+v %v", result, err)
	}
	memory, err := f.memory.Read("hq-local")
	if err != nil || len(memory.Entries()) != 1 || memory.Entries()[0].Text != "Finish my portfolio" {
		t.Fatalf("HQ memory from interview=%+v %v", memory.Entries(), err)
	}
	profile, err = f.profileStore.Get(ctx, "local")
	if err != nil || profile.Preferences["response_style"] != "concise" || profile.Preferences["language"] != "English" {
		t.Fatalf("profile preference not scoped to one field: %+v %v", profile, err)
	}
	restarted := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	restarted.SetCanonicalSavers(NewKnowledgeLifecycleService(restarted.store, f.memory, nil), f.profileStore)
	replayed, err := restarted.SaveReviewed(ctx, "local", request)
	if err != nil || len(replayed.SavedRows) != 2 || replayed.Status != KnowledgeInterviewCompleted {
		t.Fatalf("lost-response retry=%+v %v", replayed, err)
	}
	memory, err = f.memory.Read("hq-local")
	if err != nil || len(memory.Entries()) != 1 {
		t.Fatalf("retry duplicated canonical fact: %+v %v", memory.Entries(), err)
	}
	changed := request
	changed.Rows = append([]KnowledgeInterviewReviewedRow(nil), request.Rows...)
	changed.Rows[0].Text = "Unreviewed replacement"
	if _, err := restarted.SaveReviewed(ctx, "local", changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("same retry key accepted changed final review: %v", err)
	}
}

func TestKnowledgeInterviewDoesNotAdoptOutsideMatchingProfileEdit(t *testing.T) {
	ctx := context.Background()
	f, s, version := interviewSaveFixture(t)
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	request := KnowledgeInterviewSaveRequest{StateVersion: version, RequestID: "outside-same-text", Rows: []KnowledgeInterviewReviewedRow{{
		RowID: "communication", Category: "how_you_work", Destination: "profile", Text: "concise", Preference: "response_style",
		ExpectedProfileValue: "brief", ExpectedProfileUpdatedAt: profile.UpdatedAt,
	}}}
	// Another editor saves exactly the proposed text after final review. That
	// is not proof that this interview wrote it or that its receipt may be minted.
	if _, err := f.profileStore.UpdateFieldCAS(ctx, "local", "preferences.response_style", profile.UpdatedAt, "brief", "concise"); err != nil {
		t.Fatal(err)
	}
	result, err := s.SaveReviewed(ctx, "local", request)
	if !errors.Is(err, ErrConflict) || len(result.SavedRows) != 0 || len(result.PendingRows) != 1 {
		t.Fatalf("outside edit incorrectly adopted: %+v %v", result, err)
	}
	status, err := s.Read(ctx, "local")
	if err != nil || status == nil || len(status.RowReceipts) != 0 {
		t.Fatalf("outside edit gained a saved interview receipt: %+v %v", status, err)
	}
}

func TestKnowledgeInterviewRejectsSecretsOverlongAndMalformedRowsBeforeAnySave(t *testing.T) {
	ctx := context.Background()
	for _, unsafe := range []string{"token sk-abcdefghijklmnopqrstuv", strings.Repeat("é", 251), "first\nsecond", "\x00bad"} {
		t.Run(strings.ReplaceAll(unsafe[:min(len(unsafe), 12)], "\n", "newline"), func(t *testing.T) {
			f, s, version := interviewSaveFixture(t)
			request := KnowledgeInterviewSaveRequest{StateVersion: version, RequestID: "invalid-before-save",
				Rows: []KnowledgeInterviewReviewedRow{
					{RowID: "priority", Destination: "personal_hq", Category: "projects", Text: "Valid first row"},
					{RowID: "communication", Destination: "personal_hq", Category: "how_you_work", Text: unsafe},
				}}
			if _, err := s.SaveReviewed(ctx, "local", request); !errors.Is(err, ErrValidation) {
				t.Fatalf("invalid second row accepted: %v", err)
			}
			memory, err := f.memory.Read("hq-local")
			if err != nil || len(memory.Entries()) != 0 {
				t.Fatalf("validation failed after first row saved: %+v %v", memory.Entries(), err)
			}
		})
	}
}

func TestKnowledgeInterviewAllSkippedCompletesWithoutCanonicalWrites(t *testing.T) {
	ctx := context.Background()
	f, s, version := interviewSaveFixture(t)
	before, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.SaveReviewed(ctx, "local", KnowledgeInterviewSaveRequest{
		StateVersion: version, RequestID: "all-skipped", Rows: nil,
	})
	if err != nil || result.Status != KnowledgeInterviewCompleted || len(result.SavedRows) != 0 {
		t.Fatalf("all-skipped explicit finish=%+v %v", result, err)
	}
	memory, err := f.memory.Read("hq-local")
	if err != nil || len(memory.Entries()) != 0 {
		t.Fatalf("skipped answer became memory: %+v %v", memory.Entries(), err)
	}
	after, err := f.profileStore.Get(ctx, "local")
	if err != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("skipped interview mutated profile: %+v %v", after, err)
	}
}

func TestKnowledgeInterviewPartialProfileConflictKeepsSavedHQRowWithoutClobber(t *testing.T) {
	ctx := context.Background()
	f, s, version := interviewSaveFixture(t)
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	request := KnowledgeInterviewSaveRequest{StateVersion: version, RequestID: "partial-save", Rows: []KnowledgeInterviewReviewedRow{
		{RowID: "priority", Category: "projects", Destination: "personal_hq", Text: "Finish the dossier"},
		{RowID: "communication", Category: "how_you_work", Destination: "profile", Text: "concise", Preference: "response_style",
			ExpectedProfileValue: "brief", ExpectedProfileUpdatedAt: profile.UpdatedAt},
	}}
	if _, err := f.profileStore.UpdateFieldCAS(ctx, "local", "preferences.response_style", profile.UpdatedAt, "brief", "detailed"); err != nil {
		t.Fatal(err)
	}
	partial, err := s.SaveReviewed(ctx, "local", request)
	if !errors.Is(err, ErrConflict) || len(partial.SavedRows) != 1 || partial.SavedRows[0] != "priority" || len(partial.PendingRows) != 1 {
		t.Fatalf("partial save lied about outcome: %+v %v", partial, err)
	}
	status, err := s.Read(ctx, "local")
	if err != nil || status.Status == KnowledgeInterviewCompleted || len(status.RowReceipts) != 1 {
		t.Fatalf("partial status not durable: %+v %v", status, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		partial, err = s.SaveReviewed(ctx, "local", request)
		if !errors.Is(err, ErrConflict) || len(partial.SavedRows) != 1 {
			t.Fatalf("retry after changed profile clobbered or duplicated: %+v %v", partial, err)
		}
	}
	profile, err = f.profileStore.Get(ctx, "local")
	if err != nil || profile.Preferences["response_style"] != "detailed" {
		t.Fatalf("outside profile edit lost: %+v %v", profile, err)
	}
	memory, err := f.memory.Read("hq-local")
	if err != nil || len(memory.Entries()) != 1 || memory.Entries()[0].Text != "Finish the dossier" {
		t.Fatalf("partial HQ save changed on retry: %+v %v", memory.Entries(), err)
	}
	// The user refreshes the changed preference and approves a new review.
	// The already saved HQ row is verified, adopted and never rewritten.
	fresh := request
	fresh.RequestID = "partial-save-new-review"
	fresh.ResetPartial = true
	fresh.Rows = append([]KnowledgeInterviewReviewedRow(nil), request.Rows...)
	fresh.Rows[1].ExpectedProfileValue = "detailed"
	fresh.Rows[1].ExpectedProfileUpdatedAt = profile.UpdatedAt
	restarted := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	restarted.SetCanonicalSavers(NewKnowledgeLifecycleService(restarted.store, f.memory, nil), f.profileStore)
	completed, err := restarted.SaveReviewed(ctx, "local", fresh)
	if err != nil || completed.Status != KnowledgeInterviewCompleted || len(completed.SavedRows) != 2 {
		t.Fatalf("new review could not finish partial save: %+v %v", completed, err)
	}
	memory, err = f.memory.Read("hq-local")
	if err != nil || len(memory.Entries()) != 1 {
		t.Fatalf("new review duplicated previous HQ fact: %+v %v", memory.Entries(), err)
	}
	bad := request
	bad.RequestID = "invalid-review"
	bad.Rows = []KnowledgeInterviewReviewedRow{{RowID: "priority", Category: "projects", Destination: "personal_hq", Text: strings.Repeat("x", 501)}}
	if _, err := s.SaveReviewed(ctx, "local", bad); !errors.Is(err, ErrValidation) {
		t.Fatalf("unsafe row accepted: %v", err)
	}
}
