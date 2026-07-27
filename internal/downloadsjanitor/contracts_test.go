package downloadsjanitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The PRD's success metrics, asserted directly rather than inferred from the
// behaviour tests that happen to cover them.
//
// These exist because "we have tests for that" is not the same claim as "this
// property holds". Each test below states one contract in the PRD's own terms
// and fails loudly if it stops being true — including if a future change makes
// it true only by accident.

// Metric 2: every move and Trash mutation has a prior explicit approval record
// and a journal entry.
func TestContract_EveryMutationHasPriorApprovalAndAJournalEntry(t *testing.T) {
	service, _, candidates := reviewFixture(t, "a.pdf", "b.png", "c.zip")
	mover := &realMover{}
	service.SetMover(mover)
	trash := newFakeTrash(t)
	service.SetTrash(trash)

	// A mixed batch: two moves and one removal.
	items := []PreviewRequestItem{
		{CandidateID: candidates[0].ID, Operation: OperationMove, Category: "documents"},
		{CandidateID: candidates[1].ID, Operation: OperationMove, Category: "images"},
		{CandidateID: candidates[2].ID, Operation: OperationTrash},
	}
	result := approveAndConfirmItems(t, service, items)
	if result.Applied != 3 {
		t.Fatalf("expected three applied mutations: %+v", result)
	}

	actions, err := service.ListActions("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 3 {
		t.Fatalf("every mutation needs a journal entry: %d entries for 3 mutations", len(actions))
	}
	for _, action := range actions {
		if action.ApprovedBy == "" {
			t.Fatalf("%s has no approver", action.SourceName)
		}
		if action.ApprovedAt.IsZero() {
			t.Fatalf("%s has no approval time", action.SourceName)
		}
		// Approval must precede execution, not merely accompany it.
		if !action.StartedAt.IsZero() && action.ApprovedAt.After(action.StartedAt) {
			t.Fatalf("%s was executed before it was approved", action.SourceName)
		}
		if action.BeforeFingerprint.Zero() {
			t.Fatalf("%s does not record the file state that was approved", action.SourceName)
		}
		if action.IdempotencyKey == "" {
			t.Fatalf("%s has no idempotency key", action.SourceName)
		}
	}
	// The count of real filesystem effects matches the count of journal
	// entries: nothing happened that was not recorded.
	if mover.calls+trash.moves != len(actions) {
		t.Fatalf("%d filesystem effects but %d journal entries", mover.calls+trash.moves, len(actions))
	}
}

// Metric 3: zero overwrites, source-root escapes, destination-root escapes, or
// symbolic-link traversals.
func TestContract_NoOverwritesEscapesOrTraversals(t *testing.T) {
	service, root, _ := reviewFixture(t, "report.pdf")
	service.SetMover(&realMover{})

	// An existing file with known contents occupies the destination.
	destination := filepath.Join(root, "Filed", "Documents")
	if err := os.MkdirAll(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	occupant := filepath.Join(destination, "report.pdf")
	if err := os.WriteFile(occupant, []byte("do not overwrite me"), 0o600); err != nil {
		t.Fatal(err)
	}
	// And a sibling folder outside the root, which nothing may reach.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "untouchable.pdf"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, candidates, err := service.LatestPendingBatchCandidates("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	approveAndConfirm(t, service, candidates, "")

	// The occupant survives byte for byte.
	data, err := os.ReadFile(occupant)
	if err != nil || string(data) != "do not overwrite me" {
		t.Fatalf("the occupying file was overwritten: %q %v", string(data), err)
	}
	// Everything outside the root is untouched.
	outsideEntries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(outsideEntries) != 1 || outsideEntries[0].Name() != "untouchable.pdf" {
		t.Fatalf("something outside the folder changed: %v", outsideEntries)
	}
	// Every filed path is inside the configured root.
	filed, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range filed {
		full := filepath.Join(destination, entry.Name())
		if !withinRoot(root, full) {
			t.Fatalf("%q is outside the configured folder", full)
		}
		info, err := os.Lstat(full)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("a symlink was created at %q", full)
		}
	}
}

