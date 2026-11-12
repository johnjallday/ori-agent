package learningmissions

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

type workSource struct {
	sourceStub
	mu      sync.Mutex
	calls   int
	missing bool
	ticket  string
}

func (s *workSource) CheckWork(context.Context, string, Instance, string) error { return nil }
func (s *workSource) CreateTicket(_ context.Context, _ string, i Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if !s.missing {
		s.ticket = i.Progress.TicketID
	}
	return errors.New("reply lost after possible creation")
}
func (s *workSource) MatchTicket(_ context.Context, _ string, i Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ticket != i.Progress.TicketID {
		return ErrUnavailable
	}
	return nil
}

func workFixture(t *testing.T) (*Service, *workSource, string, WorkRequest) {
	t.Helper()
	return workFixtureOn(t, testService)
}

// freshWorkFixture is workFixture on a freshly initialized database, for the
// restart tests.
func freshWorkFixture(t *testing.T) (*Service, *workSource, string, WorkRequest) {
	t.Helper()
	return workFixtureOn(t, freshTestService)
}

func workFixtureOn(t *testing.T, newService serviceOpener) (*Service, *workSource, string, WorkRequest) {
	t.Helper()
	s, _ := newService(t)
	offer := propose(t, s)
	if _, err := s.Decide(t.Context(), "local", offer.Instance.ID, DecisionRequest{
		MutationRequest: MutationRequest{OperationID: "accept", ExpectedRevision: 1}, InstanceRevision: 1, Decision: "accept"}); err != nil {
		t.Fatal(err)
	}
	host := &workSource{}
	s.source = host
	return s, host, offer.Instance.ID, WorkRequest{MutationRequest: MutationRequest{OperationID: "prepare", ExpectedRevision: 2},
		InstanceRevision: 2, Input: "Draft using this supplied text", Assignee: "Ori"}
}

func TestWorkPreparationReconcilesLostCreationReplyAndNeverStarts(t *testing.T) {
	s, host, id, req := workFixture(t)
	var first string
	for range 2 {
		out, err := s.PrepareWork(t.Context(), "local", id, req)
		if err != nil || out.Operation.Status != "succeeded" || out.Instance.Progress.RunID != "" || out.Instance.Progress.TicketID == "" {
			t.Fatalf("prepared, not started: %+v %v", out, err)
		}
		if first != "" && first != out.Instance.Progress.TicketID {
			t.Fatal("retry created another Ticket identity")
		}
		first = out.Instance.Progress.TicketID
	}
	if host.calls != 1 {
		t.Fatalf("creation calls = %d", host.calls)
	}
}

func TestWorkUnknownOutcomeOnlyLooksUpOriginalIdentity(t *testing.T) {
	s, host, id, req := workFixture(t)
	host.missing = true
	out, err := s.PrepareWork(t.Context(), "local", id, req)
	if err != nil || out.Operation.Status != "pending" {
		t.Fatalf("unknown effect must stay pending: %+v %v", out, err)
	}
	if err := s.ReserveRun(t.Context(), "local", id, out.Operation.TicketID, uuid.NewString(), 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("unreconciled creation permitted execution: %v", err)
	}
	for range 2 {
		if _, err := s.PrepareWork(t.Context(), "local", id, req); err != nil {
			t.Fatal(err)
		}
	}
	if host.calls != 1 {
		t.Fatal("unknown creation was retried")
	}
	// Canonical persistence becomes readable again; lookup can reconcile it.
	host.ticket = out.Operation.TicketID
	out, err = s.PrepareWork(t.Context(), "local", id, req)
	if err != nil || out.Operation.Status != "succeeded" || host.calls != 1 {
		t.Fatalf("lookup recovery: %+v %v", out, err)
	}
}

func TestWorkReservationPinsOneRunAndRefusesLateUnrelatedStart(t *testing.T) {
	s, _, id, req := workFixture(t)
	out, err := s.PrepareWork(t.Context(), "local", id, req)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	if err := s.ReserveRun(t.Context(), "local", id, out.Operation.TicketID, run, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveRun(t.Context(), "local", id, out.Operation.TicketID, uuid.NewString(), 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("later start replaced first attempt: %v", err)
	}
	i, _ := s.store.Instance(t.Context(), "local", id)
	if i.Progress.RunID != run || i.Progress.AttemptID != out.Operation.AttemptID {
		t.Fatal("reserved attempt identity moved")
	}
}
