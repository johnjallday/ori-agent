package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
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

func TestStudioSessions_SharedRecapRequiresLiveExactLinkAtReviewAndCommit(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	s := a.library
	original := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	goalInput := GoalInput{Goal: "Plan the bridge"}
	goalReview, err := s.ReviewGoal(scope, "single", 1, goalInput, "local")
	if err != nil {
		t.Fatal(err)
	}
	goal, _, err := s.CommitGoal(scope, "single", goalReview.Token, "goal-before-share", 1, goalInput, "local")
	if err != nil {
		t.Fatal(err)
	}
	input := RecapInput{Recap: "I chose a quieter bridge", ShareLinked: true}
	if _, err := s.ReviewRecap(scope, goal.ID, goal.Revision, 0, input, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("catalog-only entry shared a project recap: %v", err)
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
	child, err := activation.Commit(t.Context(), scope, "single", setup.Token, "connect-before-share")
	if err != nil {
		t.Fatal(err)
	}
	linked, err := file.Get(child.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	exact := linked.GetAssistantProjectLink()
	review, err := s.ReviewRecap(scope, goal.ID, goal.Revision, 0, input, "local")
	if err != nil || review.Session.SharedFrom == nil || review.Session.SharedFrom.WorkspaceID != child.WorkspaceID {
		t.Fatalf("exact child was not disclosed for owner review: %+v %v", review, err)
	}
	if snapshot, err := s.Read(scope); err != nil || snapshot.Sessions[0].Recap != "" || snapshot.Sessions[0].SharedFrom != nil {
		t.Fatalf("review exposed an unconfirmed summary: %+v %v", snapshot.Sessions, err)
	}
	if err := file.Update(child.WorkspaceID, func(project *workspace.Workspace) error {
		broken := project.GetAssistantProjectLink()
		broken.StationWorkspaceID = "other-home"
		project.SetAssistantProjectLink(broken)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitRecap(scope, goal.ID, review.Token, "share-after-disconnect", 1, 0, input, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("disconnected project shared a recap: %v", err)
	}
	// Test-only restoration from the exact unchanged link; there is no product
	// adoption or repaired child authority in this path.
	if err := file.Update(child.WorkspaceID, func(project *workspace.Workspace) error {
		project.SetAssistantProjectLink(exact)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	review, err = s.ReviewRecap(scope, goal.ID, goal.Revision, 0, input, "local")
	if err != nil {
		t.Fatal(err)
	}
	accepted, replay, err := s.CommitRecap(scope, goal.ID, review.Token, "confirmed-linked-share", 1, 0, input, "local")
	if err != nil || replay || accepted.SharedFrom == nil || accepted.SharedFrom.LinkID != exact.ID || accepted.Recap != input.Recap {
		t.Fatalf("separate confirmed share: %+v replay=%v err=%v", accepted, replay, err)
	}
	if err := file.Update(child.WorkspaceID, func(project *workspace.Workspace) error {
		broken := project.GetAssistantProjectLink()
		broken.StationWorkspaceID = "other-home"
		project.SetAssistantProjectLink(broken)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if previous, replay, err := s.CommitRecap(scope, goal.ID, review.Token, "confirmed-linked-share", 1, 0, input, "local"); err != nil || !replay || previous.SharedFrom == nil {
		t.Fatalf("accepted receipt cannot be read after disconnect: %+v replay=%v err=%v", previous, replay, err)
	}
	resumed, err := s.Resume(scope)
	if err != nil || len(resumed.Cards) != 1 || resumed.Cards[0].WorkspaceID != "" || resumed.Cards[0].Session.SharedFrom == nil ||
		fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != original {
		t.Fatalf("historical share leaked link authority or changed song: %+v %v", resumed, err)
	}
}

func TestStudioSessions_CitesOnlyExactHomeHandoffReceiptAfterOwnerReview(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	s := a.library
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	goalInput := GoalInput{Goal: "Plan a vocal take"}
	goalReview, err := s.ReviewGoal(scope, "single", 1, goalInput, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	goal, _, err := s.CommitGoal(scope, "single", goalReview.Token, "goal-with-handoff", 1, goalInput, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	input := RecapInput{Recap: "Planned a take", ShareLinked: true, HandoffTicketID: "home-ticket"}
	if _, err := s.ReviewRecap(scope, goal.ID, 1, 0, input, scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("unlinked catalog entry cited a child Ticket: %v", err)
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
	child, err := activation.Commit(t.Context(), scope, "single", setup.Token, "connect-before-handoff")
	if err != nil {
		t.Fatal(err)
	}
	linked, err := file.Get(child.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	link := linked.GetAssistantProjectLink()
	at := s.now().UTC()
	receipt := workspace.AssistantPortfolioHandoffOperationReceipt{LinkID: link.ID, ProjectWorkspaceID: child.WorkspaceID,
		TicketID: "home-ticket", TicketNumber: 12, RecordedAt: at, IdempotencyKey: "old-confirmed-handoff", InputDigest: "private"}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Portfolio.HandoffOperationReceipts = append(state.Portfolio.HandoffOperationReceipts, receipt)
		for i, delta := range []time.Duration{-time.Minute, -2 * time.Minute, -time.Hour} {
			state.Portfolio.HandoffOperationReceipts = append(state.Portfolio.HandoffOperationReceipts,
				workspace.AssistantPortfolioHandoffOperationReceipt{LinkID: link.ID, ProjectWorkspaceID: child.WorkspaceID,
					TicketID: fmt.Sprintf("older-ticket-%d", i), RecordedAt: at.Add(delta)})
		}
		state.Portfolio.HandoffOperationReceipts = append(state.Portfolio.HandoffOperationReceipts,
			workspace.AssistantPortfolioHandoffOperationReceipt{LinkID: "another-link", ProjectWorkspaceID: child.WorkspaceID,
				TicketID: "foreign-ticket", RecordedAt: at})
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if choices, err := s.HandoffsForOwner(scope, "single"); err != nil || choices.Total != 4 || len(choices.Rows) != 3 ||
		choices.Rows[0].TicketID != "home-ticket" || choices.Rows[0].TicketNumber != 12 {
		t.Fatalf("Home-only receipt choices: %+v %v", choices, err)
	}
	for _, denied := range []RecapInput{
		{Recap: "Claim", HandoffTicketID: "home-ticket"},
		{Recap: "Claim", ShareLinked: true, HandoffTicketID: "foreign-ticket"},
		{Recap: "Claim", ShareLinked: true, HandoffTicketID: "older-ticket-2"}, // Not in the three reviewable choices.
		{Recap: "Claim", ShareLinked: true, HandoffTicketID: "invented-ticket"},
	} {
		if _, err := s.ReviewRecap(scope, goal.ID, 1, 0, denied, scope.OwnerUserID); !errors.Is(err, ErrConflict) {
			t.Fatalf("unverified citation passed review: %+v %v", denied, err)
		}
	}
	review, err := s.ReviewRecap(scope, goal.ID, 1, 0, input, scope.OwnerUserID)
	if err != nil || review.Session.Handoff == nil || review.Session.Handoff.TicketNumber != 12 ||
		review.Session.SharedFrom == nil || review.Session.SharedFrom.WorkspaceID != child.WorkspaceID {
		t.Fatalf("review did not disclose exact Home citation: %+v %v", review, err)
	}
	if doc, err := s.Read(scope); err != nil || doc.Sessions[0].Handoff != nil {
		t.Fatalf("review inserted a citation without confirmation: %+v %v", doc.Sessions, err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Portfolio.HandoffOperationReceipts[0].TicketNumber = 13
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitRecap(scope, goal.ID, review.Token, "changed-handoff", 1, 0, input, scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed Home receipt passed final review: %v", err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Portfolio.HandoffOperationReceipts[0].TicketNumber = 12 // test-only restore of unchanged evidence
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	accepted, replay, err := s.CommitRecap(scope, goal.ID, review.Token, "cite-confirmed", 1, 0, input, scope.OwnerUserID)
	if err != nil || replay || accepted.Handoff == nil || accepted.Handoff.TicketID != "home-ticket" ||
		accepted.Handoff.TicketNumber != 12 || accepted.Handoff.RecordedAt.IsZero() {
		t.Fatalf("confirmed Home-only citation: %+v replay=%v err=%v", accepted, replay, err)
	}
	if err := file.Update(child.WorkspaceID, func(project *workspace.Workspace) error {
		broken := project.GetAssistantProjectLink()
		broken.StationWorkspaceID = "other-home"
		project.SetAssistantProjectLink(broken)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandoffsForOwner(scope, "single"); !errors.Is(err, ErrConflict) {
		t.Fatalf("disconnected child exposed new citation choices: %v", err)
	}
	if _, err := s.ReviewRecap(scope, goal.ID, accepted.Revision, 0, input, scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("disconnected child accepted a new citation: %v", err)
	}
	if again, replay, err := s.CommitRecap(scope, goal.ID, review.Token, "cite-confirmed", 1, 0, input, scope.OwnerUserID); err != nil || !replay || again.Handoff == nil {
		t.Fatalf("historical citation replay after disconnect: %+v replay=%v err=%v", again, replay, err)
	}
	folder := filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID)))
	reopened, err := workspace.NewFileStore(folder)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := NewStore(reopened).GetSession(scope, "single", goal.ID)
	if err != nil || persisted.Handoff == nil || persisted.Handoff.TicketID != "home-ticket" ||
		fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("historical citation lost or source modified: %+v %v", persisted, err)
	}
}

// disconnectAfterRecapCheckStore injects a child unlink after the Home update
// callback (including CommitRecap's final link verification) succeeds, but
// before the Home document is saved. This is a test seam, not a cross-store
// lock or an implementation of unlink reconciliation.
type disconnectAfterRecapCheckStore struct {
	workspace.Store
	homeID     string
	disconnect func() error
	fired      bool
}

func (s *disconnectAfterRecapCheckStore) Update(id string, fn func(*workspace.Workspace) error) error {
	if id != s.homeID {
		return s.Store.Update(id, fn)
	}
	return s.Store.Update(id, func(home *workspace.Workspace) error {
		if err := fn(home); err != nil {
			return err
		}
		if s.fired {
			return nil
		}
		s.fired = true
		return s.disconnect()
	})
}

func TestStudioSessions_ChildUnlinkAfterFinalHomeCheckPreservesHistoricalRecapButNotNavigation(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	s := a.library
	original := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	goalInput := GoalInput{Goal: "Review the exact project"}
	goalReview, err := s.ReviewGoal(scope, "single", 1, goalInput, "local")
	if err != nil {
		t.Fatal(err)
	}
	goal, _, err := s.CommitGoal(scope, "single", goalReview.Token, "goal-before-unlink", 1, goalInput, "local")
	if err != nil {
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
	child, err := activation.Commit(t.Context(), scope, "single", setup.Token, "child-before-unlink")
	if err != nil {
		t.Fatal(err)
	}
	input := RecapInput{Recap: "I wrote a note about the bridge", ShareLinked: true}
	review, err := s.ReviewRecap(scope, goal.ID, goal.Revision, 0, input, "local")
	if err != nil || review.Session.SharedFrom == nil {
		t.Fatalf("review must pin exact child before simulated disconnect: %+v %v", review, err)
	}
	injected := &disconnectAfterRecapCheckStore{Store: file, homeID: scope.HomeID}
	injected.disconnect = func() error {
		return file.Update(child.WorkspaceID, func(project *workspace.Workspace) error {
			link := project.GetAssistantProjectLink()
			link.StationWorkspaceID = "other-home"
			project.SetAssistantProjectLink(link)
			return nil
		})
	}
	final := NewStore(injected).WithProviderEvidence(s.providerEvidence)
	accepted, replay, err := final.CommitRecap(scope, goal.ID, review.Token, "unlink-after-check", 1, 0, input, "local")
	if err != nil || replay || !injected.fired || accepted.SharedFrom == nil ||
		accepted.SharedFrom.WorkspaceID != child.WorkspaceID {
		t.Fatalf("interleaved commit lost the owner note or did not exercise the gap: %+v replay=%v fired=%v err=%v", accepted, replay, injected.fired, err)
	}
	// The final verification observed a connected child; another child write
	// completed before the Home save. An accepted owner-written attribution is
	// historical, not a live grant to navigate or publish more child context.
	restarted, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	history := NewStore(restarted)
	view, err := history.Resume(scope)
	if err != nil || len(view.Cards) != 1 || view.Cards[0].WorkspaceID != "" ||
		view.Cards[0].Session.SharedFrom == nil || view.Cards[0].Session.Recap != input.Recap {
		t.Fatalf("restart implied a live link or dropped the accepted note: %+v %v", view, err)
	}
	if _, err := final.ReviewRecap(scope, goal.ID, accepted.Revision, 0, input, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("disconnected child permitted another attributed review: %v", err)
	}
	if previous, replay, err := final.CommitRecap(scope, goal.ID, review.Token, "unlink-after-check", 1, 0, input, "local"); err != nil || !replay || previous.SharedFrom == nil {
		t.Fatalf("accepted historical receipt could not be reread: %+v replay=%v err=%v", previous, replay, err)
	}
	if saved, err := history.Read(scope); err != nil || len(saved.Sessions) != 1 ||
		fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != original {
		t.Fatalf("race duplicated a session or modified source: %+v %v", saved.Sessions, err)
	}
}

func TestStudioSessions_LinkedRecapProviderLossAtCommitThenRestartPreservesOnlyAcceptedHistory(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	s := a.library
	original := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	goalInput := GoalInput{Goal: "Plan a small change"}
	goalReview, err := s.ReviewGoal(scope, "single", 1, goalInput, "local")
	if err != nil {
		t.Fatal(err)
	}
	goal, _, err := s.CommitGoal(scope, "single", goalReview.Token, "goal-before-provider-loss", 1, goalInput, "local")
	if err != nil {
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
	child, err := activation.Commit(t.Context(), scope, "single", setup.Token, "child-before-provider-loss")
	if err != nil {
		t.Fatal(err)
	}
	input := RecapInput{Recap: "A user-entered arrangement note", ShareLinked: true}
	review, err := s.ReviewRecap(scope, goal.ID, goal.Revision, 0, input, "local")
	if err != nil || review.Session.SharedFrom == nil {
		t.Fatalf("linked review before provider loss: %+v %v", review, err)
	}
	setProviderAvailable := func(available bool) {
		t.Helper()
		if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
			state := home.GetAssistantProgramState()
			state.PluginAvailable = available
			home.SetAssistantProgramState(state)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	setProviderAvailable(false)
	if _, _, err := s.CommitRecap(scope, goal.ID, review.Token, "share-after-provider-loss", 1, 0, input, "local"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled Home accepted a linked recap: %v", err)
	}
	if _, err := s.ReviewRecap(scope, goal.ID, goal.Revision, 0, input, "local"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled Home minted another linked recap review: %v", err)
	}
	if before, err := s.Read(scope); err != nil || len(before.Sessions) != 1 || before.Sessions[0].SharedFrom != nil || before.Sessions[0].Recap != "" {
		t.Fatalf("provider loss left a partial attributed note: %+v %v", before.Sessions, err)
	}
	setProviderAvailable(true) // Same installation in this disposable fixture, not a replacement pin.
	accepted, replay, err := s.CommitRecap(scope, goal.ID, review.Token, "share-after-provider-loss", 1, 0, input, "local")
	if err != nil || replay || accepted.SharedFrom == nil || accepted.SharedFrom.WorkspaceID != child.WorkspaceID {
		t.Fatalf("same current review could not be confirmed after re-enable: %+v replay=%v err=%v", accepted, replay, err)
	}
	setProviderAvailable(false)
	reopened, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	history := NewStore(reopened)
	view, err := history.Resume(scope)
	if err != nil || len(view.Cards) != 1 || view.Cards[0].WorkspaceID != child.WorkspaceID ||
		view.Cards[0].Session.SharedFrom == nil || view.Cards[0].Session.Recap != input.Recap {
		t.Fatalf("provider disable/restart erased accepted user attribution: %+v %v", view, err)
	}
	// The linked child can be independently navigable even while the Home is
	// read-only; disconnect, not provider status alone, removes link authority.
	if err := reopened.Update(child.WorkspaceID, func(project *workspace.Workspace) error {
		link := project.GetAssistantProjectLink()
		link.StationWorkspaceID = "other-home"
		project.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err = history.Resume(scope)
	if err != nil || len(view.Cards) != 1 || view.Cards[0].WorkspaceID != "" ||
		view.Cards[0].Session.SharedFrom == nil || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != original {
		t.Fatalf("disconnected provider-disabled Home leaked live child or lost notes: %+v %v", view, err)
	}
}

func TestStudioSessions_SharedRecapRefusesSplitChildRoleMirrorAtReviewAndCommit(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	original := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	creator := NewActivationService(a, realActivationCreator(t, scope, file, installed))
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := creator.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	child, err := creator.Commit(t.Context(), scope, "single", setup.Token, "split-recap-child")
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), &database.Config{Path: filepath.Join(t.TempDir(), "ori.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	primary := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10))
	for _, id := range []string{scope.HomeID, child.WorkspaceID} {
		item, getErr := file.Get(id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if err := primary.Save(item); err != nil {
			t.Fatal(err)
		}
	}
	library := NewStore(workspace.NewSyncStore(primary, file)).WithProviderEvidence(
		func(_ Scope, _ *workspace.Workspace) bool { return true })
	linkedDoc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	entryRevision := sessionEntry(linkedDoc, "single").Revision
	goalInput := GoalInput{Goal: "Review the exact link"}
	goalReview, err := library.ReviewGoal(scope, "single", entryRevision, goalInput, "local")
	if err != nil {
		t.Fatal(err)
	}
	goal, _, err := library.CommitGoal(scope, "single", goalReview.Token, "goal-before-split", entryRevision, goalInput, "local")
	if err != nil {
		t.Fatal(err)
	}
	input := RecapInput{Recap: "A user-written note", ShareLinked: true}
	exact, err := file.Get(child.WorkspaceID)
	if err != nil || len(exact.GetAssistantProjectLink().ProjectRoles) == 0 {
		t.Fatalf("expected saved split-child role snapshot: %v", err)
	}
	// Diverge only the folder's project-role declaration. Link ID, Home,
	// revision and provider remain identical, so a shallow mirror test misses it.
	breakFolder := func() {
		t.Helper()
		if err := file.Update(child.WorkspaceID, func(project *workspace.Workspace) error {
			link := project.GetAssistantProjectLink()
			link.ProjectRoles = nil
			project.SetAssistantProjectLink(link)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	breakFolder()
	if _, err := library.ReviewRecap(scope, goal.ID, goal.Revision, 0, input, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("split role mirror permitted a new attributed review: %v", err)
	}
	// Test-only exact restoration (no version bump) so both mirrors agree again.
	if err := file.RestoreMirrorRecord(exact); err != nil {
		t.Fatal(err)
	}
	review, err := library.ReviewRecap(scope, goal.ID, goal.Revision, 0, input, "local")
	if err != nil || review.Session.SharedFrom == nil {
		t.Fatalf("restored exact child cannot be reviewed: %+v %v", review, err)
	}
	primaryChild, err := primary.Get(child.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	primaryLink := primaryChild.GetAssistantProjectLink()
	primaryLink.ProjectRoles = nil
	primaryChild.SetAssistantProjectLink(primaryLink)
	if err := primary.Save(primaryChild); err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitRecap(scope, goal.ID, review.Token, "split-child-recap", 1, 0, input, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("primary-only role change permitted a new attributed recap: %v", err)
	}
	// Restore only the exact unchanged child in this disposable test; there is
	// no product-selected winner or automatic role repair.
	if err := primary.Save(exact); err != nil {
		t.Fatal(err)
	}
	breakFolder()
	if _, _, err := library.CommitRecap(scope, goal.ID, review.Token, "split-child-recap", 1, 0, input, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("folder-only role change permitted a new attributed recap: %v", err)
	}
	unchanged, err := library.Read(scope)
	if err != nil || len(unchanged.Sessions) != 1 || unchanged.Sessions[0].Recap != "" || unchanged.Sessions[0].SharedFrom != nil {
		t.Fatalf("refused recap changed the Home: %+v %v", unchanged.Sessions, err)
	}
	resume, err := library.Resume(scope)
	if err != nil || len(resume.Cards) != 1 || resume.Cards[0].WorkspaceID != "" || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != original {
		t.Fatalf("split child implied a live workspace or changed source: %+v %v", resume, err)
	}
	// Exact test-only restoration of the folder mirror at the primary's version.
	if err := file.RestoreMirrorRecord(exact); err != nil {
		t.Fatal(err)
	}
	accepted, replay, err := library.CommitRecap(scope, goal.ID, review.Token, "split-child-recap", 1, 0, input, "local")
	if err != nil || replay || accepted.SharedFrom == nil || accepted.SharedFrom.WorkspaceID != child.WorkspaceID {
		t.Fatalf("same review did not recover after exact test-only restoration: %+v replay=%v err=%v", accepted, replay, err)
	}
	breakFolder()
	resume, err = library.Resume(scope)
	if err != nil || len(resume.Cards) != 1 || resume.Cards[0].WorkspaceID != "" || resume.Cards[0].Session.SharedFrom == nil {
		t.Fatalf("historical share did not survive later mirror divergence: %+v %v", resume, err)
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
	linkedChild, childErr := file.Get(child.WorkspaceID)
	if err != nil || childErr != nil || len(view.Cards) != 1 || view.Cards[0].WorkspaceID != child.WorkspaceID ||
		view.Cards[0].Name != linkedChild.Name {
		t.Fatalf("current exact link not resumable by child name: %+v %v (child %v)", view, err, childErr)
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