// Metric 4: metadata-only mode performs zero file-content reads and sends zero
// file-content model payloads.
//
// The strongest available proof: make every file unreadable, then run the whole
// pipeline. Anything that opened a file would fail.
func TestContract_MetadataOnlyOpensNoFiles(t *testing.T) {
	service, root := configuredService(t)
	service.SetMover(&realMover{})
	provider := &recordingProvider{name: "ShouldNotBeCalled"}
	service.SetClassificationProvider(provider)

	names := []string{"report.pdf", "mystery.qqq", "notes.txt"}
	for _, name := range names {
		agedFile(t, root, name, 200)
		if err := os.Chmod(filepath.Join(root, name), 0o200); err != nil {
			t.Skipf("chmod unavailable: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, name := range names {
			_ = os.Chmod(filepath.Join(root, name), 0o600)
		}
	})

	batch, created, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created {
		t.Fatalf("a scan must not need to read file contents: created=%v err=%v", created, err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("metadata-only mode sent %d model payloads", len(provider.requests))
	}

	// Approving and applying likewise never open a file.
	_, candidates, err := service.BatchDetail("ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := approveAndConfirm(t, service, candidates, "documents")
	if result.Applied != len(names) {
		t.Fatalf("the whole pipeline must work without reading contents: %+v", result)
	}
	if len(provider.requests) != 0 {
		t.Fatal("no model payload may be sent in metadata-only mode")
	}
}

// Metric 5: in-progress and partial-download fixtures are never proposed or
// mutated before settling.
func TestContract_PartialAndUnsettledFilesAreNeverProposed(t *testing.T) {
	service, root := configuredService(t)
	service.SetMover(&realMover{})

	// Partial downloads, by extension.
	for _, name := range []string{
		"movie.mp4.crdownload", "archive.zip.part", "installer.dmg.partial",
		"song.mp3.download", "book.pdf.opdownload",
	} {
		agedFile(t, root, name, 100)
	}
	// And a file written right now: no history, fresh timestamp.
	writeFile(t, filepath.Join(root, "downloading.iso"), 100)

	report, err := service.TestScan("ws-1")
	if err != nil {
		t.Fatalf("TestScan: %v", err)
	}
	if report.EligibleCount != 0 {
		t.Fatalf("nothing unsettled may be proposed: %+v", report.Eligible)
	}
	if _, created, err := service.ScanNow("ws-1", ScanSourceManual); err != nil || created {
		t.Fatalf("no batch may be created from unsettled files: created=%v err=%v", created, err)
	}

	// Every file is still exactly where it was.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 7 { // 6 files + the Filed folder
		t.Fatalf("the folder changed: %d entries", len(entries))
	}
}

// Metric 6: a burst of 100 watcher events creates no more than one active scan
// and one coalesced follow-up. (Asserted directly in automation_test.go; this
// restates it as a contract so the metric is checkable in one place.)
func TestContract_HundredEventsYieldAtMostTwoScans(t *testing.T) {
	automation, service, root, _, _ := automationFixture(t)
	agedFile(t, root, "report.pdf", 100)

	var mu sync.Mutex
	scans := 0
	release := make(chan struct{})
	realScan := service.ScanNow
	automation.scan = func(workspaceID string, source ScanSource) (JanitorBatch, bool, error) {
		mu.Lock()
		scans++
		first := scans == 1
		mu.Unlock()
		if first {
			<-release
		}
		return realScan(workspaceID, source)
	}

	automation.RunCoalescedScan("ws-1", ScanSourceWatcher)
	for range 100 {
		automation.RunCoalescedScan("ws-1", ScanSourceWatcher)
	}
	close(release)
	waitForIdle(t, automation, "ws-1")

	mu.Lock()
	defer mu.Unlock()
	if scans > 2 {
		t.Fatalf("100 events produced %d scans, want at most 2", scans)
	}
}

func waitForIdle(t *testing.T, automation *Automation, workspaceID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		automation.mu.Lock()
		running := automation.running[workspaceID]
		automation.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scans did not settle")
}

// Metric 9: repeating an apply or undo request never performs the same
// operation twice.
func TestContract_RepeatedApplyAndUndoAreIdempotent(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	mover := &realMover{}
	service.SetMover(mover)

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: moveItems(candidates, ""),
	}

	// Five identical confirms.
	var first ApplyResult
	for i := range 5 {
		result, err := service.ConfirmMoves(context.Background(), request)
		if err != nil {
			t.Fatalf("confirm %d: %v", i, err)
		}
		if i == 0 {
			first = result
		}
	}
	if mover.calls != 1 {
		t.Fatalf("the move ran %d times, want exactly 1", mover.calls)
	}
	actions, _ := service.ListActions("ws-1")
	if len(actions) != 1 {
		t.Fatalf("%d journal entries for one approved move", len(actions))
	}
	entries, err := os.ReadDir(filepath.Join(root, "Filed", "Documents"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the file exists %d times in the destination", len(entries))
	}

	// Five identical undos.
	actionID := first.Outcomes[0].ActionID
	undone := 0
	for range 5 {
		if _, err := service.Undo(context.Background(), "ws-1", actionID, "user-1"); err == nil {
			undone++
		}
	}
	if undone != 1 {
		t.Fatalf("%d undos succeeded, want exactly 1", undone)
	}
	if _, err := os.Stat(filepath.Join(root, "report.pdf")); err != nil {
		t.Fatalf("the file should be back exactly once: %v", err)
	}
}

// Metric 12: no code path exposes permanent deletion, and no agent-facing tool
// list contains a generic mutation tool for the Downloads root.
func TestContract_NoPermanentDeletionAnywhere(t *testing.T) {
	// The operation vocabulary has exactly two members, neither destructive.
	if len(ValidOperations) != 2 {
		t.Fatalf("operations = %v", ValidOperations)
	}
	for _, operation := range ValidOperations {
		lowered := strings.ToLower(string(operation))
		for _, banned := range []string{"delete", "remove", "unlink", "erase", "purge"} {
			if strings.Contains(lowered, banned) {
				t.Fatalf("operation %q is destructive", operation)
			}
		}
	}

	// The agent's binding grants only the four read tools.
	service, workspaces := newTestService(t)
	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatal(err)
	}
	binding, found := janitorBinding(workspaces.workspaces["ws-1"])
	if !found || binding.AllowsAllTools() {
		t.Fatal("the Janitor binding must exist and be restricted")
	}
	for _, tool := range binding.AllowedTools {
		if !strings.HasPrefix(tool, "list_") && !strings.HasPrefix(tool, "search_") && !strings.HasPrefix(tool, "get_") {
			t.Fatalf("tool %q is not a read tool", tool)
		}
	}
}

