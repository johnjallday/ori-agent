package filejanitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestLinkedPreparation_StableSymlinkSpellingPreservesTheAdoptedReference(t *testing.T) {
	service, store, root, request := preparationFixture(t, false)
	alias := filepath.Join(t.TempDir(), "inbox-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	store.workspaces["ws-1"].DirectoryReferences[0].Path = alias
	review, err := service.ReviewLinkedSetup("ws-1", "local", request.ReferenceID, false)
	if err != nil {
		t.Fatal(err)
	}
	request.ReviewRevision = review.Revision
	result, err := service.PrepareLinkedSetup(request, nil)
	if err != nil || result.Result == nil {
		t.Fatalf("canonical alias was treated as permission drift: %+v, %v", result, err)
	}
	if store.workspaces["ws-1"].DirectoryReferences[0].Path != alias {
		t.Fatal("adopted reference spelling was rewritten")
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

func TestLinkedPreparation_StopReleasesOnlyItsPendingClaimAndDoesNotUndoEffects(t *testing.T) {
	service, workspaces, root, request := preparationFixture(t, false)
	workspaces.updateErr = errors.New("access storage unavailable")
	first, err := service.PrepareLinkedSetup(request, nil)
	if err == nil || first.OperationID == "" {
		t.Fatal("expected an accepted partial grant")
	}
	if _, err := service.StopLinkedPreparation("ws-1", "another-owner", first.OperationID); err == nil {
		t.Fatal("foreign owner stopped preparation")
	}
	stopped, err := service.StopLinkedPreparation("ws-1", "local", first.OperationID)
	if err != nil || stopped.Settings.SetupPreparation != nil || stopped.Settings.PendingFolderGrant != nil || !stopped.Settings.Paused {
		t.Fatalf("stop = %+v, %v", stopped, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); err != nil {
		t.Fatal("stop removed an already-created Filed directory")
	}
	workspaces.updateErr = nil
	if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
		t.Fatal("stale retry revived stopped preparation")
	}
	paused := true
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused}); err != nil {
		t.Fatalf("stopped pending claim stranded explicit manual repair: %v", err)
	}
}

func TestLinkedPreparation_ManualChangesReleaseOnlyTheInvalidatedPendingClaim(t *testing.T) {
	for _, change := range []string{"pause", "schedule"} {
		t.Run(change, func(t *testing.T) {
			service, workspaces, root, request := preparationFixture(t, false)
			workspaces.updateErr = errors.New("grant storage unavailable")
			if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
				t.Fatal("expected pending grant")
			}
			workspaces.updateErr = nil
			var err error
			if change == "pause" {
				_, err = service.SetPaused("ws-1", true)
			} else {
				value := "10:30"
				_, err = service.UpdateSettings("ws-1", SettingsUpdate{DailyScanLocalTime: &value})
			}
			if err != nil {
				t.Fatal(err)
			}
			settings, _ := service.store.LoadSettings("ws-1")
			if settings.SetupPreparation != nil || settings.PendingFolderGrant != nil {
				t.Fatal("manual edit stranded the invalidated grant claim")
			}
			if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
				t.Fatal("old review undid the manual edit")
			}
			paused := true
			if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLinkedPreparation_StopPreservesSavedAccessAndExactResult(t *testing.T) {
	service, workspaces, _, request := preparationFixture(t, false)
	first, err := service.PrepareLinkedSetup(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := service.StopLinkedPreparation("ws-1", "local", first.OperationID)
	if err != nil || !stopped.Settings.IsSetUp() || !stopped.Settings.ManualScansOnly() || stopped.Settings.SetupPreparation != nil {
		t.Fatalf("stop discarded setup: %+v, %v", stopped, err)
	}
	if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
		t.Fatal("stopped preparation resumed")
	}
	ws, _ := workspaces.Get("ws-1")
	// The existing linked access was pinned separately from the Janitor's
	// metadata binding; stopping must preserve both associations.
	if len(ws.DirectoryReferences) != 1 || len(ws.MCPBindings) != 2 {
		t.Fatal("stop removed saved access")
	}
	batches, err := service.ListBatches("ws-1")
	if err != nil || len(batches) != 1 || batches[0].ID != first.Result.BatchID {
		t.Fatal("stop removed or replaced result")
	}
}

func TestLinkedPreparation_CancellationStopsUnstartedStagesAndResumesSameGrant(t *testing.T) {
	service, workspaces, root, request := preparationFixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	request.Context = ctx
	service.workspaces = &preparationBoundaryStore{fakeWorkspaceStore: workspaces, change: func(*workspace.Workspace) { cancel() }}
	first, err := service.PrepareLinkedSetup(request, nil)
	if !errors.Is(err, context.Canceled) || first.Stage != "decision" {
		t.Fatalf("cancelled = %+v, %v", first, err)
	}
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil || !settings.IsSetUp() || settings.SetupDecision != nil || settings.LastFolderGrant == nil {
		t.Fatalf("cancel lost prior grant or started a later stage: %+v, %v", settings, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); err != nil {
		t.Fatal("accepted folder consequence was concealed")
	}
	batches, _ := service.ListBatches("ws-1")
	if len(batches) != 0 {
		t.Fatal("cancellation started the scan")
	}
	request.Context = context.Background()
	resumed, err := service.PrepareLinkedSetup(request, nil)
	if err != nil || resumed.OperationID != first.OperationID || resumed.Result == nil {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	ws, _ := workspaces.Get("ws-1")
	if len(ws.DirectoryReferences) != 1 {
		t.Fatal("resume duplicated folder grant")
	}
}

func TestLinkedPreparation_SettlingIsAReceiptedResultNotAnAutomaticRescan(t *testing.T) {
	service, workspaces, root, request := preparationFixture(t, false)
	now := time.Now().UTC()
	file := filepath.Join(root, "invoice.pdf")
	if err := os.Chtimes(file, now, now); err != nil {
		t.Fatal(err)
	}
	service.now, service.scanner.now = func() time.Time { return now }, func() time.Time { return now }
	first, err := service.PrepareLinkedSetup(request, nil)
	if err != nil || first.Result == nil || first.Result.Outcome != ScanOutcomeNoEligible || first.Result.BatchID != "" || !first.Status.Settings.ManualScansOnly() {
		t.Fatalf("settling = %+v, %v", first, err)
	}
	now = now.Add(SettleInterval + time.Second)
	restarted := NewService(NewStore(service.store.resolver), workspaces)
	restarted.now, restarted.scanner.now = func() time.Time { return now }, func() time.Time { return now }
	replay, err := restarted.PrepareLinkedSetup(request, nil)
	if err != nil || replay.Result == nil || !replay.Result.CompletedAt.Equal(first.Result.CompletedAt) || replay.Result.Outcome != ScanOutcomeNoEligible {
		t.Fatalf("retry silently rescanned settling files: %+v, %v", replay, err)
	}
	batches, _ := restarted.ListBatches("ws-1")
	if len(batches) != 0 {
		t.Fatal("no-eligible receipt created a fake batch")
	}
	_, created, err := restarted.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created {
		t.Fatalf("explicit later scan = %v, %v", created, err)
	}
	settings, _ := restarted.store.LoadSettings("ws-1")
	if !settings.Paused || !settings.AutomationApprovedAt.IsZero() {
		t.Fatal("explicit Scan again enabled monitoring")
	}
}

type preparationPostUpdateErrorStore struct{ *fakeWorkspaceStore }

func (s *preparationPostUpdateErrorStore) Update(id string, update func(*workspace.Workspace) error) error {
	if err := s.fakeWorkspaceStore.Update(id, update); err != nil {
		return err
	}
	for i := range s.workspaces[id].MCPBindings {
		if s.workspaces[id].MCPBindings[i].Alias == LegacyBindingAlias {
			s.workspaces[id].MCPBindings[i].Enabled = false
		}
	}
	return errors.New("commit response lost, then owner disabled metadata access")
}

func TestLinkedPreparation_PartialGrantCannotRepairALaterPermissionRemoval(t *testing.T) {
	service, store, _, request := preparationFixture(t, false)
	service.workspaces = &preparationPostUpdateErrorStore{store}
	if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
		t.Fatal("expected uncertain workspace commit")
	}
	service.workspaces = store
	settings, _ := service.store.LoadSettings("ws-1")
	if settings.IsSetUp() || settings.PendingFolderGrant == nil {
		t.Fatal("fixture did not retain the unfinished grant")
	}
	if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
		t.Fatal("retry re-enabled removed access")
	}
	for _, binding := range store.workspaces["ws-1"].MCPBindings {
		if binding.Alias == LegacyBindingAlias && binding.Enabled {
			t.Fatal("old preparation re-enabled metadata access")
		}
	}
	if _, err := service.StopLinkedPreparation("ws-1", "local", settings.SetupPreparation.ID); err != nil {
		t.Fatal(err)
	}
}

func TestLinkedPreparation_PermissionEditsInvalidateReviewAndContinuation(t *testing.T) {
	t.Run("before confirmation", func(t *testing.T) {
		service, store, root, request := preparationFixture(t, false)
		store.workspaces["ws-1"].MCPBindings = append(store.workspaces["ws-1"].MCPBindings, workspace.MCPBinding{ID: "later", Alias: "other-access", Enabled: true})
		if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
			t.Fatal("permission edit did not expire review")
		}
		if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
			t.Fatal("stale review created Filed")
		}
	})
	for _, completed := range []bool{false, true} {
		for _, change := range []string{"disabled", "tools", "root", "association"} {
			t.Run(fmt.Sprintf("completed=%t/%s", completed, change), func(t *testing.T) {
				service, store, _, request := preparationFixture(t, false)
				if completed {
					if _, err := service.PrepareLinkedSetup(request, nil); err != nil {
						t.Fatal(err)
					}
				} else {
					ctx, cancel := context.WithCancel(context.Background())
					service.workspaces = &preparationBoundaryStore{fakeWorkspaceStore: store, change: func(*workspace.Workspace) { cancel() }}
					request.Context = ctx
					if _, err := service.PrepareLinkedSetup(request, nil); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					service.workspaces = store
					request.Context = context.Background()
				}
				ws := store.workspaces["ws-1"]
				for i := range ws.MCPBindings {
					binding := &ws.MCPBindings[i]
					if binding.Alias != LegacyBindingAlias {
						continue
					}
					switch change {
					case "disabled":
						binding.Enabled = false
					case "tools":
						binding.AllowedTools = []string{}
					case "root":
						binding.Config = map[string]any{"roots": []string{t.TempDir()}}
					case "association":
						ws.ForgetCapabilityResource(workspace.CapabilityFileJanitor, workspace.ResourceMCPBinding, binding.ID)
					}
				}
				before, _ := service.store.LoadScanState("ws-1")
				if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
					t.Fatal("old confirmation repaired changed permissions")
				}
				after, _ := service.store.LoadScanState("ws-1")
				if len(before.Batches) != len(after.Batches) || len(before.ScanOperations) != len(after.ScanOperations) {
					t.Fatal("permission edit caused another scan")
				}
				review, err := service.ReviewLinkedSetup("ws-1", "local", request.ReferenceID, false)
				if err != nil || review.State != "preparation_changed" || review.OperationID == "" {
					t.Fatalf("repair projection = %+v, %v", review, err)
				}
				if _, err := service.StopLinkedPreparation("ws-1", "local", review.OperationID); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestLinkedPreparation_DispatchIntentCannotGuessAfterCrashAndReceiptLoss(t *testing.T) {
	service, workspaces, _, request := preparationFixture(t, false)
	first, err := service.PrepareLinkedSetup(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate interruption after the scan committed but before its separate
	// refusal acknowledgment, followed by loss of the entire scan-state file.
	_, err = service.store.UpdateSettings("ws-1", func(current *JanitorSettings) error { current.SetupPreparation.ScanReceiptDigest = ""; return nil })
	if err != nil {
		t.Fatal(err)
	}
	path, _ := service.store.scanStatePath("ws-1")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	settings, _ := service.store.LoadSettings("ws-1")
	if settings.SetupPreparation.ScanDispatchedAt.IsZero() {
		t.Fatal("no durable dispatch fence")
	}
	restarted := NewService(NewStore(service.store.resolver), service.workspaces)
	if _, err := restarted.PrepareLinkedSetup(request, nil); err == nil {
		t.Fatal("restart guessed a fresh scan")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("old confirmation recreated missing scan state")
	}
	if first.Result == nil {
		t.Fatal("fixture never scanned")
	}
	beforeAccess, _ := setupAccessRevision(workspaces.workspaces["ws-1"])
	if _, created, err := restarted.ScanNow("ws-1", ScanSourceManual); err != nil || !created {
		t.Fatalf("explicit fresh scan = %v, %v", created, err)
	}
	current, _ := restarted.store.LoadSettings("ws-1")
	if current.SetupPreparation != nil || !current.ManualScansOnly() || current.RootID != settings.RootID {
		t.Fatal("fresh scan reapplied setup or retained uncertain continuation")
	}
	afterAccess, _ := setupAccessRevision(workspaces.workspaces["ws-1"])
	if beforeAccess != afterAccess {
		t.Fatal("fresh scan changed workspace permissions")
	}
}

func TestLinkedPreparation_MissingPrunedOrChangedReceiptNeverBecomesAnotherScan(t *testing.T) {
	for _, loss := range []string{"missing", "corrupt", "pruned", "changed", "invalid_time", "missing_batch"} {
		t.Run(loss, func(t *testing.T) {
			service, _, _, request := preparationFixture(t, false)
			first, err := service.PrepareLinkedSetup(request, nil)
			if err != nil {
				t.Fatal(err)
			}
			settings, _ := service.store.LoadSettings("ws-1")
			if len(settings.SetupPreparation.ScanReceiptDigest) != 64 {
				t.Fatal("initial acknowledgment has no retention refusal fence")
			}
			path, err := service.store.scanStatePath("ws-1")
			if err != nil {
				t.Fatal(err)
			}
			switch loss {
			case "invalid_time", "missing_batch":
				_, err := service.store.UpdateSettings("ws-1", func(current *JanitorSettings) error { current.SetupPreparation.ScanReceiptDigest = ""; return nil })
				if err != nil {
					t.Fatal(err)
				}
				state, _ := service.store.LoadScanState("ws-1")
				if loss == "invalid_time" {
					state.ScanOperations[0].CompletedAt = time.Time{}
				} else {
					state.Batches = nil
				}
				data, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				_, err = service.store.UpdateScanState("ws-1", func(state *ScanState) error {
					if loss == "changed" {
						state.ScanOperations[0].IneligibleCount++
						return nil
					}
					for i := range MaxRetainedScanOperations {
						state.ScanOperations = append(state.ScanOperations, ScanOperationReceipt{OperationID: fmt.Sprintf("later-%d", i), RootID: settings.RootID, PrivacyMode: ContentModeMetadataOnly, Outcome: ScanOutcomeNoEligible, CompletedAt: first.Result.CompletedAt.Add(time.Duration(i+1) * time.Second)})
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			if _, err := service.PrepareLinkedSetup(request, nil); err == nil {
				t.Fatal("lost receipt was treated as a fresh scan")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("refused old confirmation rewrote scan state")
			}
			review, err := service.ReviewLinkedSetup("ws-1", "local", request.ReferenceID, false)
			if err != nil || review.State != "result_unavailable" || review.Result != nil {
				t.Fatalf("uncertain receipt projection = %+v, %v", review, err)
			}
		})
	}
}

func TestLinkedPreparation_LifecycleChangeDuringScanCannotPublishResult(t *testing.T) {
	service, workspaces, _, request := preparationFixture(t, false)
	service.scanner.now = func() time.Time {
		workspaces.workspaces["ws-1"].Status = workspace.StatusTrashed
		return time.Now()
	}
	first, err := service.PrepareLinkedSetup(request, nil)
	if err == nil || first.Stage != "scan" || first.Result != nil {
		t.Fatalf("stale scan = %+v, %v", first, err)
	}
	batches, err := service.ListBatches("ws-1")
	if err != nil || len(batches) != 0 {
		t.Fatalf("stale scan published work: %+v, %v", batches, err)
	}
	settings, _ := service.store.LoadSettings("ws-1")
	if _, found, err := service.ScanOperationReceipt("ws-1", settings.SetupPreparation.ID+":scan"); err != nil || found {
		t.Fatal("stale scan published a receipt")
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
	if err == nil || failed.Stage != "monitoring" {
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
