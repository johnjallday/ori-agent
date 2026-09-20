package filejanitor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanNowOperationPersistsNoEligibleReceiptAndReplays(t *testing.T) {
	service, _ := newTestService(t)
	root := inboxFixture(t)
	status := assistedGrant(t, service, root, FolderGrantOperation{RunID: "run-1", OperationID: "folder-op-1"})
	request := ScanOperationRequest{OperationID: "scan-op-1", RootID: status.Settings.RootID, PrivacyMode: ContentModeMetadataOnly}
	first, err := service.ScanNowOperation("ws-1", ScanSourceAssistantSetup, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Outcome != ScanOutcomeNoEligible || first.BatchID != "" || first.CompletedAt.IsZero() {
		t.Fatalf("receipt = %+v", first)
	}
	replayed, err := service.ScanNowOperation("ws-1", ScanSourceAssistantSetup, request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.OperationID != first.OperationID || replayed.Outcome != first.Outcome ||
		replayed.BatchID != first.BatchID || !replayed.CompletedAt.Equal(first.CompletedAt) {
		t.Fatalf("replay = %+v; want %+v", replayed, first)
	}
	batches, err := service.ListBatches("ws-1")
	if err != nil || len(batches) != 0 {
		t.Fatalf("batches = %+v, err = %v", batches, err)
	}
}

func TestScanNowOperationCommitsBatchAndReceiptAtomically(t *testing.T) {
	service, _ := newTestService(t)
	root := inboxFixture(t)
	file := filepath.Join(root, "statement.pdf")
	if err := os.WriteFile(file, []byte("metadata only"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	status := assistedGrant(t, service, root, FolderGrantOperation{RunID: "run-1", OperationID: "folder-op-1"})
	request := ScanOperationRequest{OperationID: "scan-op-1", RootID: status.Settings.RootID, PrivacyMode: ContentModeMetadataOnly}
	first, err := service.ScanNowOperation("ws-1", ScanSourceAssistantSetup, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Outcome != ScanOutcomeBatch || first.BatchID == "" || first.EligibleCount != 1 {
		t.Fatalf("receipt = %+v", first)
	}
	replayed, err := service.ScanNowOperation("ws-1", ScanSourceAssistantSetup, request)
	if err != nil || replayed.OperationID != first.OperationID || replayed.Outcome != first.Outcome ||
		replayed.BatchID != first.BatchID || !replayed.CompletedAt.Equal(first.CompletedAt) {
		t.Fatalf("replay = %+v, err = %v", replayed, err)
	}
	batches, err := service.ListBatches("ws-1")
	if err != nil || len(batches) != 1 || batches[0].ID != first.BatchID {
		t.Fatalf("batches = %+v, err = %v", batches, err)
	}
}

func TestScanNowOperationRejectsChangedAuthority(t *testing.T) {
	service, _ := newTestService(t)
	root := inboxFixture(t)
	status := assistedGrant(t, service, root, FolderGrantOperation{RunID: "run-1", OperationID: "folder-op-1"})
	cases := []ScanOperationRequest{
		{OperationID: "scan-op-1", RootID: "other-root", PrivacyMode: ContentModeMetadataOnly},
		{OperationID: "scan-op-2", RootID: status.Settings.RootID, PrivacyMode: ContentModeLocalModel},
	}
	for _, request := range cases {
		if _, err := service.ScanNowOperation("ws-1", ScanSourceAssistantSetup, request); !errors.Is(err, ErrScanOperationConflict) {
			t.Fatalf("request %+v err = %v", request, err)
		}
	}
	batches, _ := service.ListBatches("ws-1")
	if len(batches) != 0 {
		t.Fatalf("rejected scans created batches: %+v", batches)
	}
}
