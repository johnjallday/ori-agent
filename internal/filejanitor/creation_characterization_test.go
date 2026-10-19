package filejanitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// These tests pin existing boundaries before the reviewed creation path is
// added. They deliberately use the canonical service without an agent/model.
func TestCreationCharacterization_LinkIsNotAJanitorGrant(t *testing.T) {
	service, workspaces, base := installedService(t)
	root := mkdir(t, filepath.Join(base, "Inbox"))
	if err := workspaces.Update("ws-1", func(ws *workspace.Workspace) error {
		return ws.AddDirectoryReference(workspace.DirectoryReference{Name: "Inbox", Path: root})
	}); err != nil {
		t.Fatal(err)
	}
	before, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		status, err := service.Status("ws-1")
		if err != nil || status.Settings.IsSetUp() {
			t.Fatalf("link became a grant: %+v, %v", status, err)
		}
	}
	after, err := service.store.LoadSettings("ws-1")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read changed settings: %+v, %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(root, DefaultFilingRootName)); !os.IsNotExist(err) {
		t.Fatalf("read created Filed: %v", err)
	}
	ws, _ := workspaces.Get("ws-1")
	if len(ws.MCPBindings) != 0 {
		t.Fatal("read created a tool binding")
	}
	batches, err := service.ListBatches("ws-1")
	if err != nil || len(batches) != 0 {
		t.Fatalf("read created a batch: %+v, %v", batches, err)
	}
}

func TestCreationCharacterization_ManualScanWhilePausedDoesNotApproveMonitoring(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	file := filepath.Join(root, "invoice.pdf")
	contents := []byte("synthetic file: scan metadata only")
	if err := os.WriteFile(file, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	paused := true
	status, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.ScanNowOperation("ws-1", ScanSourceManual, ScanOperationRequest{
		OperationID: "characterize-scan", RootID: status.Settings.RootID, PrivacyMode: ContentModeMetadataOnly,
	})
	if err != nil || receipt.EligibleCount != 1 || receipt.BatchID == "" {
		t.Fatalf("manual result = %+v, %v", receipt, err)
	}
	after, err := service.Status("ws-1")
	if err != nil || !after.Settings.Paused || !after.Settings.AutomationApprovedAt.IsZero() {
		t.Fatalf("scan changed monitoring: %+v, %v", after, err)
	}
	readiness, err := adapter.Evaluate(context.Background(), stepFor(workspace.SetupStepKindAutomationReview))
	if err != nil || readiness.Ready {
		t.Fatalf("scan manufactured setup consent: %+v, %v", readiness, err)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != string(contents) {
		t.Fatalf("source file changed: %v", err)
	}
}

func TestCreationCharacterization_RevocationPreservesAnAdoptedLink(t *testing.T) {
	service, workspaces, base := installedService(t)
	root := mkdir(t, filepath.Join(base, "Inbox"))
	if err := workspaces.Update("ws-1", func(ws *workspace.Workspace) error {
		return ws.AddDirectoryReference(workspace.DirectoryReference{Name: "Existing link", Path: root})
	}); err != nil {
		t.Fatal(err)
	}
	paused := true
	grant, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused})
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.RevokeAccess(nil, "ws-1")
	if err != nil || status.Settings.IsSetUp() || status.Settings.RootPath != "" {
		t.Fatalf("revoke = %+v, %v", status, err)
	}
	ws, err := workspaces.Get("ws-1")
	if err != nil || len(ws.DirectoryReferences) != 1 || ws.DirectoryReferences[0].ID != grant.Settings.DirectoryReferenceID {
		t.Fatalf("revoke deleted adopted link: %+v, %v", ws, err)
	}
	if _, err := os.Stat(filepath.Join(root, DefaultFilingRootName)); err != nil {
		t.Fatalf("revoke removed Filed: %v", err)
	}
}

func TestCreationCharacterization_LegacyPausedIsNotManualConsent(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	paused := true
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused}); err != nil {
		t.Fatal(err)
	}
	path, err := service.store.settingsPath("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	// Schema 1 had neither an automation timestamp nor a manual-mode decision.
	// Leave it absent rather than obtaining today's defaults through a save.
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	settings.SchemaVersion = 1
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{workspace.SetupStepKindAutomationReview, workspace.SetupStepKindReadiness} {
		readiness, err := adapter.Evaluate(context.Background(), stepFor(kind))
		if err != nil || readiness.Ready || readiness.Blocked {
			t.Fatalf("legacy %s = %+v, %v", kind, readiness, err)
		}
	}
}
