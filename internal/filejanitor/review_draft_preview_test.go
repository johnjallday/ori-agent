package filejanitor

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestReviewDraftPreview_BoundsApplyToLegacyPreviewAndConfirmToo(t *testing.T) {
	s, _, candidates := reviewFixture(t, "one.png")
	items := make([]PreviewRequestItem, MaxReviewChoicesPerBatch+1)
	for i := range items {
		items[i] = PreviewRequestItem{CandidateID: candidates[0].ID, Operation: OperationMove}
	}
	if _, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: items}); !errors.Is(err, ErrReviewBounds) {
		t.Fatalf("unbounded legacy preview: %v", err)
	}
	if _, err := s.ConfirmMoves(t.Context(), ConfirmRequest{WorkspaceID: "ws-1", UserID: "local", Items: items}); !errors.Is(err, ErrReviewBounds) {
		t.Fatalf("unbounded legacy confirm: %v", err)
	}
	state, err := s.store.LoadScanState("ws-1")
	if err != nil || len(state.Approvals) != 0 || len(state.Actions) != 0 {
		t.Fatal("bounds refusal issued authority or mutated state", err)
	}
}

func TestReviewDraftPreview_RequiresFreshExactIntentEvenForLegacyCallers(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	view := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	for _, mode := range []string{"missing revision", "subset", "duplicates", "changed category", "changed operation"} {
		t.Run(mode, func(t *testing.T) {
			request := PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(candidates, ""), ReviewRevision: view.Revision}
			switch mode {
			case "missing revision":
				request.ReviewRevision = ""
			case "subset":
				request.Items = request.Items[:1]
			case "duplicates":
				request.Items[1] = request.Items[0]
			case "changed category":
				request.Items[0].Category = "documents"
			case "changed operation":
				request.Items[0].Operation = OperationTrash
			}
			if _, err := s.PreviewMoves(request); !errors.Is(err, ErrReviewConflict) {
				t.Fatalf("preview escaped exact saved intent: %v", err)
			}
		})
	}
	state, err := s.store.LoadScanState("ws-1")
	if err != nil || len(state.Approvals) != 0 {
		t.Fatal("rejected preview issued authority", err)
	}
	preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(candidates, ""), ReviewRevision: view.Revision})
	if err != nil {
		t.Fatal(err)
	}
	state, err = s.store.LoadScanState("ws-1")
	if err != nil || len(state.Approvals) != 1 || state.Approvals[0].ReviewRevision != view.Revision || state.Approvals[0].ReviewScope == "" || preview.MoveCount != 2 {
		t.Fatalf("missing canonical approval fence: %+v %v", state.Approvals, err)
	}
	editDraft(t, s, batch.ID, "exclude_files", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{candidates[0].ID} })
	if _, err := s.ConsumeApproval("ws-1", "local", preview.Token, planFrom(preview, candidates), batch.ID); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("old confirmation survived another tab's deselection: %v", err)
	}
}

func TestReviewDraftPreview_ConsumptionRechecksScopeAndIntentUnderLock(t *testing.T) {
	for _, mode := range []string{"generation", "install", "root", "owner", "classification"} {
		t.Run(mode, func(t *testing.T) {
			s, batch, candidates := draftServiceFixture(t, "one.png")
			view := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
			preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(candidates, ""), ReviewRevision: view.Revision})
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "generation":
				_, err = s.store.UpdateScanState("ws-1", func(state *ScanState) error { state.ReviewDrafts[0].Generation++; return nil })
			case "classification":
				_, err = s.store.UpdateScanState("ws-1", func(state *ScanState) error { state.Candidates[0].Confidence = ConfidenceLow; return nil })
			case "install":
				ws, _ := s.workspaces.Get("ws-1")
				ws.InstalledCapabilities[0].InstalledAt = time.Now().Add(time.Hour)
			case "root":
				_, err = s.store.UpdateSettings("ws-1", func(settings *JanitorSettings) error { settings.RootID = "new-root"; return nil })
			case "owner":
				ws, _ := s.workspaces.Get("ws-1")
				ws.OwnerUserID = "other-owner"
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ConsumeApproval("ws-1", "local", preview.Token, planFrom(preview, candidates), batch.ID); !errors.Is(err, ErrApprovalInvalid) {
				t.Fatalf("stale bound token consumed: %v", err)
			}
		})
	}
}

func TestReviewDraftReturn_RejectsLifecycleChangesAndNeverReplaysAcrossReinstall(t *testing.T) {
	for _, mode := range []string{"owner", "revoke", "remove", "reinstall", "relink"} {
		t.Run(mode, func(t *testing.T) {
			s, batch, _ := draftServiceFixture(t, "one.png")
			editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
			settings, err := s.store.LoadSettings("ws-1")
			if err != nil {
				t.Fatal(err)
			}
			ws, _ := s.workspaces.Get("ws-1")
			switch mode {
			case "owner":
				ws.OwnerUserID = "new-owner"
			case "remove":
				ws.InstalledCapabilities = nil
			case "reinstall":
				ws.InstalledCapabilities[0].InstalledAt = time.Now().Add(time.Hour)
			case "revoke":
				_, err = s.RevokeAccess(nil, "ws-1")
			case "relink":
				_, err = s.RevokeAccess(nil, "ws-1")
				if err == nil {
					_, err = s.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: settings.RootPath})
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
			if !errors.Is(err, ErrReviewUnavailable) || len(view.Choices) != 0 {
				t.Fatalf("old choices restored into new lifecycle: %+v %v", view, err)
			}
		})
	}
}

func TestReviewDraftReturn_RevalidatesSelectedNamesWithoutWritingOrScanning(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	selected := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	settings, err := s.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	first := candidateNamed(t, candidates, "one.png")
	if err := os.Rename(filepath.Join(settings.RootPath, first.Name), filepath.Join(settings.RootPath, "renamed.png")); err != nil {
		t.Fatal(err)
	}
	before, err := s.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || len(view.Choices) != 1 || view.RemovedStale != 1 || view.Revision == selected.Revision {
		t.Fatalf("stale selected file restored on return: %+v %v", view, err)
	}
	after, err := s.store.LoadScanState("ws-1")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("return wrote state or scanned", err)
	}
	_, err = s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{ExpectedRevision: selected.Revision, ExpectedGroupRevision: selected.GroupRevision, Action: "select_group", GroupID: reviewGroupID("filing-images")})
	if !errors.Is(err, ErrReviewConflict) {
		t.Fatalf("stale return overwrote current choices: %v", err)
	}
	_, err = s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{ExpectedRevision: view.Revision, ExpectedGroupRevision: view.GroupRevision, Action: "select_group", GroupID: reviewGroupID("filing-images")})
	if !errors.Is(err, ErrCandidateNotActionable) {
		t.Fatalf("selection silently staged a changed file: %v", err)
	}
	newBatch, created, err := s.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created {
		t.Fatal("explicit fresh scan not usable", err)
	}
	fresh, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", newBatch.ID)
	if err != nil || len(fresh.Choices) != 0 {
		t.Fatal("fresh scan auto-selected replacement", err)
	}
}
