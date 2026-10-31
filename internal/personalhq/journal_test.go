package personalhq

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type fakeSnapshotBuilder struct {
	snap JournalSnapshot
	err  error
}

func (f fakeSnapshotBuilder) BuildJournalSnapshot(ctx context.Context, userID, localDate string) (JournalSnapshot, error) {
	return f.snap, f.err
}

type fakeNoteWriter struct{ notes []*session.WorkspaceNote }

func (f *fakeNoteWriter) CreateNote(ctx context.Context, note *session.WorkspaceNote) error {
	f.notes = append(f.notes, note)
	return nil
}

func TestBuildJournalDraftFromGroundedSnapshot(t *testing.T) {
	snap := JournalSnapshot{
		CompletedTasks:  []string{"Shipped the report"},
		FollowUpChanges: []string{"Closed: waiting on Dana"},
	}
	draft := BuildJournalDraft("2026-07-15", snap)
	for _, want := range []string{"End of day — 2026-07-15", "## Completed", "Shipped the report", "## Follow-ups", "Reflection"} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft missing %q:\n%s", want, draft)
		}
	}
}

func TestBuildJournalDraftEmptyIsReflectionPrompt(t *testing.T) {
	draft := BuildJournalDraft("2026-07-15", JournalSnapshot{})
	if strings.Contains(draft, "## Completed") {
		t.Fatal("empty snapshot must not fabricate sections")
	}
	if !strings.Contains(draft, "What went well today") {
		t.Fatalf("empty snapshot should offer a reflection prompt:\n%s", draft)
	}
}

func TestJournalProposeHasNoWriteSideEffect(t *testing.T) {
	svc, _, _ := newTestHarness(t)
	notes := &fakeNoteWriter{}
	j := NewJournalService(svc, fakeSnapshotBuilder{snap: JournalSnapshot{CompletedTasks: []string{"x"}}}, notes)

	p, err := j.Propose(context.Background(), "local", "2026-07-15")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Sections) == 0 || p.Draft == "" {
		t.Fatalf("expected a grounded proposal, got %+v", p)
	}
	if len(notes.notes) != 0 {
		t.Fatal("Propose must never write a note")
	}
}

func TestJournalProposeDegradesWhenBuilderFails(t *testing.T) {
	svc, _, _ := newTestHarness(t)
	j := NewJournalService(svc, fakeSnapshotBuilder{err: context.DeadlineExceeded}, &fakeNoteWriter{})
	p, err := j.Propose(context.Background(), "local", "2026-07-15")
	if err != nil {
		t.Fatalf("Propose should degrade, not error: %v", err)
	}
	if !p.Degraded || len(p.Gaps) == 0 {
		t.Fatalf("expected a degraded proposal with a named gap, got %+v", p)
	}
}

