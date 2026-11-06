package learningmissions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type sourceStub struct{ missing bool }

func (f sourceStub) Candidate(_ context.Context, owner, workspace string) (Candidate, error) {
	if f.missing || owner != "local" || workspace != "workspace" {
		return Candidate{}, ErrUnavailable
	}
	return Candidate{Ref: "candidate", WorkspaceID: workspace, Incarnation: "incarnation", ScopeDigest: digest([]byte("scope")), Name: "Practice"}, nil
}
func (f sourceStub) Validate(ctx context.Context, owner string, d Definition) error {
	c, err := f.Candidate(ctx, owner, d.WorkspaceID)
	if err != nil || c.Ref != d.CandidateRef || c.Incarnation != d.Incarnation || c.ScopeDigest != d.ScopeDigest {
		return ErrUnavailable
	}
	return nil
}

func testService(t *testing.T) (*Service, *database.DB) {
	t.Helper()
	s, db, _ := testStore(t)
	service := NewService(s, &resetstate.WorkGate{}, sourceStub{})
	service.now = func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) }
	return service, db
}

func propose(t *testing.T, s *Service) Outcome {
	t.Helper()
	out, err := s.Propose(t.Context(), "local", ProposalRequest{MutationRequest: MutationRequest{OperationID: "proposal", ExpectedRevision: 0}, WorkspaceID: "workspace"})
	if err != nil || out.Instance == nil {
		t.Fatalf("propose: %+v %v", out, err)
	}
	return out
}

func TestServiceLostResponseReplaysDurableOperationNotEffect(t *testing.T) {
	s, db := testService(t)
	first := propose(t, s)
	// The first response is lost. A fresh service object recovers the operation
	// even when canonical eligibility has since disappeared.
	restarted := NewService(s.store, s.gate, sourceStub{missing: true})
	again := propose(t, restarted)
	if first.Operation.ID != again.Operation.ID || first.Instance.ID != again.Instance.ID {
		t.Fatal("retry replaced the original operation/instance")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_instances`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate effect: %d %v", count, err)
	}
	_, err := s.Propose(t.Context(), "local", ProposalRequest{MutationRequest: MutationRequest{OperationID: "proposal"}, WorkspaceID: "foreign"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("same operation with different content: %v", err)
	}
}

func TestServiceConcurrentAcceptanceHasOneWinner(t *testing.T) {
	s, _ := testService(t)
	offer := propose(t, s)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"tab-one", "tab-two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := s.Decide(context.Background(), "local", offer.Instance.ID, DecisionRequest{
				MutationRequest: MutationRequest{OperationID: id, ExpectedRevision: 1}, InstanceRevision: 1, Decision: "accept"})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("acceptance winners: %d", winners)
	}
	got, _ := s.store.Instance(t.Context(), "local", offer.Instance.ID)
	if got.State != Active || got.Progress.TicketID != "" || got.Progress.RunID != "" {
		t.Fatal("acceptance must not imply creating or starting work")
	}
}

func TestServiceIdempotentDecisionAndStaleRevision(t *testing.T) {
	s, _ := testService(t)
	offer := propose(t, s)
	req := DecisionRequest{MutationRequest: MutationRequest{OperationID: "accept", ExpectedRevision: 1}, InstanceRevision: 1, Decision: "accept"}
	for range 2 {
		if _, err := s.Decide(t.Context(), "local", offer.Instance.ID, req); err != nil {
			t.Fatal(err)
		}
	}
	req.OperationID, req.Decision = "stop", "stop"
	if _, err := s.Decide(t.Context(), "local", offer.Instance.ID, req); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale tab mutation: %v", err)
	}
	req.ExpectedRevision, req.InstanceRevision = 2, 2
	s.source = sourceStub{missing: true}
	if out, err := s.Decide(t.Context(), "local", offer.Instance.ID, req); err != nil || out.Instance.State != Stopped {
		t.Fatalf("stop must work without the source: %+v %v", out, err)
	}
}

func TestServicePersistenceFailureRollsBackEffectAndReceipt(t *testing.T) {
	s, db := testService(t)
	// Inject failure after instance insertion, at the final receipt boundary.
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER refuse_learning_receipt BEFORE INSERT ON learning_operations BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.Propose(t.Context(), "local", ProposalRequest{MutationRequest: MutationRequest{OperationID: "proposal"}, WorkspaceID: "workspace"})
	if err == nil {
		t.Fatal("injected persistence failure was ignored")
	}
	status, err := s.Status(t.Context(), "local")
	if err != nil || len(status.Instances) != 0 || status.Owner.Revision != 0 {
		t.Fatalf("partial durable effect: %+v %v", status, err)
	}
}

func TestServiceClearFencesOldOperationsAndCallbacks(t *testing.T) {
	s, _ := testService(t)
	propose(t, s)
	before, _ := s.store.Owner(t.Context(), "local")
	clear := MutationRequest{OperationID: "clear", ExpectedRevision: before.Revision}
	for range 2 {
		if _, err := s.ClearHistory(t.Context(), "local", clear); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := s.store.Owner(t.Context(), "local")
	if after.Epoch != before.Epoch+1 || after.Revision != before.Revision+1 {
		t.Fatalf("clear replay advanced epoch: %+v", after)
	}
	_, err := s.store.Change(t.Context(), "local", after.Revision, before.Epoch, false, func(m *Mutation) error {
		return m.InsertInstance(testInstance("local", "late"))
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("late callback recreated cleared work: %v", err)
	}
	_, err = s.Propose(t.Context(), "local", ProposalRequest{MutationRequest: MutationRequest{OperationID: "proposal"}, WorkspaceID: "workspace"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("old operation revived after clear: %v", err)
	}
	status, _ := s.Status(t.Context(), "local")
	if len(status.Instances) != 0 {
		t.Fatal("clear left learning history")
	}
}

func TestServiceRequiresResetAdmissionAndReadsStayInert(t *testing.T) {
	s, db := testService(t)
	for range 3 {
		if _, err := s.Status(t.Context(), "local"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_operations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("status created work")
	}
	if err := s.gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := s.Propose(t.Context(), "local", ProposalRequest{MutationRequest: MutationRequest{OperationID: "proposal"}, WorkspaceID: "workspace"})
	if !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("write after reset fence: %v", err)
	}
	s.gate = nil
	_, err = s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear"})
	if !errors.Is(err, resetstate.ErrWorkUntracked) {
		t.Fatalf("untracked writer: %v", err)
	}
}
