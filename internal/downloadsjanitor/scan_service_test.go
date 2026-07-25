package downloadsjanitor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// configuredService returns a service whose workspace is already set up against
// an isolated inbox folder, plus that folder's path.
func configuredService(t *testing.T) (*Service, string) {
	t.Helper()
	store, _ := newTestStore(t)
	workspaces := newFakeWorkspaceStore("ws-1", "ws-2")
	service := NewService(store, workspaces)

	root := filepath.Join(t.TempDir(), "Inbox")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	return service, root
}

// agedFile writes a file and backdates it past the settle interval, standing in
// for a download that finished a while ago.
func agedFile(t *testing.T, root, name string, size int) {
	t.Helper()
	path := filepath.Join(root, name)
	writeFile(t, path, size)
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestTestScan_ReportsWithoutCreatingAnything(t *testing.T) {
	service, root := configuredService(t)
	agedFile(t, root, "report.pdf", 100)
	agedFile(t, root, "mystery.qqq", 10)
	agedFile(t, root, "movie.mp4.crdownload", 50)

	report, err := service.TestScan("ws-1")
	if err != nil {
		t.Fatalf("TestScan: %v", err)
	}
	if report.EligibleCount != 2 {
		t.Fatalf("eligible = %d (%+v)", report.EligibleCount, report.Eligible)
	}
	if report.NeedsReviewCount != 1 {
		t.Fatalf("needs review = %d, want the unrecognized file", report.NeedsReviewCount)
	}
	if report.IneligibleCount == 0 {
		t.Fatal("the partial download should be reported as ineligible")
	}

	// Nothing was created: no batch, no candidate, and — importantly — not even
	// a settling observation, so a test scan cannot change what a later real
	// scan decides.
	state, err := service.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Batches) != 0 || len(state.Candidates) != 0 {
		t.Fatalf("a test scan must create no records: %+v", state)
	}
	if len(state.Observations) != 0 {
		t.Fatalf("a test scan must not write observations: %+v", state.Observations)
	}
}

func TestScanNow_PersistsOneReviewableBatch(t *testing.T) {
	service, root := configuredService(t)
	agedFile(t, root, "report.pdf", 100)
	agedFile(t, root, "photo.png", 200)

	batch, created, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	if !created {
		t.Fatal("expected a batch to be created")
	}
	if batch.Source != ScanSourceManual || batch.Summary.Proposed != 2 {
		t.Fatalf("batch = %+v", batch)
	}

	stored, candidates, err := service.BatchDetail("ws-1", batch.ID)
	if err != nil {
		t.Fatalf("BatchDetail: %v", err)
	}
	if len(candidates) != 2 || stored.ID != batch.ID {
		t.Fatalf("batch did not persist: %+v %+v", stored, candidates)
	}
	for _, candidate := range candidates {
		if candidate.Category == "" || candidate.Reason == "" {
			t.Fatalf("candidate should carry a classification: %+v", candidate)
		}
		// Every candidate arrives undecided: opening a batch cannot move a file.
		if candidate.Decision != DecisionNone || candidate.State != CandidatePending {
			t.Fatalf("candidate must start pending and undecided: %+v", candidate)
		}
	}
}

func TestScanNow_DoesNotRepeatOrCreateEmptyBatches(t *testing.T) {
	service, root := configuredService(t)
	agedFile(t, root, "report.pdf", 100)

	if _, created, err := service.ScanNow("ws-1", ScanSourceManual); err != nil || !created {
		t.Fatalf("first scan: created=%v err=%v", created, err)
	}
	// Same unchanged file: nothing new to review, so no second batch.
	batch, created, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	if created {
		t.Fatalf("an unchanged folder must not create a second batch: %+v", batch)
	}
	batches, err := service.ListBatches("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 {
		t.Fatalf("expected exactly one batch, got %d", len(batches))
	}
}