// ------------------------------------------------------- restart boundaries

// Restart at each boundary must never repeat a mutation or report an
// unverified success. A "restart" here is a fresh service over the same
// on-disk state, which is exactly what the process does when it starts.
func TestRestart_AtEveryBoundaryNeverRepeatsOrInvents(t *testing.T) {
	t.Run("after proposal", func(t *testing.T) {
		service, _, candidates := reviewFixture(t, "report.pdf")
		restarted := restartService(t, service)

		_, stored, err := restarted.LatestPendingBatchCandidates("ws-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) != 1 || stored[0].ID != candidates[0].ID {
			t.Fatalf("the proposal must survive: %+v", stored)
		}
		if stored[0].State != CandidatePending {
			t.Fatalf("state = %q, want pending", stored[0].State)
		}
	})

	t.Run("after approval, before apply", func(t *testing.T) {
		service, root, candidates := reviewFixture(t, "report.pdf")
		preview, err := service.PreviewMoves(PreviewRequest{
			WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
		})
		if err != nil {
			t.Fatal(err)
		}

		// The process restarts holding an unspent approval.
		restarted := restartService(t, service)
		mover := &realMover{}
		restarted.SetMover(mover)

		// Nothing moved on its own.
		if _, err := os.Stat(filepath.Join(root, "report.pdf")); err != nil {
			t.Fatalf("an unspent approval must not act by itself: %v", err)
		}
		// And the approval still works, once.
		result, err := restarted.ConfirmMoves(context.Background(), ConfirmRequest{
			WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
			Token: preview.Token, Items: moveItems(candidates, ""),
		})
		if err != nil || result.Applied != 1 {
			t.Fatalf("the approval should survive a restart: %+v %v", result, err)
		}
		if mover.calls != 1 {
			t.Fatalf("mover calls = %d", mover.calls)
		}
	})

	t.Run("interrupted mid-apply", func(t *testing.T) {
		service, root, candidates := reviewFixture(t, "report.pdf")
		// A mover that journals, moves, and then the process dies before the
		// outcome is recorded: the action is left in "applying".
		service.SetMover(&realMover{})
		preview, err := service.PreviewMoves(PreviewRequest{
			WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
			WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
			Token: preview.Token, Items: moveItems(candidates, ""),
		}); err != nil {
			t.Fatal(err)
		}
		// Simulate the crash by rewinding the journal entry to "applying".
		if _, err := service.store.UpdateScanState("ws-1", func(state *ScanState) error {
			state.Actions[0].Result = ResultApplying
			state.Actions[0].CompletedAt = time.Time{}
			return nil
		}); err != nil {
			t.Fatal(err)
		}

		restarted := restartService(t, service)
		actions, err := restarted.ListActions("ws-1")
		if err != nil {
			t.Fatal(err)
		}
		// The record says an action was in flight — which is the point of
		// journaling before mutating. It is not reported as a success.
		if actions[0].Result != ResultApplying {
			t.Fatalf("an interrupted action must remain distinguishable: %q", actions[0].Result)
		}
		if actions[0].Undoable() {
			t.Fatal("an unverified action must not be offered for undo")
		}
		// The filesystem shows what actually happened, and the file is there
		// exactly once.
		filed, err := os.ReadDir(filepath.Join(root, "Filed", "Documents"))
		if err != nil {
			t.Fatal(err)
		}
		if len(filed) != 1 {
			t.Fatalf("the file exists %d times", len(filed))
		}
	})

	t.Run("after apply, before undo", func(t *testing.T) {
		service, root, candidates := reviewFixture(t, "report.pdf")
		service.SetMover(&realMover{})
		result := approveAndConfirm(t, service, candidates, "")

		restarted := restartService(t, service)
		restarted.SetMover(&realMover{})
		undo, err := restarted.Undo(context.Background(), "ws-1", result.Outcomes[0].ActionID, "user-1")
		if err != nil {
			t.Fatalf("undo should survive a restart: %v", err)
		}
		if undo.Result != "undone" {
			t.Fatalf("undo = %+v", undo)
		}
		if _, err := os.Stat(filepath.Join(root, "report.pdf")); err != nil {
			t.Fatalf("the file should be back: %v", err)
		}
	})

	t.Run("after trash, before restore", func(t *testing.T) {
		service, root, candidates := reviewFixture(t, "ad.png")
		trash := newFakeTrash(t)
		service.SetTrash(trash)
		service.SetMover(&realMover{})
		result := approveAndConfirmItems(t, service, trashItems(candidates))

		restarted := restartService(t, service)
		restarted.SetTrash(trash)
		restarted.SetMover(&realMover{})
		undo, err := restarted.Undo(context.Background(), "ws-1", result.Outcomes[0].ActionID, "user-1")
		if err != nil {
			t.Fatalf("restore should survive a restart: %v", err)
		}
		if undo.Result != "undone" {
			t.Fatalf("undo = %+v", undo)
		}
		if _, err := os.Stat(filepath.Join(root, "ad.png")); err != nil {
			t.Fatalf("the file should be restored: %v", err)
		}
	})
}

