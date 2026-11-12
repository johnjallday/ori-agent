package learningmissions

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type resetScopeSource struct{ *reviewSource }

func (s resetScopeSource) LearningScope(context.Context, string, string) (string, error) {
	return s.proof.Ref.Incarnation, nil
}

func TestLearningResetScopesKeepWorkAndSeparateConsentFromEarnedHistory(t *testing.T) {
	s, _, i := reviewFixture(t)
	prepared := prepareReview(t, s, i)
	if _, err := s.ReviewResult(t.Context(), "local", i.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "consume", ExpectedRevision: 3}, InstanceRevision: 3, Action: "submit", ReviewID: prepared.Review.ID, Decision: "reject", Reason: "incorrect"}); err != nil {
		t.Fatal(err)
	}
	s.source.(*reviewSource).proof.InputText = "reviewed input"
	s.source = resetScopeSource{s.source.(*reviewSource)}
	owner, _ := s.store.Owner(t.Context(), "local")
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("scope", owner.Revision)); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Status(t.Context(), "local")
	if !before.Owner.Preferences.Observe || before.Competencies.Items[0].Attempts != 1 {
		t.Fatal("missing fixture grant/history")
	}
	if _, err := s.ClearPreferences(t.Context(), "local", MutationRequest{OperationID: "clear-consent", ExpectedRevision: before.Owner.Revision}); err != nil {
		t.Fatal(err)
	}
	cleared, _ := s.Status(t.Context(), "local")
	if cleared.Owner.Preferences.Observe || len(cleared.Owner.Preferences.ScopeBindings) != 0 || cleared.Owner.Preferences.SuggestionRevision != 0 || cleared.Instances[0].State != Completed || !reflect.DeepEqual(cleared.Competencies, before.Competencies) {
		t.Fatal("preference clear erased earned history or retained consent", cleared)
	}
	if _, err := s.EvidenceView(t.Context(), "local", cleared.Competencies.Items[0].EvidenceKeys[0]); err != nil {
		t.Fatal("preference revocation erased already-earned inspection", err)
	}
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("fresh-scope", cleared.Owner.Revision)); err != nil {
		t.Fatal(err)
	}
	reapproved, _ := s.Status(t.Context(), "local")
	if reapproved.Owner.Preferences.ObservationRevision <= before.Owner.Preferences.ObservationRevision {
		t.Fatal("reapproval revived the old consent identity")
	}
	if _, err := s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear-history", ExpectedRevision: reapproved.Owner.Revision}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Status(t.Context(), "local")
	if len(after.Instances) != 0 || after.Competencies.Items[0].Attempts != 0 || !reflect.DeepEqual(after.Owner.Preferences, reapproved.Owner.Preferences) || after.Owner.Epoch <= reapproved.Owner.Epoch {
		t.Fatal("history reset crossed preference/work boundary", after)
	}
	// The separate canonical result fixture was not mutated or cancelled.
	proof, err := s.source.(ResultSource).Result(t.Context(), "local", i)
	if err != nil || proof.Ref.RunID != i.Progress.RunID || proof.Status != "succeeded" {
		t.Fatal("learning clear changed actual canonical work", err)
	}
}

func TestFullAppRecordResetClearsAllLearningDomainsAndCannotReviveOldCallbacks(t *testing.T) {
	s, _, i := freshReviewFixture(t)
	requireFreshlyInitialized(t, s.store.db)
	prepared := prepareReview(t, s, i)
	if _, err := s.ReviewResult(t.Context(), "local", i.ID, ReviewRequest{MutationRequest: MutationRequest{OperationID: "consume", ExpectedRevision: 3}, InstanceRevision: 3, Action: "submit", ReviewID: prepared.Review.ID, Decision: "reject", Reason: "incorrect"}); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	practice, _ := s.store.Competencies(t.Context(), "local")
	key := practice.Items[0].EvidenceKeys[0]
	var sequence int
	var name, path string
	if err := s.store.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil || name != "main" {
		t.Fatal(err)
	}
	if err := s.gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	deleted, err := database.ResetAppRecords(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"learning_owners", "learning_sources", "learning_instances", "learning_operations", "learning_reviews", "learning_evidence"} {
		if deleted[table] == 0 {
			t.Fatal("domain was not seeded and explicitly reset", table)
		}
	}
	db, err := database.Open(t.Context(), &database.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fenced := NewService(NewSQLiteStore(db), s.gate, s.source)
	if _, err := fenced.Propose(t.Context(), "local", ProposalRequest{MutationRequest: MutationRequest{OperationID: "late", ExpectedRevision: 0}, WorkspaceID: "workspace"}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal("old runtime reopened admission after reset", err)
	}
	if _, err := fenced.store.Change(t.Context(), "local", owner.Revision, owner.Epoch, false, func(*Mutation) error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatal("old callback crossed deletion identity", err)
	}
	fresh := NewService(fenced.store, &resetstate.WorkGate{}, s.source)
	status, err := fresh.Status(t.Context(), "local")
	if err != nil || status.Owner.Revision != 0 || status.Owner.Epoch != 0 || len(status.Instances) != 0 || status.Competencies.Items[0].Attempts != 0 || status.Owner.Preferences.Observe {
		t.Fatal("fresh process adopted cleared history/consent", status, err)
	}
	if _, err := fresh.EvidenceView(t.Context(), "local", key); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing evidence reconstructed", err)
	}
}

func TestResetFenceRefusesAnInFlightDesignWithoutCancellingItsOwner(t *testing.T) {
	s, _ := testService(t)
	d, req := installDesigner(s)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	d.generate = func(ctx context.Context, snapshot ContextSnapshot) (GenerationReply, error) {
		close(entered)
		<-release
		if err := ctx.Err(); err != nil {
			return GenerationReply{}, err
		}
		return designReply(snapshot), nil
	}
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); _, err := s.Propose(context.Background(), "local", req); done <- err }()
	t.Cleanup(func() { unblock(); <-finished })
	<-entered
	if err := s.gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		unblock()
		<-done
		t.Fatal("reset was allowed over an admitted provider effect", err)
	}
	if s.gate.Snapshot().Fenced {
		unblock()
		<-done
		t.Fatal("busy reset cancelled/closed ordinary admission")
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal("refused reset cancelled provider work", err)
	}
	if err := s.gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Reconcile(t.Context(), "local", ""); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal("late event crossed successful reset fence", err)
	}
	if _, err := s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "late-clear", ExpectedRevision: 2}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal("late mutation crossed successful reset fence", err)
	}
	if d.calls.Load() != 1 {
		t.Fatal("reset caused provider replay")
	}
}
