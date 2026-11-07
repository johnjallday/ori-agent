package learningmissions

import (
	"context"
	"errors"
	"testing"
)

type observedSourceStub struct {
	preferenceSource
	bound   []ObservedWork
	bindErr error
	proof   ResultProof
	input   string
}

func (f *observedSourceStub) ObservedStart(_ context.Context, owner, workspace, ticket, token string) (ObservedWork, error) {
	if owner != "local" || workspace != "workspace" {
		return ObservedWork{}, ErrUnavailable
	}
	input := f.input
	if input == "" {
		input = "reviewed input"
	}
	return ObservedWork{WorkspaceID: workspace, Incarnation: f.incarnation, TicketID: ticket, StartToken: token, InputDigest: digest([]byte(input))}, nil
}
func (f *observedSourceStub) BindObservedStart(_ context.Context, _ string, work ObservedWork) error {
	f.bound = append(f.bound, work)
	return f.bindErr
}
func (f *observedSourceStub) ObservedResult(_ context.Context, owner string, _ ObservedWork) (ResultProof, error) {
	if owner != "local" || f.proof.Ref.RunID == "" {
		return ResultProof{}, ErrUnavailable
	}
	return f.proof, nil
}
func (f *observedSourceStub) ObservedReady(_ context.Context, owner string, work ObservedWork) (bool, error) {
	return owner == "local" && f.proof.Ref.RunID == work.RunID && f.proof.Status == "succeeded", nil
}
func (f *observedSourceStub) ObservedTicketLink(_ context.Context, owner string, _ ObservedWork) string {
	if owner != "local" {
		return ""
	}
	return "/workspaces/practice/task/ticket"
}

func observationRun(work ObservedWork, token, run string) ObservedRun {
	return ObservedRun{OperationID: work.ID, WorkspaceID: work.WorkspaceID, TicketID: work.TicketID, StartToken: token, InputDigest: work.InputDigest, RunID: run}
}

func observationFixture(t *testing.T) (*Service, *observedSourceStub) {
	t.Helper()
	s, _ := testService(t)
	source := &observedSourceStub{preferenceSource: preferenceSource{incarnation: "incarnation"}}
	s.source = source
	return s, source
}

func TestObservedStartNeedsPriorGrantAndReservesOnlyItsExactRun(t *testing.T) {
	s, source := observationFixture(t)
	before, _ := s.CaptureObservationConsent(t.Context(), "local", "workspace")
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("consent", 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.ObserveStart(t.Context(), "local", "workspace", "ticket", "start", before); err != nil {
		t.Fatal(err)
	}
	if len(source.bound) != 0 {
		t.Fatal("later consent adopted a pre-opt-in Start")
	}
	grant, _ := s.CaptureObservationConsent(t.Context(), "local", "workspace")
	if err := s.ObserveStart(t.Context(), "local", "workspace", "ticket", "start", grant); err != nil {
		t.Fatal(err)
	}
	work := source.bound[0]
	if err := s.ObserveStart(t.Context(), "local", "workspace", "ticket", "start", grant); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate host callback: %v", err)
	}
	if len(source.bound) != 1 {
		t.Fatal("replayed a cross-store binding")
	}
	if err := s.ReserveObservedRun(t.Context(), "local", observationRun(work, "forged", "run")); err == nil {
		t.Fatal("forged Start reserved Run")
	}
	foreign := observationRun(work, "start", "run")
	foreign.TicketID = "other-ticket"
	if err := s.ReserveObservedRun(t.Context(), "local", foreign); err == nil {
		t.Fatal("copied marker reserved another Ticket")
	}
	for range 2 {
		if err := s.ReserveObservedRun(t.Context(), "local", observationRun(work, "start", "run")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReserveObservedRun(t.Context(), "local", observationRun(work, "start", "later-run")); !errors.Is(err, ErrConflict) {
		t.Fatalf("later retry replaced Run: %v", err)
	}
	op, err := s.store.Operation(t.Context(), "local", work.ID)
	if err != nil || op.Observation.RunID != "run" || op.Observation.Revision != 2 {
		t.Fatalf("exact persisted claim: %+v %v", op, err)
	}
	instances, _ := s.store.Instances(t.Context(), "local")
	if len(instances) != 0 {
		t.Fatal("ordinary work manufactured mission enrollment")
	}
	progress, _ := s.store.Competencies(t.Context(), "local")
	if progress.Items[0].Attempts != 0 {
		t.Fatal("Start/Run binding earned credit without review")
	}
}

func TestObservedStartCannotBorrowReapprovedConsentAtSameClockOrAfterClear(t *testing.T) {
	s, source := observationFixture(t)
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("consent", 0)); err != nil {
		t.Fatal(err)
	}
	old, _ := s.CaptureObservationConsent(t.Context(), "local", "workspace")
	if err := s.ObserveStart(t.Context(), "local", "workspace", "ticket", "start", old); err != nil {
		t.Fatal(err)
	}
	work := source.bound[0]
	o, _ := s.store.Owner(t.Context(), "local")
	off := preferenceRequest("off", o.Revision)
	off.Mode = "off"
	if _, err := s.UpdatePreferences(t.Context(), "local", off); err != nil {
		t.Fatal(err)
	}
	o, _ = s.store.Owner(t.Context(), "local")
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("again", o.Revision)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveObservedRun(t.Context(), "local", observationRun(work, "start", "late-run")); err == nil {
		t.Fatal("same-millisecond reapproval revived old claim")
	}
	if err := s.ObserveStart(t.Context(), "local", "workspace", "ticket", "late-start", old); err == nil {
		t.Fatal("old approval captured a new consent grant")
	}
	fresh, _ := s.CaptureObservationConsent(t.Context(), "local", "workspace")
	o, _ = s.store.Owner(t.Context(), "local")
	if _, err := s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear", ExpectedRevision: o.Revision}); err != nil {
		t.Fatal(err)
	}
	if err := s.ObserveStart(t.Context(), "local", "workspace", "ticket", "after-clear", fresh); err == nil {
		t.Fatal("old callback recreated cleared history")
	}
	if len(source.bound) != 1 {
		t.Fatal("revoked callback wrote physical correlation")
	}
}

func TestObservedUnknownBindingIsNotReplayed(t *testing.T) {
	s, source := observationFixture(t)
	if _, err := s.UpdatePreferences(t.Context(), "local", preferenceRequest("consent", 0)); err != nil {
		t.Fatal(err)
	}
	grant, _ := s.CaptureObservationConsent(t.Context(), "local", "workspace")
	source.bindErr = ErrUnavailable
	if err := s.ObserveStart(t.Context(), "local", "workspace", "ticket", "start", grant); err == nil {
		t.Fatal("unknown write hidden")
	}
	if err := s.ObserveStart(t.Context(), "local", "workspace", "ticket", "start", grant); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown callback retried: %v", err)
	}
	if len(source.bound) != 1 {
		t.Fatal("unknown cross-store effect repeated")
	}
	if err := s.ReserveObservedRun(t.Context(), "local", observationRun(source.bound[0], "start", "run")); err != nil {
		t.Fatal("already-written correlation could not recover canonical identity", err)
	}
}
