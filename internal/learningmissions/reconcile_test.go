package learningmissions

import "testing"

func TestReconcileRestartPinsCanonicalResultWithoutCompletingOrCrediting(t *testing.T) {
	s, db, i := reviewFixture(t)
	restarted := NewService(s.store, s.gate, s.source)
	restarted.now = s.now
	for range 3 { // startup and duplicated canonical/legacy notifications
		if err := restarted.Reconcile(t.Context(), "local", i.Definition.WorkspaceID); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := s.store.Instance(t.Context(), "local", i.ID)
	if err != nil || saved.Progress.Result == nil || saved.Progress.Result.RunID != i.Progress.RunID || saved.State != Active || saved.Revision != i.Revision+1 {
		t.Fatalf("reconciled: %+v %v", saved, err)
	}
	var credits int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM learning_evidence`).Scan(&credits); err != nil || credits != 0 {
		t.Fatal("event credited practice")
	}
	// A later Run cannot move the result pointer.
	s.source.(*reviewSource).proof.Ref.RunID = "unrelated"
	if err := restarted.Reconcile(t.Context(), "local", i.Definition.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	after, _ := s.store.Instance(t.Context(), "local", i.ID)
	if after.Revision != saved.Revision || *after.Progress.Result != *saved.Progress.Result {
		t.Fatal("latest unrelated result replaced pinned proof")
	}
}

func TestReconcileRecoversCreatedTicketWithoutReplayingCreation(t *testing.T) {
	s, host, id, req := workFixture(t)
	host.missing = true
	out, err := s.PrepareWork(t.Context(), "local", id, req)
	if err != nil || out.Operation.Status != "pending" {
		t.Fatal(err)
	}
	restarted := NewService(s.store, s.gate, host)
	if err := restarted.Reconcile(t.Context(), "local", ""); err != nil {
		t.Fatal(err)
	}
	host.ticket = out.Operation.TicketID
	if err := restarted.Reconcile(t.Context(), "local", ""); err != nil {
		t.Fatal(err)
	}
	op, err := s.Operation(t.Context(), "local", req.OperationID)
	if err != nil || op.Operation.Status != "succeeded" || host.calls != 1 {
		t.Fatalf("recovery replayed creation: %+v %v calls=%d", op, err, host.calls)
	}
	current, _ := s.store.Owner(t.Context(), "local")
	if _, err := s.ClearHistory(t.Context(), "local", MutationRequest{OperationID: "clear", ExpectedRevision: current.Revision}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(t.Context(), "local", ""); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(t.Context(), "local")
	if err != nil || len(status.Instances) != 0 || host.calls != 1 {
		t.Fatal("late event resurrected cleared work")
	}
}
