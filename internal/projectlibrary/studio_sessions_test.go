package projectlibrary

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestStudioSessions_ReviewedCatalogOnlyGoalAndAtomicRecapSurviveRestart(t *testing.T) {
	a, scope, _, file, tree, _ := activationFixture(t)
	s := a.library
	beforeSong := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	doc, err := s.Read(scope)
	if err != nil || len(doc.Sessions) != 0 {
		t.Fatalf("fixture: %+v %v", doc.Sessions, err)
	}
	input := GoalInput{Goal: "Listen to the rough mix", Outcome: "Choose one edit", TimeMinutes: 45, PlannedDate: "2026-09-28"}
	review, err := s.ReviewGoal(scope, "single", 1, input, "local")
	if err != nil || review.Token == "" || review.Session.State != "reviewed" {
		t.Fatalf("goal review: %+v %v", review, err)
	}
	if current, readErr := s.Read(scope); readErr != nil || len(current.Sessions) != 0 || current.Entries[0].Fields.Revision != 0 {
		t.Fatalf("review wrote a goal or edited notes: %+v %v", current, readErr)
	}
	goal, replay, err := s.CommitGoal(scope, "single", review.Token, "goal-key", 1, input, "local")
	if err != nil || replay || goal.ID != review.Session.ID || goal.State != "accepted" || goal.Recap != "" {
		t.Fatalf("accepted goal: %+v replay=%v err=%v", goal, replay, err)
	}
	if again, replay, err := s.CommitGoal(scope, "single", review.Token, "goal-key", 1, input, "local"); err != nil || !replay || again.ID != goal.ID {
		t.Fatalf("goal replay: %+v replay=%v err=%v", again, replay, err)
	}
	activity, err := s.Query(scope, Search{PageSize: 10, Sort: "sourced_activity"})
	if err != nil {
		t.Fatal(err)
	}
	foundActivity := false
	for _, row := range activity.Rows {
		if row.ID == "single" && row.ActivitySource == "reviewed_studio_session" && row.ActivityAt.Equal(goal.UpdatedAt) {
			foundActivity = true
		}
	}
	if !foundActivity {
		t.Fatalf("accepted user goal was mistaken for scan time or ignored: %+v", activity.Rows)
	}
	if _, _, err := s.CommitGoal(scope, "single", review.Token, "goal-key", 1, GoalInput{Goal: "Different"}, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("same key changed goal: %v", err)
	}
	recap := RecapInput{Recap: "I preferred the quieter intro", Decisions: []string{"Keep the bridge"},
		Blockers: []string{"Need vocals"}, ActualDate: "2026-09-28", Next: "Record a scratch vocal", UpdateNext: true}
	wrap, err := s.ReviewRecap(scope, goal.ID, goal.Revision, 0, recap, "local")
	if err != nil || wrap.Session.Next != recap.Next || wrap.Session.State != "reviewed" {
		t.Fatalf("recap review: %+v %v", wrap, err)
	}
	if current, readErr := s.Read(scope); readErr != nil || current.Entries[0].Fields.NextAction != "" || current.Sessions[0].Recap != "" {
		t.Fatalf("review committed recap early: %+v %v", current, readErr)
	}
	accepted, replay, err := s.CommitRecap(scope, goal.ID, wrap.Token, "recap-key", 1, 0, recap, "local")
	if err != nil || replay || accepted.Revision != 2 || accepted.Recap != recap.Recap || accepted.AcceptedAt == nil {
		t.Fatalf("accepted recap: %+v replay=%v err=%v", accepted, replay, err)
	}
	if again, replay, err := s.CommitRecap(scope, goal.ID, wrap.Token, "recap-key", 1, 0, recap, "local"); err != nil || !replay || again.ID != goal.ID {
		t.Fatalf("atomic recap replay after entry revision changed: %+v replay=%v err=%v", again, replay, err)
	}
	folder := filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID)))
	reopened, err := workspace.NewFileStore(folder)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := NewStore(reopened).Read(scope)
	if err != nil || len(persisted.Sessions) != 1 || persisted.Sessions[0].Next != recap.Next ||
		persisted.Entries[0].Fields.NextAction != recap.Next || persisted.Entries[0].Fields.Source != "reviewed_user" ||
		fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != beforeSong {
		t.Fatalf("restart/atomic Home record or source changed: %+v %v", persisted, err)
	}
	full, err := NewStore(reopened).GetSession(scope, "single", goal.ID)
	if err != nil || len(full.Decisions) != 1 || full.Decisions[0] != "Keep the bridge" || len(full.Blockers) != 1 {
		t.Fatalf("bounded full session detail after restart: %+v %v", full, err)
	}
	if _, err := NewStore(reopened).GetSession(scope, "alternates", goal.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign entry claimed another session: %v", err)
	}
	page, err := NewStore(reopened).ListSessions(scope, "single", 0, 0)
	if err != nil || page.Total != 1 || len(page.Rows) != 1 || page.Rows[0].ID != goal.ID || page.NextOffset != 0 {
		t.Fatalf("bounded resume read: %+v %v", page, err)
	}
	if _, err := NewStore(reopened).ListSessions(scope, "single", page.Revision-1, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale session page was accepted: %v", err)
	}
	resume, err := NewStore(reopened).Resume(scope)
	if err != nil || len(resume.Cards) != 1 || resume.Cards[0].EntryID != "single" ||
		resume.Cards[0].ProjectNextAction != recap.Next || resume.Cards[0].Session.Recap != recap.Recap {
		t.Fatalf("Home resume after restart: %+v %v", resume, err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.PluginAvailable = false
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	paused, err := workspace.NewFileStore(folder)
	if err != nil {
		t.Fatal(err)
	}
	if history, err := NewStore(paused).Resume(scope); err != nil || len(history.Cards) != 1 || history.Cards[0].Session.Recap != recap.Recap {
		t.Fatalf("provider loss erased user-authored resume: %+v %v", history, err)
	}
}

func TestStudioSessions_ResumeWorkspaceOnlyForCurrentReciprocalLink(t *testing.T) {
	a, scope, _, file, _, installed := activationFixture(t)
	s := a.library
	goalInput := GoalInput{Goal: "Return to this song"}
	review, err := s.ReviewGoal(scope, "single", 1, goalInput, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CommitGoal(scope, "single", review.Token, "goal-before-project", 1, goalInput, "local"); err != nil {
		t.Fatal(err)
	}
	activation := NewActivationService(a, realActivationCreator(t, scope, file, installed))
	doc, err := s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := activation.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	child, err := activation.Commit(t.Context(), scope, "single", setup.Token, "resume-linked-song")
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.Resume(scope)
	if err != nil || len(view.Cards) != 1 || view.Cards[0].WorkspaceID != child.WorkspaceID {
		t.Fatalf("current exact link not resumable: %+v %v", view, err)
	}
	if err := file.Update(child.WorkspaceID, func(project *workspace.Workspace) error {
		link := project.GetAssistantProjectLink()
		link.StationWorkspaceID = "other-home"
		project.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err = s.Resume(scope)
	if err != nil || len(view.Cards) != 1 || view.Cards[0].WorkspaceID != "" || view.Cards[0].Session.Goal != goalInput.Goal {
		t.Fatalf("broken link revealed child or lost user notes: %+v %v", view, err)
	}
}

func TestStudioSessions_RecapConflictsWithConcurrentProjectEditWithoutPartialWrite(t *testing.T) {
	a, scope, _, _, _, _ := activationFixture(t)
	s := a.library
	goalInput := GoalInput{Goal: "Review the arrangement"}
	review, err := s.ReviewGoal(scope, "single", 1, goalInput, "local")
	if err != nil {
		t.Fatal(err)
	}
	goal, _, err := s.CommitGoal(scope, "single", review.Token, "goal", 1, goalInput, "local")
	if err != nil {
		t.Fatal(err)
	}
	recap := RecapInput{Recap: "Recorded an idea, not a finished song", Next: "Edit the bridge", UpdateNext: true}
	wrap, err := s.ReviewRecap(scope, goal.ID, 1, 0, recap, "local")
	if err != nil {
		t.Fatal(err)
	}
	other := "Follow up on vocals"
	edit, err := s.ReviewFields(scope, "single", 0, FieldsPatch{NextAction: &other}, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitFields(scope, "single", edit.Token, "other-edit", 0, FieldsPatch{NextAction: &other}, "local"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitRecap(scope, goal.ID, wrap.Token, "stale-recap", 1, 0, recap, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale recap overwrote edit: %v", err)
	}
	current, err := s.Read(scope)
	if err != nil || current.Entries[0].Fields.NextAction != other || current.Sessions[0].Recap != "" {
		t.Fatalf("partial recap after conflict: %+v %v", current, err)
	}
	wrap, err = s.ReviewRecap(scope, goal.ID, 1, 1, recap, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CommitRecap(scope, goal.ID, wrap.Token, "fresh-recap", 1, 1, recap, "local"); err != nil {
		t.Fatalf("fresh reviewed recap: %v", err)
	}
}

func TestStudioSessions_WorstCasePageOmitsPrivateListsAndStaysUnderWireBudget(t *testing.T) {
	a, scope, _, _, _, _ := activationFixture(t)
	s := a.library
	doc, err := s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.mutate(scope, doc.Revision, operation{key: "six-session-fixture", action: "fixture_sessions", digest: "session-wire"}, func(current *Document) (string, error) {
		at := s.now().UTC()
		private := make([]string, 16)
		for i := range private {
			private[i] = strings.Repeat("<", 240)
		}
		for i := 0; i < 6; i++ {
			current.Sessions = append(current.Sessions, StudioSession{ID: fmt.Sprintf("session-%d", i), EntryID: "single", Revision: 1,
				Goal: strings.Repeat("<", 500), Outcome: strings.Repeat("<", 500), Recap: strings.Repeat("<", 2000),
				Next: strings.Repeat("<", 240), Decisions: private, Blockers: private,
				Author: strings.Repeat("<", 160), State: "accepted", UpdatedAt: at, AcceptedAt: &at})
		}
		return "session-0", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ListSessions(scope, "single", 0, 0)
	if err != nil || first.Total != 6 || len(first.Rows) != 5 || first.NextOffset != 5 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	wire, err := json.Marshal(first)
	if err != nil || len(wire) >= 128<<10 || strings.Contains(string(wire), `"decisions"`) || strings.Contains(string(wire), `"blockers"`) {
		t.Fatalf("unbounded/private session page: %d bytes %v", len(wire), err)
	}
	last, err := s.ListSessions(scope, "single", first.Revision, first.NextOffset)
	if err != nil || len(last.Rows) != 1 || last.NextOffset != 0 {
		t.Fatalf("final page: %+v %v", last, err)
	}
}

func TestStudioSessions_ReviewHistoryBeyondFormer128CapPreservesReplay(t *testing.T) {
	a, scope, _, _, _, _ := activationFixture(t)
	s := a.library
	var first SessionReview
	for i := 0; i < 65; i++ {
		input := GoalInput{Goal: fmt.Sprintf("Plan %d", i)}
		review, err := s.ReviewGoal(scope, "single", 1, input, "local")
		if err != nil {
			t.Fatalf("review %d: %v", i, err)
		}
		if i == 0 {
			first = review
		}
		if _, _, err := s.CommitGoal(scope, "single", review.Token, fmt.Sprintf("goal-%d", i), 1, input, "local"); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}
	doc, err := s.Read(scope)
	if err != nil || len(doc.Sessions) != 65 || len(doc.Reviews) < 65 {
		t.Fatalf("review/operation history lost at old cap: sessions=%d reviews=%d err=%v", len(doc.Sessions), len(doc.Reviews), err)
	}
	if record, replay, err := s.CommitGoal(scope, "single", first.Token, "goal-0", 1, GoalInput{Goal: "Plan 0"}, "local"); err != nil || !replay || record.ID != first.Session.ID {
		t.Fatalf("old reviewed consequence no longer replays: %+v replay=%v err=%v", record, replay, err)
	}
}

func TestStudioSessions_ProviderLossOwnerIsolationAndInvalidNotes(t *testing.T) {
	a, scope, _, file, _, _ := activationFixture(t)
	var available atomic.Bool
	available.Store(true)
	s := NewStore(file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return available.Load() })
	if _, err := NewStore(file).ReviewGoal(scope, "single", 1, GoalInput{Goal: "Bypass provider policy"}, "local"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider-unbound service wrote a session: %v", err)
	}
	bad := GoalInput{Goal: "unsafe\x00instructions"}
	if _, err := s.ReviewGoal(scope, "single", 1, bad, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("control character accepted: %v", err)
	}
	if _, err := s.ReviewGoal(Scope{OwnerUserID: "foreign", HomeID: scope.HomeID, ProviderID: scope.ProviderID, ProgramID: scope.ProgramID}, "single", 1, GoalInput{Goal: "Spy"}, "foreign"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign Home accepted: %v", err)
	}
	input := GoalInput{Goal: "Save this goal"}
	review, err := s.ReviewGoal(scope, "single", 1, input, "local")
	if err != nil {
		t.Fatal(err)
	}
	available.Store(false)
	if _, _, err := s.CommitGoal(scope, "single", review.Token, "provider-lost", 1, input, "local"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider loss still committed: %v", err)
	}
	if current, err := a.library.Read(scope); err != nil || len(current.Sessions) != 0 {
		t.Fatalf("provider loss created a session: %+v %v", current.Sessions, err)
	}
	if _, err := s.ReviewGoal(scope, "single", 1, GoalInput{Goal: strings.Repeat("a", 501)}, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("oversized goal accepted: %v", err)
	}
	if view, err := s.Resume(scope); err != nil || len(view.Cards) != 0 {
		t.Fatalf("provider-loss read was not inert: %+v %v", view, err)
	}
}