func TestTestScanAndScanNow_ShareTheSameEligibilityRules(t *testing.T) {
	service, root := configuredService(t)
	names := []string{"report.pdf", ".hidden.pdf", "draft.tmp", "archive.zip.part", "photo.png"}
	for _, name := range names {
		agedFile(t, root, name, 10)
	}
	if err := os.MkdirAll(filepath.Join(root, "subfolder"), 0o750); err != nil {
		t.Fatal(err)
	}

	report, err := service.TestScan("ws-1")
	if err != nil {
		t.Fatalf("TestScan: %v", err)
	}
	batch, created, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created {
		t.Fatalf("ScanNow: created=%v err=%v", created, err)
	}
	_, candidates, err := service.BatchDetail("ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Eligible) != len(candidates) {
		t.Fatalf("test scan saw %d eligible, real scan proposed %d", len(report.Eligible), len(candidates))
	}
	for i := range candidates {
		if report.Eligible[i].Name != candidates[i].Name {
			t.Fatalf("entry %d: test scan %q vs real scan %q", i, report.Eligible[i].Name, candidates[i].Name)
		}
		if report.Eligible[i].Category != candidates[i].Category {
			t.Fatalf("%s: test scan %s vs real scan %s", candidates[i].Name, report.Eligible[i].Category, candidates[i].Category)
		}
	}
}

func TestScanNow_RequiresCompletedSetup(t *testing.T) {
	store, _ := newTestStore(t)
	service := NewService(store, newFakeWorkspaceStore("ws-1"))

	if _, _, err := service.ScanNow("ws-1", ScanSourceManual); err == nil {
		t.Fatal("scanning must be gated on setup")
	}
	var setupError *SetupError
	if _, err := service.TestScan("ws-1"); !errors.As(err, &setupError) || setupError.Code != CodeNotConfigured {
		t.Fatalf("expected a not_configured setup error, got %v", err)
	}
}

func TestApplyDecisions_RecordsIntentWithoutTouchingFiles(t *testing.T) {
	service, root := configuredService(t)
	agedFile(t, root, "report.pdf", 100)
	agedFile(t, root, "ad.png", 10)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	_, candidates, err := service.BatchDetail("ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}

	var pdf, png JanitorCandidate
	for _, candidate := range candidates {
		switch candidate.Name {
		case "report.pdf":
			pdf = candidate
		case "ad.png":
			png = candidate
		}
	}

	changed, err := service.ApplyDecisions("ws-1", []DecisionUpdate{
		{CandidateID: pdf.ID, Decision: DecisionMove, Category: "archives"},
		{CandidateID: png.ID, Decision: DecisionSkip},
	})
	if err != nil {
		t.Fatalf("ApplyDecisions: %v", err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed = %d", len(changed))
	}

	// Both files are exactly where they were: a decision is intent, not action.
	for _, name := range []string{"report.pdf", "ad.png"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("recording a decision must not move %s: %v", name, err)
		}
	}

	stored, candidates, err := service.BatchDetail("ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		switch candidate.Name {
		case "report.pdf":
			if candidate.Decision != DecisionMove || candidate.EffectiveCategory() != CategoryArchives {
				t.Fatalf("move decision not recorded: %+v", candidate)
			}
		case "ad.png":
			if candidate.Decision != DecisionSkip || candidate.State != CandidateSkipped {
				t.Fatalf("skip decision not recorded: %+v", candidate)
			}
		}
	}
	if stored.Summary.Skipped != 1 || stored.Summary.Proposed != 1 {
		t.Fatalf("summary not updated with the decisions: %+v", stored.Summary)
	}
}

func TestApplyDecisions_RejectsPathsAndUnknownCategories(t *testing.T) {
	service, root := configuredService(t)
	agedFile(t, root, "report.pdf", 100)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	_, candidates, _ := service.BatchDetail("ws-1", batch.ID)
	id := candidates[0].ID

	// A category is an allowlisted ID, never a path — so none of these can name
	// a destination.
	for _, category := range []string{"../../etc", "/absolute", "Documents/../..", "receipts", "documents\x00"} {
		if _, err := service.ApplyDecisions("ws-1", []DecisionUpdate{
			{CandidateID: id, Decision: DecisionMove, Category: category},
		}); err == nil {
			t.Fatalf("category %q should have been rejected", category)
		}
	}

	// The rejected attempts left no decision behind.
	_, candidates, _ = service.BatchDetail("ws-1", batch.ID)
	if candidates[0].Decision != DecisionNone {
		t.Fatalf("a rejected decision must not persist: %+v", candidates[0])
	}
}

func TestApplyDecisions_IsWorkspaceScoped(t *testing.T) {
	service, root := configuredService(t)
	agedFile(t, root, "report.pdf", 100)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	_, candidates, _ := service.BatchDetail("ws-1", batch.ID)

	if _, err := service.ApplyDecisions("ws-2", []DecisionUpdate{
		{CandidateID: candidates[0].ID, Decision: DecisionMove},
	}); !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("another workspace must not decide this candidate, got %v", err)
	}
	if _, _, err := service.BatchDetail("ws-2", batch.ID); !errors.Is(err, ErrBatchNotFound) {
		t.Fatalf("another workspace must not read this batch, got %v", err)
	}
}

