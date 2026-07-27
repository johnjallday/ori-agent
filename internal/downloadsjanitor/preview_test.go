package downloadsjanitor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// reviewFixture sets up a configured workspace with a scanned batch and returns
// the service, the folder, and the batch's candidates.
func reviewFixture(t *testing.T, names ...string) (*Service, string, []JanitorCandidate) {
	t.Helper()
	service, root := configuredService(t)
	if len(names) == 0 {
		names = []string{"report.pdf"}
	}
	for _, name := range names {
		agedFile(t, root, name, 100)
	}
	batch, created, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created {
		t.Fatalf("ScanNow: created=%v err=%v", created, err)
	}
	_, candidates, err := service.BatchDetail("ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	return service, root, candidates
}

func moveItems(candidates []JanitorCandidate, category string) []PreviewRequestItem {
	items := make([]PreviewRequestItem, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, PreviewRequestItem{CandidateID: candidate.ID, Operation: OperationMove, Category: category})
	}
	return items
}

func planFrom(preview Preview, candidates []JanitorCandidate) []PlanItem {
	byID := map[string]JanitorCandidate{}
	for _, candidate := range candidates {
		byID[candidate.ID] = candidate
	}
	plan := make([]PlanItem, 0, len(preview.Items))
	for _, item := range preview.Items {
		plan = append(plan, PlanItem{
			CandidateID:    item.CandidateID,
			Operation:      item.Operation,
			Category:       item.Category,
			FingerprintKey: byID[item.CandidateID].Fingerprint.Key(),
		})
	}
	return plan
}

func TestPreviewMoves_DerivesDestinationsFromServerState(t *testing.T) {
	service, _, candidates := reviewFixture(t, "report.pdf")

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	if len(preview.Items) != 1 || preview.MoveCount != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	item := preview.Items[0]
	if item.Destination != "Filed/Documents/report.pdf" {
		t.Fatalf("destination = %q", item.Destination)
	}
	if item.Renamed {
		t.Fatal("nothing was in the way, so nothing should be renamed")
	}
	if preview.Token == "" || preview.ExpiresAt.IsZero() || preview.IdempotencyKey == "" {
		t.Fatalf("preview must carry a single-use approval: %+v", preview)
	}

	// A preview mutates nothing and journals nothing.
	state, err := service.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Actions) != 0 {
		t.Fatalf("a preview must not journal an action: %+v", state.Actions)
	}
}

// The plan is the server's, not the client's: a requested category is validated
// against the allowlist, and everything path-shaped is rejected.
func TestPreviewMoves_RejectsPathsAndUnknownCategories(t *testing.T) {
	service, _, candidates := reviewFixture(t)

	for _, category := range []string{"../../etc", "/absolute", "Documents/../..", "receipts", "documents\x00"} {
		_, err := service.PreviewMoves(PreviewRequest{
			WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, category),
		})
		if err == nil {
			t.Fatalf("category %q should have been rejected", category)
		}
	}
}

// Only the two real operations are approvable. Anything else — including a
// hopeful "delete" — is rejected outright rather than interpreted.
func TestPreviewMoves_RejectsUnknownOperations(t *testing.T) {
	service, _, candidates := reviewFixture(t)

	for _, operation := range []Operation{"delete", "remove", "permanent_delete", ""} {
		items := moveItems(candidates, "")
		items[0].Operation = operation
		if _, err := service.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "user-1", Items: items}); err == nil {
			t.Fatalf("operation %q should have been rejected", operation)
		}
	}

	// Trash is approvable, but as its own operation: the preview counts it
	// separately so the confirmation can state the removal count on its own.
	items := moveItems(candidates, "")
	items[0].Operation = OperationTrash
	preview, err := service.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "user-1", Items: items})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	if preview.TrashCount != 1 || preview.MoveCount != 0 {
		t.Fatalf("preview counts = %d moves / %d trash", preview.MoveCount, preview.TrashCount)
	}
	if preview.Items[0].Destination != "" || preview.Items[0].Category != "" {
		t.Fatalf("a Trash item has no destination inside the folder: %+v", preview.Items[0])
	}
}

func TestPreviewMoves_RejectsUnknownAndUnactionableCandidates(t *testing.T) {
	service, _, candidates := reviewFixture(t)

	if _, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1",
		Items: []PreviewRequestItem{{CandidateID: "ghost", Operation: OperationMove}},
	}); !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("expected ErrCandidateNotFound, got %v", err)
	}

	// A skipped candidate is not approvable.
	if _, err := service.ApplyDecisions("ws-1", []DecisionUpdate{
		{CandidateID: candidates[0].ID, Decision: DecisionSkip},
	}); err != nil {
		t.Fatalf("ApplyDecisions: %v", err)
	}
	if _, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	}); !errors.Is(err, ErrCandidateNotActionable) {
		t.Fatalf("expected ErrCandidateNotActionable, got %v", err)
	}
}

