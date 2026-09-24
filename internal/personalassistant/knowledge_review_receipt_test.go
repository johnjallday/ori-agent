package personalassistant

import (
	"context"
	"errors"
	"testing"
)

func TestCandidateReviewEditAndRejectReceiptsSurviveRestart(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	s := NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	item, _, err := s.Propose(ctx, "local", testProposal("receipt-app", "Original suggestion"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.EditCandidateWithReceipt(ctx, "local", item.ID, item.Version, "edit-request", "My confirmed wording")
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	replay, err := restarted.EditCandidateWithReceipt(ctx, "local", item.ID, item.Version, "edit-request", "My confirmed wording")
	if err != nil || replay.Version != first.Version || replay.CurrentRevisionID != first.CurrentRevisionID {
		t.Fatalf("edit replay: %+v %v", replay, err)
	}
	if _, err := restarted.EditCandidateWithReceipt(ctx, "local", item.ID, item.Version, "edit-request", "Different wording"); !errors.Is(err, ErrConflict) {
		t.Fatalf("replayed edit changed text: %v", err)
	}
	if _, err := restarted.EditCandidateWithReceipt(ctx, "local", item.ID, first.Version, "edit-request-2", "Newer wording"); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.EditCandidateWithReceipt(ctx, "local", item.ID, item.Version, "edit-request", "My confirmed wording"); !errors.Is(err, ErrConflict) {
		t.Fatalf("old revision replay superseded newer edit: %v", err)
	}
	latest, err := restarted.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	version := latest.Items[0].Version
	rejected, err := restarted.SuppressCandidateWithReceipt(ctx, "local", item.ID, version, "reject-request", KnowledgeRejected)
	if err != nil || rejected.State != KnowledgeRejected || len(rejected.Revisions) != 0 {
		t.Fatalf("reject: %+v %v", rejected, err)
	}
	resumed := NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	again, err := resumed.SuppressCandidateWithReceipt(ctx, "local", item.ID, version, "reject-request", KnowledgeRejected)
	if err != nil || again.Version != rejected.Version || len(again.Revisions) != 0 {
		t.Fatalf("reject replay: %+v %v", again, err)
	}
	if _, err := resumed.SuppressCandidateWithReceipt(ctx, "local", item.ID, version, "edit-request", KnowledgeRejected); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused edit request id on reject: %v", err)
	}
}
