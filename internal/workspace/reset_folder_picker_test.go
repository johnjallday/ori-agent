package workspace

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestFolderPickerClaimsLifetimeBeforeAcknowledgement(t *testing.T) {
	gate := &resetstate.WorkGate{}
	folderPickerProcess.Lock()
	releaseFolderPickerLocked()
	if err := claimFolderPickerLocked(gate); err != nil {
		folderPickerProcess.Unlock()
		t.Fatal(err)
	}
	folderPickerProcess.Unlock()
	if snapshot := gate.Snapshot(); snapshot.Owners != 1 {
		t.Fatalf("folder picker ownership = %+v", snapshot)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	folderPickerProcess.Lock()
	releaseFolderPickerLocked()
	folderPickerProcess.Unlock()
	if snapshot := gate.Snapshot(); snapshot.Owners != 0 {
		t.Fatalf("folder picker release = %+v", snapshot)
	}
}

func TestFolderPickerCannotClaimAfterResetFence(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	folderPickerProcess.Lock()
	releaseFolderPickerLocked()
	err := claimFolderPickerLocked(gate)
	folderPickerProcess.Unlock()
	if !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal("folder picker bypassed fence:", err)
	}
}