// restartService builds a fresh service over the same on-disk state and
// workspace store, which is what a process restart amounts to.
func restartService(t *testing.T, service *Service) *Service {
	t.Helper()
	fresh := NewService(service.store, service.workspaces)
	fresh.SetTrash(service.trash)
	return fresh
}

// Contract: settings and scan state survive a restart intact, including
// pending decisions the user already made.
func TestRestart_PreservesSettingsAndPendingDecisions(t *testing.T) {
	service, root, candidates := reviewFixture(t, "a.pdf", "b.pdf")
	if _, err := service.ApplyDecisions("ws-1", []DecisionUpdate{
		{CandidateID: candidates[0].ID, Decision: DecisionMove, Category: "archives"},
	}); err != nil {
		t.Fatal(err)
	}

	restarted := restartService(t, service)
	status, err := restarted.Status("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Settings.RootPath != filepath.Clean(root) || !status.Settings.IsSetUp() {
		t.Fatalf("settings did not survive: %+v", status.Settings)
	}
	_, stored, err := restarted.LatestPendingBatchCandidates("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range stored {
		if candidate.ID == candidates[0].ID {
			if candidate.Decision != DecisionMove || candidate.EffectiveCategory() != CategoryArchives {
				t.Fatalf("the user's decision did not survive: %+v", candidate)
			}
		}
	}
}
