package downloadsjanitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRootSwap_ApprovedFolderReplacedByASymlinkIsRefused is the FR-142
// action-time check, exercised against the attack it exists for.
//
// After setup, the approved directory is replaced by a symlink pointing at a
// different folder. Both stored paths still agree — settings.RootPath and the
// directory reference are unchanged — so a string comparison alone would pass
// and os.ReadDir would follow the link. The result would be Ori scanning, and
// then MOVING, files in a folder the user never approved.
//
// Re-resolving the root at action time is what catches it, and because every
// operation resolves through the same function, one check covers scan, apply,
// Trash, restore, and undo.
func TestRootSwap_ApprovedFolderReplacedByASymlinkIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	store, _ := newTestStore(t)
	service := NewService(store, newFakeWorkspaceStore("ws-1"))
	service.SetMover(&realMover{})
	service.SetTrash(newFakeTrash(t))

	base := tempDirCanonical(t)
	approved := mkdir(t, filepath.Join(base, "Downloads"))
	elsewhere := mkdir(t, filepath.Join(base, "Elsewhere"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: approved}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	// Scanning works while the folder is genuinely the approved one.
	agedFile(t, approved, "before.pdf", 100)
	if _, _, err := service.ScanNow("ws-1", ScanSourceManual); err != nil {
		t.Fatalf("precondition scan: %v", err)
	}

	// Now swap the approved directory for a link to somewhere else, leaving a
	// file behind that must never be touched.
	agedFile(t, elsewhere, "not-yours.pdf", 100)
	if err := os.RemoveAll(approved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, approved); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// The stored paths are untouched and still agree with each other.
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.RootPath != approved {
		t.Fatalf("precondition: the stored root should be unchanged, got %q", settings.RootPath)
	}

	// Every operation that reaches the filesystem must refuse.
	if _, _, err := service.ScanNow("ws-1", ScanSourceManual); !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("scan followed the swapped folder: %v", err)
	}
	if _, err := service.TestScan("ws-1"); !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("test scan followed the swapped folder: %v", err)
	}

	// And the file in the other folder was never seen, let alone moved.
	if _, statErr := os.Stat(filepath.Join(elsewhere, "not-yours.pdf")); statErr != nil {
		t.Fatalf("a file outside the approved folder was touched: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(elsewhere, DefaultFilingRootName)); statErr == nil {
		t.Fatal("Ori created its filing tree in a folder the user never approved")
	}
}

// TestRootSwap_ApplyRefusesAfterTheFolderIsSwapped covers the mutation path
// specifically: an approval issued while the folder was genuine must not be
// spendable against a folder that has since been replaced.
func TestRootSwap_ApplyRefusesAfterTheFolderIsSwapped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	store, _ := newTestStore(t)
	service := NewService(store, newFakeWorkspaceStore("ws-1"))
	service.SetMover(&realMover{})
	service.SetTrash(newFakeTrash(t))

	base := tempDirCanonical(t)
	approved := mkdir(t, filepath.Join(base, "Downloads"))
	elsewhere := mkdir(t, filepath.Join(base, "Elsewhere"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: approved}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	agedFile(t, approved, "invoice.pdf", 120)

	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	candidateID := batch.CandidateIDs[0]
	if _, err := service.ApplyDecisions("ws-1", []DecisionUpdate{
		{CandidateID: candidateID, Decision: DecisionMove},
	}); err != nil {
		t.Fatalf("ApplyDecisions: %v", err)
	}
	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1",
		UserID:      "local",
		Items:       []PreviewRequestItem{{CandidateID: candidateID, Operation: OperationMove}},
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}

	// The folder is swapped between approval and apply.
	if err := os.RemoveAll(approved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, approved); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err = service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1",
		UserID:      "local",
		BatchID:     batch.ID,
		Token:       preview.Token,
		Items:       []PreviewRequestItem{{CandidateID: candidateID, Operation: OperationMove}},
	})
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("apply proceeded against a swapped folder: %v", err)
	}

	entries, readErr := os.ReadDir(elsewhere)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("apply wrote into a folder the user never approved: %v", entries)
	}
}

// TestResolveRoot_UnchangedFolderStillResolves is the negative control: the
// re-resolution must not break the ordinary case, where the stored canonical
// path resolves to itself.
func TestResolveRoot_UnchangedFolderStillResolves(t *testing.T) {
	service, root := configuredService(t)

	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	resolved, err := service.scannerFor().ResolveRoot(settings)
	if err != nil {
		t.Fatalf("ResolveRoot on an untouched folder: %v", err)
	}
	if resolved != root {
		t.Fatalf("resolved = %q, want %q", resolved, root)
	}
}
