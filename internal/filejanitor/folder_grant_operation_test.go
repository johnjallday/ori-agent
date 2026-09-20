package filejanitor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func assistedGrant(t *testing.T, service *Service, root string, operation FolderGrantOperation) Status {
	t.Helper()
	paused := true
	status, err := service.ConfirmSetup(SetupRequest{
		WorkspaceID: "ws-1", Path: root, Paused: &paused, Operation: &operation,
	})
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestAssistedFolderGrantPersistsReceiptAndReplaysWithoutChangingResources(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)
	operation := FolderGrantOperation{RunID: "run-1", OperationID: "folder-op-1"}
	first := assistedGrant(t, service, root, operation)
	if !first.Settings.Paused || first.Settings.RootID == "" || first.Settings.DirectoryReferenceID == "" {
		t.Fatalf("settings = %+v", first.Settings)
	}
	if first.Settings.PendingFolderGrant != nil || first.Settings.LastFolderGrant == nil ||
		first.Settings.LastFolderGrant.OperationID != operation.OperationID {
		t.Fatalf("grant receipts = pending %+v final %+v", first.Settings.PendingFolderGrant, first.Settings.LastFolderGrant)
	}
	if _, err := os.Stat(filepath.Join(root, DefaultFilingRootName)); err != nil {
		t.Fatalf("Filed was not created: %v", err)
	}
	refs := len(workspaces.workspaces["ws-1"].DirectoryReferences)
	bindings := len(workspaces.workspaces["ws-1"].MCPBindings)

	replayed := assistedGrant(t, service, root, operation)
	if replayed.Settings.RootID != first.Settings.RootID || replayed.Settings.DirectoryReferenceID != first.Settings.DirectoryReferenceID {
		t.Fatalf("replay changed identity: %+v then %+v", first.Settings, replayed.Settings)
	}
	if len(workspaces.workspaces["ws-1"].DirectoryReferences) != refs || len(workspaces.workspaces["ws-1"].MCPBindings) != bindings {
		t.Fatal("replay duplicated workspace access")
	}
}

func TestAssistedFolderGrantRejectsDifferentPathForSameOperation(t *testing.T) {
	service, _ := newTestService(t)
	first := inboxFixture(t)
	second := filepath.Join(tempDirCanonical(t), "Other")
	if err := os.MkdirAll(second, 0o750); err != nil {
		t.Fatal(err)
	}
	operation := FolderGrantOperation{RunID: "run-1", OperationID: "folder-op-1"}
	assistedGrant(t, service, first, operation)
	paused := true
	_, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: second, Paused: &paused, Operation: &operation})
	var setupError *SetupError
	if !errors.As(err, &setupError) || setupError.Code != CodeFolderChanged {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, DefaultFilingRootName)); !os.IsNotExist(err) {
		t.Fatalf("rejected folder was mutated: %v", err)
	}
}

func TestAssistedFolderGrantWritesPendingMarkerBeforeWorkspaceOrFilesystemConsequences(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)
	workspaces.updateErr = errors.New("injected workspace write failure")
	paused := true
	_, err := service.ConfirmSetup(SetupRequest{
		WorkspaceID: "ws-1", Path: root, Paused: &paused,
		Operation: &FolderGrantOperation{RunID: "run-1", OperationID: "folder-op-1"},
	})
	if err == nil {
		t.Fatal("expected injected failure")
	}
	settings, loadErr := service.store.LoadSettings("ws-1")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if settings.PendingFolderGrant == nil || settings.PendingFolderGrant.OperationID != "folder-op-1" || settings.LastFolderGrant != nil {
		t.Fatalf("settings = %+v", settings)
	}
	if _, statErr := os.Stat(filepath.Join(root, DefaultFilingRootName)); statErr != nil {
		t.Fatalf("pending marker was not durable before Filed consequence: %v", statErr)
	}
	workspaces.updateErr = nil
	status := assistedGrant(t, service, root, FolderGrantOperation{RunID: "run-1", OperationID: "folder-op-1"})
	if status.Settings.PendingFolderGrant != nil || status.Settings.LastFolderGrant == nil ||
		status.Settings.LastFolderGrant.RootID != settings.PendingFolderGrant.RootID {
		t.Fatalf("retry did not reconcile pending grant: %+v", status.Settings)
	}
}

func TestAssistedFolderGrantNeverResetsCustomPrivacy(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.store.UpdateSettings("ws-1", func(settings *JanitorSettings) error {
		settings.ContentMode = ContentModeLocalModel
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	root := inboxFixture(t)
	paused := true
	_, err := service.ConfirmSetup(SetupRequest{
		WorkspaceID: "ws-1", Path: root, Paused: &paused,
		Operation: &FolderGrantOperation{RunID: "run-1", OperationID: "folder-op-1"},
	})
	var setupError *SetupError
	if !errors.As(err, &setupError) || setupError.Code != CodePrivacyReviewRequired {
		t.Fatalf("err = %v", err)
	}
	settings, _ := service.store.LoadSettings("ws-1")
	if settings.ContentMode != ContentModeLocalModel || settings.PendingFolderGrant != nil {
		t.Fatalf("privacy was changed: %+v", settings)
	}
	if _, statErr := os.Stat(filepath.Join(root, DefaultFilingRootName)); !os.IsNotExist(statErr) {
		t.Fatalf("privacy refusal mutated folder: %v", statErr)
	}
}