func TestSkippedItemsStayDismissedUntilResetOrChanged(t *testing.T) {
	service, root := configuredService(t)
	agedFile(t, root, "ad.png", 10)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	_, candidates, _ := service.BatchDetail("ws-1", batch.ID)
	if _, err := service.ApplyDecisions("ws-1", []DecisionUpdate{
		{CandidateID: candidates[0].ID, Decision: DecisionSkip},
	}); err != nil {
		t.Fatalf("ApplyDecisions: %v", err)
	}

	// A later scan does not bring it back.
	if _, created, err := service.ScanNow("ws-1", ScanSourceDaily); err != nil || created {
		t.Fatalf("a skipped file must not be re-proposed: created=%v err=%v", created, err)
	}

	// Resetting skipped items does.
	if err := service.ResetSkipped("ws-1", ""); err != nil {
		t.Fatalf("ResetSkipped: %v", err)
	}
	if _, created, err := service.ScanNow("ws-1", ScanSourceDaily); err != nil || !created {
		t.Fatalf("after a reset the file should be proposed again: created=%v err=%v", created, err)
	}
}

func TestScanNow_LeavesUnsettledFilesForALaterRun(t *testing.T) {
	service, root := configuredService(t)
	// A file written right now has no history and a fresh timestamp: nothing
	// yet proves it has finished being written.
	writeFile(t, filepath.Join(root, "downloading.iso"), 100)

	if _, created, err := service.ScanNow("ws-1", ScanSourceWatcher); err != nil || created {
		t.Fatalf("an unsettled file must not be proposed: created=%v err=%v", created, err)
	}

	// The first run recorded an observation, so a later run with the file
	// unchanged can conclude it settled.
	state, err := service.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Observations) != 1 {
		t.Fatalf("a real scan should record what it saw: %+v", state.Observations)
	}

	future := time.Now().Add(SettleInterval + time.Second)
	service.SetScanner(&Scanner{store: service.store, workspaces: service.workspaces, now: func() time.Time { return future }})
	if _, created, err := service.ScanNow("ws-1", ScanSourceWatcher); err != nil || !created {
		t.Fatalf("a settled file should be proposed on the next run: created=%v err=%v", created, err)
	}
}

// A backlog of files that were downloaded long before Ori was set up must be
// reviewable on the very first scan — telling the user to come back in 30
// seconds about files that have sat untouched for weeks would be absurd.
func TestScanNow_ProposesAPreexistingBacklogImmediately(t *testing.T) {
	service, root := configuredService(t)
	for _, name := range []string{"old-invoice.pdf", "old-photo.png", "old-archive.zip"} {
		agedFile(t, root, name, 20)
	}

	batch, created, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created {
		t.Fatalf("first scan of a backlog: created=%v err=%v", created, err)
	}
	if batch.Summary.Proposed != 3 {
		t.Fatalf("expected the whole backlog proposed on the first scan: %+v", batch.Summary)
	}
}

func TestLatestPendingBatch_TracksWhatStillNeedsTheUser(t *testing.T) {
	service, root := configuredService(t)
	agedFile(t, root, "report.pdf", 100)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}

	got, candidates, ok, err := service.LatestPendingBatch("ws-1")
	if err != nil || !ok || got.ID != batch.ID || len(candidates) != 1 {
		t.Fatalf("pending batch = %+v ok=%v err=%v", got, ok, err)
	}

	if _, err := service.ApplyDecisions("ws-1", []DecisionUpdate{
		{CandidateID: candidates[0].ID, Decision: DecisionSkip},
	}); err != nil {
		t.Fatalf("ApplyDecisions: %v", err)
	}
	if _, _, ok, err := service.LatestPendingBatch("ws-1"); err != nil || ok {
		t.Fatalf("a fully decided batch is no longer pending: ok=%v err=%v", ok, err)
	}
}

// The scan path must keep working when the folder disappears underneath it.
func TestScanNow_MissingFolderIsRecoverable(t *testing.T) {
	service, root := configuredService(t)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ScanNow("ws-1", ScanSourceDaily); !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("expected ErrRootUnavailable, got %v", err)
	}

	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Readiness.State != ReadinessNeedsAttention {
		t.Fatalf("readiness should report the missing folder: %+v", status.Readiness)
	}
}