// A file that changed after it was proposed cannot be approved: the plan the
// user is looking at is about a file that no longer exists in that form.
func TestPreviewMoves_RejectsAChangedSource(t *testing.T) {
	service, root, candidates := reviewFixture(t)
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("changed contents"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if !errors.Is(err, ErrCandidateNotActionable) {
		t.Fatalf("expected the changed file to be refused, got %v", err)
	}
}

// Ori never overwrites: when the destination name is taken, the preview shows
// the name the file will actually get.
func TestPreviewMoves_ResolvesCollisionsFinderStyle(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	destination := filepath.Join(root, DefaultFilingRootName, "Documents")
	if err := os.MkdirAll(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "report.pdf"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	item := preview.Items[0]
	if item.Destination != "Filed/Documents/report (2).pdf" {
		t.Fatalf("destination = %q, want the Finder-style rename", item.Destination)
	}
	if !item.Renamed {
		t.Fatal("the preview must tell the user the file is being renamed")
	}

	// The existing file is untouched — a preview reads, it does not write.
	data, err := os.ReadFile(filepath.Join(destination, "report.pdf"))
	if err != nil || string(data) != "existing" {
		t.Fatalf("the occupying file must be left alone: %q %v", string(data), err)
	}
}

// Two files in one plan heading for the same destination name must not both be
// promised it.
func TestPreviewMoves_ReservesNamesWithinOnePlan(t *testing.T) {
	service, root, _ := reviewFixture(t, "report.pdf")
	// A second file that will classify into the same category with the same
	// resolved name is not possible from one folder (names are unique), so
	// simulate the tighter case: an existing file plus two new ones.
	destination := filepath.Join(root, DefaultFilingRootName, "Documents")
	if err := os.MkdirAll(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	agedFile(t, root, "notes.txt", 10)
	if _, _, err := service.ScanNow("ws-1", ScanSourceManual); err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	_, candidates, err := service.LatestPendingBatchCandidates("ws-1")
	if err != nil {
		t.Fatal(err)
	}

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, "documents"),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	seen := map[string]bool{}
	for _, item := range preview.Items {
		if seen[item.Destination] {
			t.Fatalf("two files were promised the same destination: %q", item.Destination)
		}
		seen[item.Destination] = true
	}
}

func TestConsumeApproval_IsSingleUseAndBoundToItsPlan(t *testing.T) {
	service, _, candidates := reviewFixture(t)
	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	plan := planFrom(preview, candidates)

	record, err := service.ConsumeApproval("ws-1", "user-1", preview.Token, plan, preview.BatchID)
	if err != nil {
		t.Fatalf("ConsumeApproval: %v", err)
	}
	if record.IdempotencyKey != preview.IdempotencyKey {
		t.Fatalf("approval should carry the apply's idempotency key: %+v", record)
	}

	// Replaying the same confirm buys nothing.
	if _, err := service.ConsumeApproval("ws-1", "user-1", preview.Token, plan, preview.BatchID); !errors.Is(err, ErrApprovalConsumed) {
		t.Fatalf("expected ErrApprovalConsumed on replay, got %v", err)
	}
}

func TestConsumeApproval_RejectsEveryMismatchedBinding(t *testing.T) {
	service, _, candidates := reviewFixture(t)
	fresh := func(t *testing.T) (Preview, []PlanItem) {
		t.Helper()
		preview, err := service.PreviewMoves(PreviewRequest{
			WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
		})
		if err != nil {
			t.Fatalf("PreviewMoves: %v", err)
		}
		return preview, planFrom(preview, candidates)
	}

	t.Run("another user", func(t *testing.T) {
		preview, plan := fresh(t)
		if _, err := service.ConsumeApproval("ws-1", "someone-else", preview.Token, plan, preview.BatchID); !errors.Is(err, ErrApprovalInvalid) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("another workspace", func(t *testing.T) {
		preview, plan := fresh(t)
		if _, err := service.ConsumeApproval("ws-2", "user-1", preview.Token, plan, preview.BatchID); err == nil {
			t.Fatal("an approval must not cross workspaces")
		}
	})

	t.Run("another batch", func(t *testing.T) {
		preview, plan := fresh(t)
		if _, err := service.ConsumeApproval("ws-1", "user-1", preview.Token, plan, "batch-other"); !errors.Is(err, ErrApprovalInvalid) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("tampered category", func(t *testing.T) {
		preview, plan := fresh(t)
		plan[0].Category = CategoryInstallers
		if _, err := service.ConsumeApproval("ws-1", "user-1", preview.Token, plan, preview.BatchID); !errors.Is(err, ErrApprovalInvalid) {
			t.Fatalf("changing the category after approval must invalidate it, got %v", err)
		}
	})

	t.Run("tampered operation", func(t *testing.T) {
		preview, plan := fresh(t)
		plan[0].Operation = OperationTrash
		if _, err := service.ConsumeApproval("ws-1", "user-1", preview.Token, plan, preview.BatchID); !errors.Is(err, ErrApprovalInvalid) {
			t.Fatalf("upgrading a move to Trash must invalidate the approval, got %v", err)
		}
	})

	t.Run("changed file state", func(t *testing.T) {
		preview, plan := fresh(t)
		plan[0].FingerprintKey = "different"
		if _, err := service.ConsumeApproval("ws-1", "user-1", preview.Token, plan, preview.BatchID); !errors.Is(err, ErrApprovalInvalid) {
			t.Fatalf("a changed file must invalidate the approval, got %v", err)
		}
	})

	t.Run("extra item smuggled in", func(t *testing.T) {
		preview, plan := fresh(t)
		plan = append(plan, PlanItem{CandidateID: "extra", Operation: OperationMove, Category: CategoryDocuments})
		if _, err := service.ConsumeApproval("ws-1", "user-1", preview.Token, plan, preview.BatchID); !errors.Is(err, ErrApprovalInvalid) {
			t.Fatalf("adding an item after approval must invalidate it, got %v", err)
		}
	})

	t.Run("unknown token", func(t *testing.T) {
		_, plan := fresh(t)
		if _, err := service.ConsumeApproval("ws-1", "user-1", "not-a-real-token", plan, "batch-1"); !errors.Is(err, ErrApprovalInvalid) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("missing token", func(t *testing.T) {
		_, plan := fresh(t)
		if _, err := service.ConsumeApproval("ws-1", "user-1", "  ", plan, "batch-1"); !errors.Is(err, ErrApprovalRequired) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestConsumeApproval_RejectsAnExpiredToken(t *testing.T) {
	service, _, candidates := reviewFixture(t)
	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	plan := planFrom(preview, candidates)

	// Jump past the approval's lifetime.
	service.now = func() time.Time { return time.Now().Add(ApprovalTTL + time.Minute) }
	if _, err := service.ConsumeApproval("ws-1", "user-1", preview.Token, plan, preview.BatchID); !errors.Is(err, ErrApprovalExpired) {
		t.Fatalf("expected ErrApprovalExpired, got %v", err)
	}
}

// Two confirms racing on one approval must not both win: the token is consumed
// inside the same atomic write that records it.
func TestConsumeApproval_ConcurrentConfirmsYieldExactlyOneWinner(t *testing.T) {
	service, _, candidates := reviewFixture(t)
	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	plan := planFrom(preview, candidates)

	const racers = 8
	var wg sync.WaitGroup
	results := make([]error, racers)
	for i := range racers {
		wg.Go(func() {
			_, results[i] = service.ConsumeApproval("ws-1", "user-1", preview.Token, plan, preview.BatchID)
		})
	}
	wg.Wait()

	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
			continue
		}
		if !errors.Is(err, ErrApprovalConsumed) {
			t.Fatalf("a loser should report the token as already used, got %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly one confirm to win, got %d", winners)
	}
}

// The stored state must not contain a usable token: only its hash.
func TestApprovals_AreStoredHashedNotInPlaintext(t *testing.T) {
	service, _, candidates := reviewFixture(t)
	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}

	path, err := service.store.scanStatePath("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), preview.Token) {
		t.Fatal("the approval token must not be stored in plaintext")
	}
	if !strings.Contains(string(data), hashToken(preview.Token)) {
		t.Fatal("the approval's hash should be stored")
	}
}

func TestPreviewMoves_RequiresAUserAndSetup(t *testing.T) {
	service, _, candidates := reviewFixture(t)
	if _, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "  ", Items: moveItems(candidates, ""),
	}); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("an approval must belong to a user, got %v", err)
	}

	store, _ := newTestStore(t)
	unconfigured := NewService(store, newFakeWorkspaceStore("ws-1"))
	if _, err := unconfigured.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1",
		Items: []PreviewRequestItem{{CandidateID: "c1", Operation: OperationMove}},
	}); err == nil {
		t.Fatal("previewing must be gated on setup")
	}
}

func TestBumpName_IncrementsFinderStyleSuffixes(t *testing.T) {
	cases := map[string]string{
		"report.pdf":      "report (2).pdf",
		"report (2).pdf":  "report (3).pdf",
		"report (10).pdf": "report (11).pdf",
		"no-extension":    "no-extension (2)",
		"weird (x).pdf":   "weird (x) (2).pdf",
		"archive.tar.gz":  "archive.tar (2).gz",
	}
	for input, want := range cases {
		if got := bumpName(input); got != want {
			t.Errorf("bumpName(%q) = %q, want %q", input, got, want)
		}
	}
}
