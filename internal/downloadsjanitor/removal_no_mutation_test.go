package downloadsjanitor

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// treeSnapshot records every path under root with its size, so a test can prove
// nothing at all changed on disk.
type treeSnapshot map[string]int64

func snapshotTree(t *testing.T, root string) treeSnapshot {
	t.Helper()
	out := treeSnapshot{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			out[relative+"/"] = -1
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		out[relative] = info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func (s treeSnapshot) paths() []string {
	out := make([]string, 0, len(s))
	for path := range s {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func assertTreeUnchanged(t *testing.T, before, after treeSnapshot) {
	t.Helper()
	beforePaths, afterPaths := before.paths(), after.paths()
	if len(beforePaths) != len(afterPaths) {
		t.Fatalf("the folder changed:\n  before %v\n  after  %v", beforePaths, afterPaths)
	}
	for i, path := range beforePaths {
		if afterPaths[i] != path {
			t.Fatalf("path %q became %q", path, afterPaths[i])
		}
		if before[path] != after[path] {
			t.Errorf("%s size %d -> %d", path, before[path], after[path])
		}
	}
}

// Removing File Janitor must not touch a single byte of the user's files.
//
// This is the property that matters most in the whole uninstall: the user is
// removing something that has been MOVING their files, and the one thing they
// need to be sure of is that removing it does not move any more. Asserting on
// the whole tree — not just "the root still exists" — is what makes a
// well-meaning future cleanup step ("tidy up the empty Filed folders") fail
// here instead of on someone's machine (FR-28).
func TestCapabilityRemoval_TouchesNoFiles(t *testing.T) {
	service, _ := newTestService(t)
	root := filepath.Join(tempDirCanonical(t), "Inbox")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	// A realistic folder: files still waiting, and files already filed.
	writeAged(t, root, "invoice.pdf", "invoice")
	writeAged(t, root, "photo.png", "photo")
	filed := filepath.Join(root, "Filed", "Documents")
	if err := os.MkdirAll(filed, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filed, "already-filed.pdf"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ScanNow("ws-1", ScanSourceManual); err != nil {
		t.Fatalf("ScanNow: %v", err)
	}

	before := snapshotTree(t, root)

	runtime := NewCapabilityRuntime(service)
	if err := runtime.StopCapabilityAutomation("ws-1"); err != nil {
		t.Fatalf("StopCapabilityAutomation: %v", err)
	}
	if err := runtime.OnCapabilityRemove("ws-1"); err != nil {
		t.Fatalf("OnCapabilityRemove: %v", err)
	}

	assertTreeUnchanged(t, before, snapshotTree(t, root))
}

// Removal strips the ACCESS, not the record of what was done with it.
//
// The journal is the user's evidence of every file Ori moved or trashed on
// their behalf. Uninstalling does not make that history untrue, and someone
// removing the capability because they are unhappy with a move is exactly the
// person who most needs it to survive (FR-29, FR-113).
func TestCapabilityRemoval_KeepsTheAuditTrailAndDropsTheGrant(t *testing.T) {
	service, _ := newTestService(t)
	// A real mover, because the point is that the journal of REAL moves
	// survives removal.
	service.SetMover(&realMover{})
	root := filepath.Join(tempDirCanonical(t), "Inbox")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	writeAged(t, root, "invoice.pdf", "invoice")
	if _, _, err := service.ScanNow("ws-1", ScanSourceManual); err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	_, candidates, err := service.LatestPendingBatchCandidates("ws-1")
	if err != nil {
		t.Fatalf("latest batch: %v", err)
	}
	approveAndConfirm(t, service, candidates, "documents")

	historyBefore, err := service.ListActions("ws-1")
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(historyBefore) == 0 {
		t.Fatal("fixture recorded no history, so the retention claim would be untested")
	}

	runtime := NewCapabilityRuntime(service)
	if err := runtime.OnCapabilityRemove("ws-1"); err != nil {
		t.Fatalf("OnCapabilityRemove: %v", err)
	}

	// The grant is gone: nothing can scan or move after removal.
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.IsSetUp() {
		t.Error("removal must leave no active folder grant")
	}
	if settings.RootPath != "" || settings.DirectoryReferenceID != "" {
		t.Errorf("active access survived removal: root=%q ref=%q",
			settings.RootPath, settings.DirectoryReferenceID)
	}

	// The record of what it did is intact.
	historyAfter, err := service.ListActions("ws-1")
	if err != nil {
		t.Fatalf("ListActions after removal: %v", err)
	}
	if len(historyAfter) != len(historyBefore) {
		t.Fatalf("history entries = %d, want %d — removal must not erase the audit trail",
			len(historyAfter), len(historyBefore))
	}
	for i := range historyBefore {
		if historyAfter[i].ID != historyBefore[i].ID ||
			historyAfter[i].SourceName != historyBefore[i].SourceName ||
			historyAfter[i].DestinationRelative != historyBefore[i].DestinationRelative {
			t.Errorf("history entry %d changed during removal", i)
		}
	}
}

// A scan after removal must find nothing to do, because there is no folder.
// Retained state is for reading, never for powering work (FR-29).
func TestCapabilityRemoval_RetainedStateCannotPowerAScan(t *testing.T) {
	service, _ := newTestService(t)
	root := filepath.Join(tempDirCanonical(t), "Inbox")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	writeAged(t, root, "invoice.pdf", "invoice")

	runtime := NewCapabilityRuntime(service)
	if err := runtime.OnCapabilityRemove("ws-1"); err != nil {
		t.Fatalf("OnCapabilityRemove: %v", err)
	}

	if _, _, err := service.ScanNow("ws-1", ScanSourceManual); err == nil {
		t.Fatal("a scan after removal must fail rather than reach the old folder")
	}
	before := snapshotTree(t, root)
	_, _, _ = service.ScanNow("ws-1", ScanSourceManual)
	assertTreeUnchanged(t, before, snapshotTree(t, root))
}
