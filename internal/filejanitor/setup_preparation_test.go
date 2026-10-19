package filejanitor

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func preparationFixture(t *testing.T, monitoring bool) (*Service, *fakeWorkspaceStore, string, LinkedPreparationRequest) {
	t.Helper()
	service, workspaces, root, reference := linkedSetupFixture(t)
	file := filepath.Join(root, "invoice.pdf")
	if err := os.WriteFile(file, []byte("synthetic bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewLinkedSetup("ws-1", "local", reference, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	return service, workspaces, root, LinkedPreparationRequest{
		WorkspaceID: "ws-1", OwnerUserID: "local", ReferenceID: reference,
		Monitoring: monitoring, ReviewRevision: review.Revision,
	}
}

func TestLinkedPreparation_ManualInitialResultAndRestartReplay(t *testing.T) {
	service, workspaces, root, request := preparationFixture(t, false)
	result, err := service.PrepareLinkedSetup(request, nil)
	if err != nil || result.Stage != "result" || result.Result == nil || result.Result.EligibleCount != 1 {
		t.Fatalf("preparation = %+v, %v", result, err)
	}
	if !result.Status.Settings.ManualScansOnly() || !result.Status.Settings.AutomationApprovedAt.IsZero() {
		t.Fatalf("manual preparation enabled monitoring: %+v", result.Status.Settings)
	}
	restarted := NewService(NewStore(service.store.resolver), workspaces)
	replayed, err := restarted.PrepareLinkedSetup(request, nil)
	if err != nil || replayed.OperationID != result.OperationID || replayed.Result == nil ||
		replayed.Result.BatchID != result.Result.BatchID || replayed.Result.Outcome != result.Result.Outcome ||
		!replayed.Result.CompletedAt.Equal(result.Result.CompletedAt) {
		t.Fatalf("restart replay = %+v, %v", replayed, err)
	}
	batches, err := restarted.ListBatches("ws-1")
	if err != nil || len(batches) != 1 {
		t.Fatalf("duplicate batches: %+v, %v", batches, err)
	}
	ws, _ := workspaces.Get("ws-1")
	if len(ws.DirectoryReferences) != 1 {
		t.Fatal("duplicate directory reference")
	}
	if contents, err := os.ReadFile(filepath.Join(root, "invoice.pdf")); err != nil || string(contents) != "synthetic bytes" {
		t.Fatalf("source mutated: %v", err)
	}
}

func TestLinkedPreparation_ConcurrentConfirmationsReplayOneResult(t *testing.T) {
	service, _, _, request := preparationFixture(t, false)
	results := make([]SetupPreparationResult, 8)
	errs := make([]error, len(results))
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() { defer wg.Done(); results[i], errs[i] = service.PrepareLinkedSetup(request, nil) }()
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil || results[i].OperationID != results[0].OperationID || results[i].Result == nil || results[i].Result.BatchID != results[0].Result.BatchID {
			t.Fatalf("submit %d = %+v, %v", i, results[i], errs[i])
		}
	}
	batches, err := service.ListBatches("ws-1")
	if err != nil || len(batches) != 1 {
		t.Fatalf("duplicate batches: %+v, %v", batches, err)
	}
}

func TestLinkedPreparation_StaleOrForeignReviewGrantsNothing(t *testing.T) {
	for _, change := range []string{"foreign_owner", "foreign_workspace", "foreign_reference", "monitoring", "root", "symlink"} {
		t.Run(change, func(t *testing.T) {
			service, _, root, request := preparationFixture(t, false)
			switch change {
			case "foreign_owner":
				request.OwnerUserID = "other"
			case "foreign_workspace":
				request.WorkspaceID = "ws-2"
			case "foreign_reference":
				request.ReferenceID = "other"
			case "monitoring":
				request.Monitoring = true
			case "root":
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				mkdir(t, root)
			case "symlink":
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), root); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
				t.Fatal("stale review was accepted")
			}
			if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
				t.Fatalf("stale review created Filed: %v", err)
			}
			settings, err := service.store.LoadSettings("ws-1")
			if err != nil || settings.IsSetUp() || settings.SetupPreparation != nil {
				t.Fatalf("stale review wrote permission: %+v, %v", settings, err)
			}
		})
	}
}

func TestLinkedPreparation_RevocationAndManualChangesStopRetries(t *testing.T) {
	for _, change := range []string{"revoke", "pause", "privacy", "schedule", "owner", "capability"} {
		t.Run(change, func(t *testing.T) {
			service, workspaces, _, request := preparationFixture(t, false)
			result, err := service.PrepareLinkedSetup(request, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "revoke":
				_, err = service.RevokeAccess(nil, "ws-1")
			case "pause":
				_, err = service.SetPaused("ws-1", true)
			case "privacy":
				mode := ContentModeLocalModel
				_, err = service.UpdateSettings("ws-1", SettingsUpdate{ContentMode: &mode})
			case "schedule":
				value := "10:30"
				_, err = service.UpdateSettings("ws-1", SettingsUpdate{DailyScanLocalTime: &value})
			case "owner":
				workspaces.workspaces["ws-1"].OwnerUserID = "other"
			case "capability":
				workspaces.workspaces["ws-1"].RemoveInstalledCapability("file-janitor")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
				t.Fatal("retry undid later user choice")
			}
			batches, err := service.ListBatches("ws-1")
			if err != nil || len(batches) != 1 || batches[0].ID != result.Result.BatchID {
				t.Fatalf("retry produced work: %+v, %v", batches, err)
			}
		})
	}
}

