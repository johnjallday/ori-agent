package learningmissions

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// Reopen the owned test database, not merely a second wrapper around the same
// connection. External-owner fixtures remain independent canonical stores.
func reopenLearning(t *testing.T, before *Service) *Service {
	t.Helper()
	requireFreshlyInitialized(t, before.store.db)
	var sequence int
	var name, path string
	if err := before.store.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil || name != "main" || path == "" {
		t.Fatal("owned database identity", err)
	}
	if err := before.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), &database.Config{Path: path, BusyTimeout: 5000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	next := NewService(NewSQLiteStore(db), &resetstate.WorkGate{}, before.source)
	next.now, next.designer = before.now, before.designer
	return next
}

func TestRestartAfterPhysicalTicketBeforeLearningFinalizationRecoversOnlyReservedIdentity(t *testing.T) {
	s, host, id, req := freshWorkFixture(t)
	if _, err := s.store.db.ExecContext(t.Context(), `CREATE TRIGGER fail_work_finish BEFORE UPDATE ON learning_operations WHEN NEW.kind='prepare_work' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'injected finalization failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareWork(t.Context(), "local", id, req); err == nil {
		t.Fatal("injected commit unexpectedly succeeded")
	}
	op, err := s.store.Operation(t.Context(), "local", req.OperationID)
	if err != nil || op.Status != "pending" || op.TicketID == "" || host.ticket != op.TicketID || host.calls != 1 {
		t.Fatal("claim/physical owner boundary", op, err)
	}
	s = reopenLearning(t, s)
	if _, err := s.store.db.ExecContext(t.Context(), `DROP TRIGGER fail_work_finish`); err != nil {
		t.Fatal(err)
	}
	out, err := s.PrepareWork(t.Context(), "local", id, req)
	if err != nil || out.Operation.Status != "succeeded" || out.Operation.TicketID != op.TicketID || host.calls != 1 || out.Instance.Progress.RunID != "" {
		t.Fatal("replayed or started work", out, err)
	}
	for range 3 {
		if err := s.Reconcile(t.Context(), "local", ""); err != nil {
			t.Fatal(err)
		}
	}
	if host.calls != 1 {
		t.Fatal("duplicate notifications repeated physical creation")
	}
}

func TestRestartAtReservedRunResultChallengeAndConsumedDecisionNeverManufacturesCredit(t *testing.T) {
	s, _, instance := freshReviewFixture(t)
	source := s.source.(*reviewSource)
	proof := source.proof
	source.proof.Status = "executing"
	s = reopenLearning(t, s)
	for range 2 {
		if err := s.Reconcile(t.Context(), "local", ""); err != nil {
			t.Fatal(err)
		}
	}
	current, _ := s.store.Instance(t.Context(), "local", instance.ID)
	if current.Progress.Result != nil || current.Progress.RunID != instance.Progress.RunID {
		t.Fatal("reserved execution became completion")
	}
	source.proof = proof
	source.proof.Ref.RunID = "later-unrelated-run"
	if err := s.Reconcile(t.Context(), "local", ""); err != nil {
		t.Fatal(err)
	}
	current, _ = s.store.Instance(t.Context(), "local", instance.ID)
	if current.Progress.Result != nil {
		t.Fatal("out-of-order event selected another Run")
	}
	source.proof = proof
	s = reopenLearning(t, s)
	if err := s.Reconcile(t.Context(), "local", ""); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.store.Owner(t.Context(), "local")
	current, _ = s.store.Instance(t.Context(), "local", instance.ID)
	request := ReviewRequest{MutationRequest: MutationRequest{OperationID: "prepare-restart-review", ExpectedRevision: owner.Revision}, InstanceRevision: current.Revision, Action: "prepare"}
	prepared, err := s.ReviewResult(t.Context(), "local", instance.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	s = reopenLearning(t, s)
	again, err := s.ReviewResult(t.Context(), "local", instance.ID, request)
	if err != nil || again.Review.ID != prepared.Review.ID || again.ResultText != proof.Text {
		t.Fatal("challenge recovery changed exact content or nonce", err)
	}
	practice, _ := s.store.Competencies(t.Context(), "local")
	if practice.Items[0].Attempts != 0 {
		t.Fatal("execution/inspection credited practice")
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	current, _ = s.store.Instance(t.Context(), "local", instance.ID)
	submit := ReviewRequest{MutationRequest: MutationRequest{OperationID: "consume-restart-review", ExpectedRevision: owner.Revision}, InstanceRevision: current.Revision, Action: "submit", ReviewID: prepared.Review.ID, Decision: "reject", Reason: "incorrect"}
	consumed, err := s.ReviewResult(t.Context(), "local", instance.ID, submit)
	if err != nil {
		t.Fatal(err)
	}
	s = reopenLearning(t, s)
	replayed, err := s.ReviewResult(t.Context(), "local", instance.ID, submit)
	if err != nil || replayed.Operation.ID != consumed.Operation.ID || replayed.Instance.State != Completed {
		t.Fatal("lost decision response did not recover", err)
	}
	practice, _ = s.store.Competencies(t.Context(), "local")
	if practice.Items[0].Attempts != 1 || practice.Items[1].Attempts != 1 {
		t.Fatal("projection did not rebuild exact deduplicated credit", practice)
	}
	owner, _ = s.store.Owner(t.Context(), "local")
	submit.OperationID, submit.ExpectedRevision = "reused-nonce", owner.Revision
	if _, err := s.ReviewResult(t.Context(), "local", instance.ID, submit); !errors.Is(err, ErrConflict) {
		t.Fatal("consumed challenge became usable after restart", err)
	}
}

func TestRestartAfterModelResponseBeforeCommitRetainsUnknownReservationWithoutReplay(t *testing.T) {
	s, _ := freshTestService(t)
	d, req := installDesigner(s)
	if _, err := s.store.db.ExecContext(t.Context(), `CREATE TRIGGER fail_design_finish BEFORE UPDATE ON learning_operations WHEN NEW.kind='generate' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'injected finalization failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Propose(t.Context(), "local", req); err == nil {
		t.Fatal("injected commit unexpectedly succeeded")
	}
	s = reopenLearning(t, s)
	op, err := s.store.Operation(t.Context(), "local", req.OperationID)
	if err != nil || op.Status != "pending" || op.ReservedTokens != GenerationReservedTokens || d.calls.Load() != 1 {
		t.Fatal("lost admitted claim", op, err)
	}
	now := s.now().Add(GenerationTimeout + time.Second)
	s.now = func() time.Time { return now }
	if err := s.Reconcile(t.Context(), "local", ""); err != nil {
		t.Fatal(err)
	}
	again, err := s.Propose(t.Context(), "local", req)
	if err != nil || again.Operation.Status != "unknown" || again.Operation.Failure != "outcome_unknown" || again.Instance != nil || d.calls.Load() != 1 {
		t.Fatal("uncertain model response was replayed or published", again, err)
	}
	diagnostics, err := s.Diagnostics(t.Context(), "local")
	if err != nil || diagnostics.DesignCalls != 1 || diagnostics.UnknownUsageCalls != 1 || diagnostics.ReservedTokens != GenerationReservedTokens {
		t.Fatal("uncertain cost was forgotten", diagnostics, err)
	}
}
