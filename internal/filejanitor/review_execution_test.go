package filejanitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReviewExecution_LifecycleInvalidatedAfterConsumptionStopsBeforePreparation(t *testing.T) {
	for _, mode := range []string{"owner", "remove", "reinstall", "binding", "revoke", "root"} {
		t.Run(mode, func(t *testing.T) {
			s, batch, candidates := draftServiceFixture(t, "one.png")
			view := editDraft(t, s, batch.ID, "select_filing_groups", nil)
			preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(candidates, ""), ReviewRevision: view.Revision})
			if err != nil {
				t.Fatal(err)
			}
			settings, _ := s.store.LoadSettings("ws-1")
			ws, _ := s.workspaces.Get("ws-1")
			calls := 0
			s.now = func() time.Time {
				calls++
				if calls == 2 { // ConsumeApproval's clock is first; applyOne's is second.
					switch mode {
					case "owner":
						ws.OwnerUserID = "someone-else"
					case "remove":
						ws.InstalledCapabilities = nil
					case "reinstall":
						ws.InstalledCapabilities[0].InstalledAt = ws.InstalledCapabilities[0].InstalledAt.Add(time.Second)
					case "binding":
						ws.MCPBindings[0].Enabled = false
					case "revoke":
						_, err = s.store.UpdateSettings("ws-1", func(current *JanitorSettings) error { current.AccessRevokedAt = time.Now(); return nil })
					case "root":
						_, err = s.store.UpdateSettings("ws-1", func(current *JanitorSettings) error { current.RootID = "different-root"; return nil })
					}
				}
				return time.Now()
			}
			mover := &realMover{}
			s.SetMover(mover)
			result, confirmErr := s.ConfirmMoves(t.Context(), ConfirmRequest{WorkspaceID: "ws-1", UserID: "local", BatchID: batch.ID, Token: preview.Token, Items: moveItems(candidates, "")})
			if err != nil || confirmErr != nil || result.Stale != 1 || mover.calls != 0 {
				t.Fatalf("late invalidation escaped: %+v %v %v calls=%d", result, err, confirmErr, mover.calls)
			}
			if _, err := os.Stat(filepath.Join(settings.RootPath, "one.png")); err != nil {
				t.Fatal("source changed", err)
			}
			if _, err := os.Stat(filepath.Join(settings.RootPath, "Filed", "Images")); !os.IsNotExist(err) {
				t.Fatal("destination prepared after access invalidation", err)
			}
			state, _ := s.store.LoadScanState("ws-1")
			if len(state.Actions) != 0 || len(state.Approvals) != 1 || !state.Approvals[0].Consumed() {
				t.Fatal("unexpected journal/consumption boundary")
			}
		})
	}
}

func TestReviewExecution_EachItemGetsNewScopeAdmission(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	view := editDraft(t, s, batch.ID, "select_filing_groups", nil)
	preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(candidates, ""), ReviewRevision: view.Revision})
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := s.workspaces.Get("ws-1")
	mover := &realMover{beforeMove: func(string, string) { ws.OwnerUserID = "new-owner" }}
	s.SetMover(mover)
	result, err := s.ConfirmMoves(t.Context(), ConfirmRequest{WorkspaceID: "ws-1", UserID: "local", BatchID: batch.ID, Token: preview.Token, Items: moveItems(candidates, "")})
	if err != nil || result.Applied != 1 || result.Stale != 1 || mover.calls != 1 {
		t.Fatalf("later item inherited admitted operation's scope: %+v %v calls=%d", result, err, mover.calls)
	}
}

func TestReviewExecution_FreshLegacyRetryRemainsCompatible(t *testing.T) {
	s, _, candidates := reviewFixture(t, "fail.png")
	s.SetMover(reviewPartialMover{})
	preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(candidates, "")})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ConfirmMoves(t.Context(), ConfirmRequest{WorkspaceID: "ws-1", UserID: "local", BatchID: preview.BatchID, Token: preview.Token, Items: moveItems(candidates, "")})
	if err != nil || first.Failed != 1 {
		t.Fatalf("legacy initial failure: %+v %v", first, err)
	}
	fresh, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(candidates, "")})
	if err != nil {
		t.Fatal(err)
	}
	s.SetMover(&realMover{})
	retried, err := s.ConfirmMoves(t.Context(), ConfirmRequest{WorkspaceID: "ws-1", UserID: "local", BatchID: fresh.BatchID, Token: fresh.Token, Items: moveItems(candidates, "")})
	if err != nil || retried.Applied != 1 {
		t.Fatalf("fresh explicit legacy retry refused: %+v %v", retried, err)
	}
}

type reviewPartialMover struct{}

func (reviewPartialMover) Move(_ context.Context, _ string, source, destination string) error {
	if filepath.Base(source) == "fail.png" {
		return errors.New("synthetic connector failure")
	}
	return os.Rename(source, destination)
}

func TestReviewExecution_FailureRequiresUnselectedRetryReviewAndFreshPreview(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "good.png", "fail.png")
	view := editDraft(t, s, batch.ID, "select_filing_groups", nil)
	preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(candidates, ""), ReviewRevision: view.Revision})
	if err != nil {
		t.Fatal(err)
	}
	s.SetMover(reviewPartialMover{})
	result, err := s.ConfirmMoves(t.Context(), ConfirmRequest{WorkspaceID: "ws-1", UserID: "local", BatchID: batch.ID, Token: preview.Token, Items: moveItems(candidates, "")})
	if err != nil || result.Applied != 1 || result.Failed != 1 {
		t.Fatalf("partial outcome misreported: %+v %v", result, err)
	}
	view, err = s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || len(view.Choices) != 0 {
		t.Fatal("completed/failed intent survived for quiet replay", err)
	}
	failed := candidateNamed(t, candidates, "fail.png")
	view = editDraft(t, s, batch.ID, "retry_files", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{failed.ID} })
	if len(view.Choices) != 0 {
		t.Fatal("retry selected or executed automatically")
	}
	view = editDraft(t, s, batch.ID, "select_files", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{failed.ID} })
	if _, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems([]JanitorCandidate{failed}, ""), ReviewRevision: view.Revision}); err != nil {
		t.Fatal("fresh retry preview unavailable", err)
	}
}