func TestLinkedPreparation_GrantFailurePreservesClaimAndRetriesExactFolder(t *testing.T) {
	service, workspaces, root, request := preparationFixture(t, false)
	workspaces.updateErr = errors.New("injected access write failure")
	first, err := service.PrepareLinkedSetup(request, nil)
	if err == nil || first.OperationID == "" || first.Stage != "folder" {
		t.Fatalf("failure = %+v, %v", first, err)
	}
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil || settings.PendingFolderGrant == nil || settings.SetupPreparation == nil {
		t.Fatalf("missing pending authority: %+v, %v", settings, err)
	}
	workspaces.updateErr = nil
	restarted := NewService(NewStore(service.store.resolver), workspaces)
	retried, err := restarted.PrepareLinkedSetup(request, nil)
	if err != nil || retried.OperationID != first.OperationID || retried.Stage != "result" || retried.Status.Settings.RootPath != root {
		t.Fatalf("retry = %+v, %v", retried, err)
	}
}

type preparationBoundaryStore struct {
	*fakeWorkspaceStore
	change func(*workspace.Workspace)
}

func (s *preparationBoundaryStore) Update(id string, mutate func(*workspace.Workspace) error) error {
	return s.fakeWorkspaceStore.Update(id, func(ws *workspace.Workspace) error {
		if s.change != nil {
			s.change(ws)
			s.change = nil
		}
		return mutate(ws)
	})
}

func TestLinkedPreparation_WorkspaceChangesAtGrantBoundaryCannotRestoreAccess(t *testing.T) {
	for _, change := range []string{"owner", "archive", "remove", "reinstall"} {
		t.Run(change, func(t *testing.T) {
			service, workspaces, _, request := preparationFixture(t, false)
			service.workspaces = &preparationBoundaryStore{fakeWorkspaceStore: workspaces, change: func(ws *workspace.Workspace) {
				switch change {
				case "owner":
					ws.OwnerUserID = "another-owner"
				case "archive":
					ws.Status = workspace.StatusTrashed
				case "remove":
					ws.RemoveInstalledCapability(workspace.CapabilityFileJanitor)
				case "reinstall":
					install, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
					install.InstalledAt = install.InstalledAt.Add(time.Second)
					ws.SetInstalledCapabilities([]workspace.InstalledCapability{install})
				}
			}}
			if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
				t.Fatal("changed workspace received access")
			}
			settings, err := service.store.LoadSettings("ws-1")
			if err != nil || settings.IsSetUp() || settings.LastFolderGrant != nil {
				t.Fatalf("grant survived boundary: %+v, %v", settings, err)
			}
			if ws, _ := workspaces.Get("ws-1"); len(ws.MCPBindings) != 0 {
				t.Fatal("access resource was written after lifecycle change")
			}
		})
	}
}

type preparationAutomation struct {
	calls int
	fail  bool
}

func (a *preparationAutomation) EnsureWatcher(string) error {
	a.calls++
	if a.fail {
		return errors.New("injected watcher failure")
	}
	return nil
}
func (a *preparationAutomation) WatcherRecord(string) (string, bool, error) {
	return "watcher-1", !a.fail, nil
}
func (a *preparationAutomation) WatcherRegistered(string) (bool, error) { return !a.fail, nil }
func (a *preparationAutomation) SchedulerRegistered(string) bool        { return !a.fail }
func (a *preparationAutomation) MarkCaughtUp(string, string)            {}

func TestLinkedPreparation_OptInRequiresRealMonitoringHealth(t *testing.T) {
	service, _, _, request := preparationFixture(t, true)
	automation := &preparationAutomation{fail: true}
	service.SetAutomationStatus(automation)
	failed, err := service.PrepareLinkedSetup(request, automation)
	if err == nil || failed.Stage != "prepare" {
		t.Fatalf("unhealthy monitoring claimed success: %+v, %v", failed, err)
	}
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil || !settings.Paused || settings.AutomationApprovedAt.IsZero() {
		t.Fatalf("approval/rollback = %+v, %v", settings, err)
	}
	batches, err := service.ListBatches("ws-1")
	if err != nil || len(batches) != 0 {
		t.Fatalf("unhealthy monitoring scanned: %+v, %v", batches, err)
	}
	automation.fail = false
	success, err := service.PrepareLinkedSetup(request, automation)
	if err != nil || success.Result == nil || success.Result.EligibleCount != 1 || success.Status.Settings.Paused {
		t.Fatalf("retry = %+v, %v", success, err)
	}
	calls := automation.calls
	if _, err := service.PrepareLinkedSetup(request, automation); err != nil {
		t.Fatal(err)
	}
	if automation.calls != calls {
		t.Fatal("lost-response replay restarted monitoring")
	}
}