func TestJournalSaveCreatesDatedNoteNoMemory(t *testing.T) {
	svc, profiles, workspaces := newTestHarness(t)
	ctx := context.Background()
	if err := profiles.Upsert(ctx, &userprofile.UserProfile{ID: "local"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	ws := &session.Workspace{ID: "hq-1", Name: "HQ", Kind: session.WorkspaceKindWorkspace, OwnerUserID: "local", Status: session.WorkspaceStatusActive}
	if err := workspaces.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := svc.Designate(ctx, "local", "hq-1"); err != nil {
		t.Fatalf("designate: %v", err)
	}

	notes := &fakeNoteWriter{}
	j := NewJournalService(svc, nil, notes)
	note, err := j.Save(ctx, "local", "2026-07-15", "Today I shipped the thing.")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(notes.notes) != 1 {
		t.Fatalf("expected exactly one note written, got %d", len(notes.notes))
	}
	got := notes.notes[0]
	if got.WorkspaceID != "hq-1" || !strings.Contains(got.Name, "2026-07-15") {
		t.Fatalf("unexpected note: %+v", got)
	}
	if len(got.Tags) == 0 || got.Tags[0] != "journal" {
		t.Fatalf("journal note should be tagged 'journal', got %v", got.Tags)
	}
	_ = note
}

type fakeDailyMirror struct {
	result  DailyResult
	calls   []string
	content string
}

func (f *fakeDailyMirror) MirrorDaily(_ context.Context, userID, localDate, content string) DailyResult {
	f.calls = append(f.calls, userID+" "+localDate)
	f.content = content
	return f.result
}

// journalHQ gives the harness a valid Personal HQ to save into.
func journalHQ(t *testing.T) *Service {
	t.Helper()
	svc, profiles, workspaces := newTestHarness(t)
	ctx := context.Background()
	if err := profiles.Upsert(ctx, &userprofile.UserProfile{ID: "local"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	ws := &session.Workspace{ID: "hq-1", Name: "HQ", Kind: session.WorkspaceKindWorkspace, OwnerUserID: "local", Status: session.WorkspaceStatusActive}
	if err := workspaces.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := svc.Designate(ctx, "local", "hq-1"); err != nil {
		t.Fatalf("designate: %v", err)
	}
	return svc
}

func TestJournalSaveMirroredReportsWhatBecameOfTheDailyCopy(t *testing.T) {
	for _, result := range []DailyResult{"", DailyMirrored, DailyUserOwned, DailyFailed} {
		t.Run("result "+string(result), func(t *testing.T) {
			notes := &fakeNoteWriter{}
			mirror := &fakeDailyMirror{result: result}
			j := NewJournalService(journalHQ(t), nil, notes)
			j.SetDailyMirror(mirror)
			saved, err := j.SaveMirrored(context.Background(), "local", "2026-07-15", "  Today I shipped the thing. ")
			if err != nil {
				t.Fatalf("SaveMirrored: %v", err)
			}
			// The workspace note is the canonical save whatever the copy did.
			if len(notes.notes) != 1 || saved.Note == nil || saved.Note.ID != notes.notes[0].ID {
				t.Fatalf("notes = %d, saved = %+v", len(notes.notes), saved)
			}
			if saved.DailyNote != result || len(mirror.calls) != 1 || mirror.calls[0] != "local 2026-07-15" || mirror.content != "Today I shipped the thing." {
				t.Fatalf("daily = %q, calls = %v, content = %q", saved.DailyNote, mirror.calls, mirror.content)
			}
		})
	}
}

func TestJournalSaveWithoutAMirrorOrAfterAFailedSaveCopiesNothing(t *testing.T) {
	j := NewJournalService(journalHQ(t), nil, &fakeNoteWriter{})
	saved, err := j.SaveMirrored(context.Background(), "local", "2026-07-15", "Today.")
	if err != nil || saved.DailyNote != "" {
		t.Fatalf("no mirror: %+v, %v", saved, err)
	}

	mirror := &fakeDailyMirror{result: DailyMirrored}
	j.SetDailyMirror(mirror)
	if _, err := j.SaveMirrored(context.Background(), "local", "2026-07-15", "   "); err == nil {
		t.Fatal("empty content must be rejected")
	}
	if len(mirror.calls) != 0 {
		t.Fatalf("a rejected journal was copied: %v", mirror.calls)
	}
	// Save keeps its old shape and also copies.
	if _, err := j.Save(context.Background(), "local", "2026-07-15", "Today."); err != nil || len(mirror.calls) != 1 {
		t.Fatalf("Save: %v, calls = %v", err, mirror.calls)
	}
}

func TestJournalSaveRejectsEmptyContentAndNoHQ(t *testing.T) {
	svc, _, _ := newTestHarness(t)
	j := NewJournalService(svc, nil, &fakeNoteWriter{})
	if _, err := j.Save(context.Background(), "local", "2026-07-15", "   "); err == nil {
		t.Fatal("empty content must be rejected")
	}
	// No designated HQ → cannot save.
	if _, err := j.Save(context.Background(), "local", "2026-07-15", "content"); err == nil {
		t.Fatal("saving without a valid HQ must fail")
	}
}
