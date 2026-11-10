package filejanitor

import (
	"errors"
	"testing"
	"time"
)

// A saved legacy category edit is not checkbox selection. The group draft must
// preserve this distinction when it is introduced; nothing here issues approval.
func TestGroupReviewContract_CategoryOnlyDecisionRemainsUnapproved(t *testing.T) {
	s, _, candidates := reviewFixture(t)
	id := candidates[0].ID
	if _, err := s.ApplyDecisions("ws-1", []DecisionUpdate{{CandidateID: id, Decision: DecisionMove, Category: "images"}}); err != nil {
		t.Fatal(err)
	}
	// Recreate the store/service, rather than relying on a cached read.
	restarted := NewService(NewStore(s.store.resolver), s.workspaces)
	state, err := restarted.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	candidate, found := state.Candidate(id)
	if !found || candidate.State != CandidatePending || candidate.DecisionCategory != CategoryImages || candidate.Category != CategoryDocuments {
		t.Fatalf("category-only decision changed proposal/approval state: %+v", candidate)
	}
	if len(state.Approvals) != 0 || len(state.Actions) != 0 {
		t.Fatal("saving a category issued authority or journaled a file action")
	}
}

// The explicit category in the old submitted payload can otherwise mask a later
// deselection: PayloadHash alone does not bind the current saved user intent.
func TestGroupReviewContract_ClearedDecisionInvalidatesUnspentPreview(t *testing.T) {
	s, _, candidates := reviewFixture(t)
	preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, "documents")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyDecisions("ws-1", []DecisionUpdate{{CandidateID: candidates[0].ID, Decision: DecisionNone}}); err != nil {
		t.Fatal(err)
	}
	_, err = s.ConsumeApproval("ws-1", "user-1", preview.Token, planFrom(preview, candidates), preview.BatchID)
	if !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("a cleared decision must invalidate old authority, got %v", err)
	}
}

func TestGroupReviewContract_ApprovalInvalidationKeepsConsumedAndOtherBatches(t *testing.T) {
	state := ScanState{Approvals: []ApprovalRecord{
		{ID: "old", BatchID: "batch-1"},
		{ID: "spent", BatchID: "batch-1", ConsumedAt: time.Now()},
		{ID: "other", BatchID: "batch-2"},
	}}
	invalidateBatchApprovals(&state, "batch-1")
	if len(state.Approvals) != 2 || state.Approvals[0].ID != "spent" || state.Approvals[1].ID != "other" {
		t.Fatalf("invalidated unrelated or consumed authority: %+v", state.Approvals)
	}
}

func TestGroupReviewContract_TargetedReconsiderDoesNotReviveOldCandidate(t *testing.T) {
	s, _, candidates := reviewFixture(t, "one.pdf", "two.pdf")
	updates := make([]DecisionUpdate, 0, len(candidates))
	for _, candidate := range candidates {
		updates = append(updates, DecisionUpdate{CandidateID: candidate.ID, Decision: DecisionSkip})
	}
	if _, err := s.ApplyDecisions("ws-1", updates); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetSkipped("ws-1", candidates[0].Fingerprint.Key()); err != nil {
		t.Fatal(err)
	}
	state, err := s.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.IsSkipped(candidates[0].Fingerprint) || !state.IsSkipped(candidates[1].Fingerprint) {
		t.Fatal("targeted reconsider changed unrelated skip records")
	}
	old, _ := state.Candidate(candidates[0].ID)
	if old.State != CandidateSkipped {
		t.Fatal("reconsider revived an old candidate instead of requiring a fresh scan")
	}
	batch, created, err := s.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created || len(batch.CandidateIDs) != 1 || batch.CandidateIDs[0] == old.ID {
		t.Fatalf("explicit scan must create exactly one fresh candidate: %+v, %v", batch, err)
	}
}
