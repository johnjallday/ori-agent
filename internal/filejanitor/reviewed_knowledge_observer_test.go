package filejanitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type journalKnowledgeObserver struct {
	t       *testing.T
	service *Service
	moves   []string
	undos   []string
}

func (o *journalKnowledgeObserver) MoveApplied(workspaceID, actionID string) {
	o.t.Helper()
	actions, err := o.service.ListActions(workspaceID)
	if err != nil || len(actions) != 1 || actions[0].ID != actionID || actions[0].Result != ResultApplied {
		o.t.Fatalf("apply notification before durable journal: %+v %v", actions, err)
	}
	o.moves = append(o.moves, actionID)
}

func (o *journalKnowledgeObserver) UndoSucceeded(workspaceID, actionID string) {
	o.t.Helper()
	actions, err := o.service.ListActions(workspaceID)
	if err != nil || len(actions) != 1 || actions[0].ID != actionID || actions[0].Undo != UndoDone {
		o.t.Fatalf("undo notification before durable UndoDone: %+v %v", actions, err)
	}
	o.undos = append(o.undos, actionID)
}

func TestReviewedKnowledgeObserverOnlySeesDurableAppliedAndSuccessfulUndoIDs(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&realMover{})
	observer := &journalKnowledgeObserver{t: t, service: service}
	service.SetReviewedKnowledgeObserver(observer)
	result := approveAndConfirm(t, service, candidates, "")
	if result.Applied != 1 || len(observer.moves) != 1 || observer.moves[0] != result.Outcomes[0].ActionID {
		t.Fatalf("verified applied move did not notify once: %+v moves=%v", result, observer.moves)
	}
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed, err := service.Undo(context.Background(), "ws-1", observer.moves[0], "user-1")
	if err != nil || failed.Result != "failed" || len(observer.undos) != 0 {
		t.Fatalf("failed undo notified learning: %+v %v undos=%v", failed, err, observer.undos)
	}
	if err := os.Remove(filepath.Join(root, "report.pdf")); err != nil {
		t.Fatal(err)
	}
	// Failed undo is a terminal state for the action; a retry is refused.
	if _, err := service.Undo(context.Background(), "ws-1", observer.moves[0], "user-1"); err == nil || len(observer.undos) != 0 {
		t.Fatalf("failed undo unexpectedly replayed: %v undos=%v", err, observer.undos)
	}
	other, _, next := reviewFixture(t, "second.pdf")
	other.SetMover(&realMover{})
	otherObserver := &journalKnowledgeObserver{t: t, service: other}
	other.SetReviewedKnowledgeObserver(otherObserver)
	applied := approveAndConfirm(t, other, next, "")
	undone, err := other.Undo(context.Background(), "ws-1", applied.Outcomes[0].ActionID, "user-1")
	if err != nil || undone.Result != "undone" || len(otherObserver.moves) != 1 || len(otherObserver.undos) != 1 {
		t.Fatalf("successful undo did not notify after journal: %+v %v moves=%v undos=%v", undone, err, otherObserver.moves, otherObserver.undos)
	}
}

type unavailableKnowledgeObserver struct{}

func (unavailableKnowledgeObserver) MoveApplied(string, string) { panic("learning store unavailable") }
func (unavailableKnowledgeObserver) UndoSucceeded(string, string) {
	panic("learning store unavailable")
}

func TestReviewedKnowledgeFailureCannotChangeFileActionReceipt(t *testing.T) {
	service, _, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&realMover{})
	service.SetReviewedKnowledgeObserver(unavailableKnowledgeObserver{})
	applied := approveAndConfirm(t, service, candidates, "")
	if applied.Applied != 1 {
		t.Fatalf("learning failure rolled back move: %+v", applied)
	}
	undone, err := service.Undo(context.Background(), "ws-1", applied.Outcomes[0].ActionID, "user-1")
	if err != nil || undone.Result != "undone" {
		t.Fatalf("learning failure rolled back undo: %+v %v", undone, err)
	}
	actions, err := service.ListActions("ws-1")
	if err != nil || len(actions) != 1 || actions[0].Result != ResultApplied || actions[0].Undo != UndoDone {
		t.Fatalf("journal lost despite successful file actions: %+v %v", actions, err)
	}
}

func TestReviewedKnowledgeObserverIgnoresUnverifiedMove(t *testing.T) {
	service, _, candidates := reviewFixture(t, "failed.pdf")
	service.SetMover(&lyingMover{})
	observer := &journalKnowledgeObserver{t: t, service: service}
	service.SetReviewedKnowledgeObserver(observer)
	result := approveAndConfirm(t, service, candidates, "")
	if result.Applied != 0 || len(observer.moves) != 0 || len(observer.undos) != 0 {
		t.Fatalf("unverified action notified: %+v %+v", result, observer)
	}
}
