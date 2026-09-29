package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Restart classification: after a crash at each phase of a fenced Home write,
// the journal plus both independently reopened mirrors decide the outcome.
// Only exact before/before or after/after proceed; a split or a third value
// stays refused until an explicit exact restoration.

func writeTestJournal(t *testing.T, folder string, journal fenceJournal) {
	t.Helper()
	data, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, WorkspaceFenceJournalFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func journalExists(t *testing.T, folder string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(folder, WorkspaceFenceJournalFile))
	return err == nil
}

func TestSyncStoreFence_JournalClassifiesInterruptedWriteAcrossRestart(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	disk, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	home := newFencedHome("home-journal")
	if err := NewSyncStore(primary, disk).Save(home); err != nil {
		t.Fatal(err)
	}
	folder, err := disk.GetFolderPath(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(folder, WorkspaceConfigFile)
	beforeRaw, err := os.ReadFile(configPath) // #nosec G304 -- disposable test folder.
	if err != nil {
		t.Fatal(err)
	}
	crashed, _ := cloneWorkspaceForRebind(home)
	crashed.Version, crashed.Description = 2, "crashed mid-save"
	afterRaw, err := crashed.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	journal := fenceJournal{OperationID: "op-split", WorkspaceID: home.ID, BaseVersion: 1, TargetVersion: 2,
		FolderBefore: fenceDigest(beforeRaw), FolderAfter: fenceDigest(afterRaw), StartedAt: time.Now().UTC()}

	// Phase 1: the process died after the folder write and before the primary
	// write. Folder = after, primary = before.
	writeTestJournal(t, folder, journal)
	if err := os.WriteFile(configPath, afterRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sync := NewSyncStore(primary, restarted)
	attempt, _ := cloneWorkspaceForRebind(home)
	attempt.Description = "new work"
	err = sync.Save(attempt)
	if !errors.Is(err, ErrWorkspaceMirrorsDiverged) || !strings.Contains(err.Error(), "op-split") {
		t.Fatalf("split after folder write was not classified as reconcile_required: %v", err)
	}
	if got, _ := os.ReadFile(configPath); string(got) != string(afterRaw) || !journalExists(t, folder) { // #nosec G304 -- disposable test folder.
		t.Fatal("refused write touched the folder or dropped the journal")
	}

	// Phase 2: the process died after both mirror writes and before the
	// journal was closed. Folder = after, primary = after.
	applied, _ := cloneWorkspaceForRebind(crashed)
	if err := primary.Save(applied); err != nil {
		t.Fatal(err)
	}
	next, _ := cloneWorkspaceForRebind(applied)
	next.Description = "after applied"
	if err := sync.Save(next); err != nil {
		t.Fatalf("applied write was not recognized from its journal: %v", err)
	}
	if journalExists(t, folder) {
		t.Fatal("journal of an applied write was not closed")
	}
	if got, err := restarted.Get(home.ID); err != nil || got.Version != 3 || got.Description != "after applied" {
		t.Fatalf("write after applied classification did not land: %+v %v", got, err)
	}

	// Phase 3: the process died after a successful rollback and before the
	// journal was closed. Folder = before, primary = before.
	currentRaw, err := os.ReadFile(configPath) // #nosec G304 -- disposable test folder.
	if err != nil {
		t.Fatal(err)
	}
	writeTestJournal(t, folder, fenceJournal{OperationID: "op-rolled-back", WorkspaceID: home.ID,
		BaseVersion: 3, TargetVersion: 4, FolderBefore: fenceDigest(currentRaw), FolderAfter: "never-written", StartedAt: time.Now().UTC()})
	again, _ := cloneWorkspaceForRebind(next)
	again.Description = "after rollback"
	if err := sync.Save(again); err != nil {
		t.Fatalf("not_applied write blocked a fresh write: %v", err)
	}
	if journalExists(t, folder) {
		t.Fatal("journal of a rolled-back write was not closed")
	}

	// Phase 4: the folder matches neither image (a third writer). Unavailable
	// until an explicit exact restoration, which is the only thing that
	// clears the journal.
	writeTestJournal(t, folder, fenceJournal{OperationID: "op-foreign", WorkspaceID: home.ID,
		BaseVersion: 4, TargetVersion: 5, FolderBefore: "deadbeef", FolderAfter: "cafebabe", StartedAt: time.Now().UTC()})
	stuck, _ := cloneWorkspaceForRebind(again)
	stuck.Description = "must wait"
	if err := sync.Save(stuck); !errors.Is(err, ErrWorkspaceFenceUnknown) {
		t.Fatalf("unknown journal state was not refused: %v", err)
	}
	restore, _ := cloneWorkspaceForRebind(again)
	if err := restarted.RestoreMirrorRecord(restore); err != nil {
		t.Fatal(err)
	}
	if journalExists(t, folder) {
		t.Fatal("explicit restoration left the journal in place")
	}
	if err := sync.Save(stuck); err != nil {
		t.Fatalf("write refused after explicit restoration: %v", err)
	}
}

// compensationBreaker fails the primary write and, first, makes the folder
// unwritable so the fence's rollback of the folder mirror also fails.
type compensationBreaker struct {
	Store
	folder string
}

func (b *compensationBreaker) Save(*Workspace) error {
	_ = os.Chmod(b.folder, 0o500)
	return os.ErrPermission
}

func TestSyncStoreFence_FailedCompensationKeepsJournalAndFailsClosed(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	disk, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	home := newFencedHome("home-compensation")
	if err := NewSyncStore(primary, disk).Save(home); err != nil {
		t.Fatal(err)
	}
	folder, err := disk.GetFolderPath(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(folder, 0o750) })
	failing := NewSyncStore(&compensationBreaker{Store: primary, folder: folder}, disk)
	attempt, _ := cloneWorkspaceForRebind(home)
	attempt.Description = "unfinished"
	err = failing.Save(attempt)
	if err == nil || !strings.Contains(err.Error(), "folder rollback failed") {
		t.Fatalf("failed compensation was not reported: %v", err)
	}
	if err := os.Chmod(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	if !journalExists(t, folder) {
		t.Fatal("failed compensation dropped the journal that records the split")
	}
	// After a restart, the split is refused from the journal: the folder holds
	// the after image, the primary the before image.
	restarted, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sync := NewSyncStore(primary, restarted)
	retry, _ := cloneWorkspaceForRebind(home)
	retry.Description = "retry"
	if err := sync.Save(retry); !errors.Is(err, ErrWorkspaceMirrorsDiverged) {
		t.Fatalf("split after failed compensation was accepted: %v", err)
	}
	onDisk, err := restarted.Get(home.ID)
	if err != nil || onDisk.Version != 2 || onDisk.Description != "unfinished" {
		t.Fatalf("expected the folder to keep the unfinished after image: %+v %v", onDisk, err)
	}
	// Only an explicit exact restoration from the unchanged primary clears it.
	restore, _ := cloneWorkspaceForRebind(home)
	if err := restarted.RestoreMirrorRecord(restore); err != nil {
		t.Fatal(err)
	}
	if err := sync.Save(retry); err != nil {
		t.Fatalf("write refused after explicit restoration: %v", err)
	}
	if journalExists(t, folder) {
		t.Fatal("journal left behind after an applied write")
	}
}
