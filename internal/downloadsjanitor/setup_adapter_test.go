package downloadsjanitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// stepFor builds the wizard request for one of the Downloads blueprint's steps,
// shaped exactly as the service would hand it to the adapter.
func stepFor(kind string) setupwizard.StepRequest {
	return setupwizard.StepRequest{
		WorkspaceID: "ws-1",
		Step: workspace.SetupWizardStep{
			ID:       kind,
			Kind:     kind,
			Required: true,
			Adapter:  SetupAdapterID,
		},
	}
}

// fakeAutomationStatus stands in for the registered watcher and scheduler, so
// readiness can report "running" without a real trigger system.
type fakeAutomationStatus struct{ registered bool }

func (f fakeAutomationStatus) WatcherRegistered(string) (bool, error) { return f.registered, nil }
func (f fakeAutomationStatus) SchedulerRegistered(string) bool        { return f.registered }

// countingWatcher records how many times the wizard asked for the watcher to be
// brought in line — the number that must not grow when a step is retried.
type countingWatcher struct {
	calls int
	err   error
}

func (w *countingWatcher) EnsureWatcher(string) error {
	w.calls++
	return w.err
}

func newAdapterFixture(t *testing.T) (*SetupAdapter, *Service, string) {
	t.Helper()
	store, _ := newTestStore(t)
	service := NewService(store, newFakeWorkspaceStore("ws-1"))
	root := filepath.Join(t.TempDir(), "Inbox")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	return NewSetupAdapter(service), service, root
}

func TestSetupAdapter_FreshWorkspaceIsNotReadyAndNotBlocked(t *testing.T) {
	adapter, _, _ := newAdapterFixture(t)
	ctx := context.Background()

	folder, err := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindDirectory))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if folder.Ready || folder.Blocked {
		t.Fatalf("a fresh workspace has simply not been set up yet: %+v", folder)
	}
	if folder.ErrorCategory != setupwizard.ErrorCategoryNotConfigured {
		t.Fatalf("category = %q, want not_configured", folder.ErrorCategory)
	}

	automation, _ := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindAutomationReview))
	if automation.Ready {
		t.Fatal("automation cannot be ready before a folder is chosen")
	}
	if automation.Summary == "" {
		t.Fatal("the automation step must say what it is waiting for")
	}
}

func TestSetupAdapter_EvaluateChangesNothing(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	ctx := context.Background()

	for range 3 {
		if _, err := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindDirectory)); err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if _, err := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindReadiness)); err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
	}

	// Looking must never grant: no folder chosen, and nothing written.
	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Settings.RootPath != "" {
		t.Fatalf("evaluation selected a folder: %q", status.Settings.RootPath)
	}
	if _, err := os.Stat(filepath.Join(root, DefaultFilingRootName)); !os.IsNotExist(err) {
		t.Fatal("evaluation created the destination folder")
	}
}

// TestSetupAdapter_FolderApprovalDoesNotStartAutomation is the ordering this
// blueprint's disclosure promises: choosing a folder grants access, and the
// watcher and daily scan start only when the user approves the step that
// describes them.
func TestSetupAdapter_FolderApprovalDoesNotStartAutomation(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	ctx := context.Background()

	paused := true
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	folder, _ := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindDirectory))
	if !folder.Ready {
		t.Fatalf("the folder step should pass once a usable folder is confirmed: %+v", folder)
	}

	automation, _ := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindAutomationReview))
	if automation.Ready || automation.Blocked {
		t.Fatalf("automation must read as not started yet, not ready and not broken: %+v", automation)
	}
	if automation.ErrorCategory != setupwizard.ErrorCategoryNotConfigured {
		t.Fatalf("category = %q, want not_configured", automation.ErrorCategory)
	}

	overall, _ := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindReadiness))
	if overall.Ready {
		t.Fatal("a workspace whose automation has not been approved is not ready")
	}
}

func TestSetupAdapter_ApprovingAutomationStartsItOnceAndIsRetrySafe(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	watcher := &countingWatcher{}
	adapter.SetAutomation(watcher)
	service.SetAutomationStatus(fakeAutomationStatus{registered: true})
	ctx := context.Background()

	paused := true
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	readiness, err := adapter.Confirm(ctx, stepFor(workspace.SetupStepKindAutomationReview), setupwizard.StepAction{Type: setupwizard.ActionConfirm})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !readiness.Ready {
		t.Fatalf("approving automation should start it: %+v", readiness)
	}
	status, _ := service.Status("ws-1")
	if status.Settings.Paused {
		t.Fatal("approval must resume unattended work")
	}
	if watcher.calls != 1 {
		t.Fatalf("watcher synced %d times, want 1", watcher.calls)
	}

	// A retry after a timeout or a refresh updates the same workspace rather
	// than registering a second watcher.
	if _, err := adapter.Confirm(ctx, stepFor(workspace.SetupStepKindAutomationReview), setupwizard.StepAction{}); err != nil {
		t.Fatalf("Confirm (retry): %v", err)
	}
	if watcher.calls != 2 {
		t.Fatalf("watcher syncs = %d; a retry re-syncs the same workspace", watcher.calls)
	}
	after, _ := service.Status("ws-1")
	if after.Settings.RootPath != status.Settings.RootPath || after.Settings.Paused {
		t.Fatalf("a retry changed the workspace's configuration: %+v", after.Settings)
	}
}

func TestSetupAdapter_AutomationApprovalWithoutAFolderIsRefusedQuietly(t *testing.T) {
	adapter, service, _ := newAdapterFixture(t)
	watcher := &countingWatcher{}
	adapter.SetAutomation(watcher)

	readiness, err := adapter.Confirm(context.Background(), stepFor(workspace.SetupStepKindAutomationReview), setupwizard.StepAction{})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if readiness.Ready {
		t.Fatal("automation cannot be approved before a folder exists")
	}
	if watcher.calls != 0 {
		t.Fatal("nothing may be registered for a workspace with no folder")
	}
	status, _ := service.Status("ws-1")
	if !status.Settings.Paused && status.Settings.RootPath != "" {
		t.Fatalf("the workspace was modified: %+v", status.Settings)
	}
}

func TestSetupAdapter_WatcherFailureKeepsTheApprovalAndReportsRepair(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	watcher := &countingWatcher{err: os.ErrPermission}
	adapter.SetAutomation(watcher)
	ctx := context.Background()

	paused := true
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused}); err != nil {
		t.Fatal(err)
	}
	readiness, err := adapter.Confirm(ctx, stepFor(workspace.SetupStepKindAutomationReview), setupwizard.StepAction{})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !readiness.Blocked || readiness.Ready {
		t.Fatalf("a watcher that would not start is a blocked step: %+v", readiness)
	}
	// The user's approval is not thrown away because the watcher failed.
	status, _ := service.Status("ws-1")
	if status.Settings.Paused {
		t.Fatal("the approval must survive a watcher failure so a retry can use it")
	}
}

func TestSetupAdapter_ReadyWorkspaceReportsReadyEverywhere(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	service.SetAutomationStatus(fakeAutomationStatus{registered: true})
	ctx := context.Background()

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{
		workspace.SetupStepKindDirectory,
		workspace.SetupStepKindAutomationReview,
		workspace.SetupStepKindReadiness,
		workspace.SetupStepKindSummary,
	} {
		readiness, err := adapter.Evaluate(ctx, stepFor(kind))
		if err != nil {
			t.Fatalf("Evaluate(%s): %v", kind, err)
		}
		if !readiness.Ready {
			t.Fatalf("step %s should be ready for a configured workspace: %+v", kind, readiness)
		}
		if readiness.ErrorCategory != "" {
			t.Errorf("a ready step carries no error category, got %q", readiness.ErrorCategory)
		}
	}
}

// TestSetupAdapter_RegressionIsReportedAsNeedsAttention covers the case the
// whole needs_attention state exists for: setup passed, then the folder went
// away.
func TestSetupAdapter_RegressionIsReportedAsNeedsAttention(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	service.SetAutomationStatus(fakeAutomationStatus{registered: true})
	ctx := context.Background()

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	folder, _ := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindDirectory))
	if folder.Ready || !folder.Blocked {
		t.Fatalf("a folder that disappeared blocks its step: %+v", folder)
	}
	if folder.ErrorCategory == "" {
		t.Fatal("a blocked step must carry a safe category")
	}
	overall, _ := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindReadiness))
	if overall.Ready {
		t.Fatal("the readiness step must follow the Janitor's own verdict")
	}

	// Repair: the same folder comes back, and so does readiness — no second
	// setup, no duplicate binding.
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatal(err)
	}
	repaired, _ := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindReadiness))
	if !repaired.Ready {
		t.Fatalf("repair should restore readiness: %+v", repaired)
	}
}

func TestSetupAdapter_SummariesCarryNoLocalPaths(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	service.SetAutomationStatus(fakeAutomationStatus{registered: true})
	ctx := context.Background()

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{
		workspace.SetupStepKindDirectory,
		workspace.SetupStepKindAutomationReview,
		workspace.SetupStepKindReadiness,
	} {
		readiness, _ := adapter.Evaluate(ctx, stepFor(kind))
		// The wizard payload travels into logs and analytics; the folder's path
		// belongs to the Janitor's own surface, which the user is already
		// looking at.
		if readiness.Summary != "" && containsPath(readiness.Summary, root) {
			t.Errorf("step %s leaked a local path: %q", kind, readiness.Summary)
		}
	}
}

func containsPath(summary, root string) bool {
	return len(root) > 0 && len(summary) >= len(root) && (summary == root ||
		len(summary) > len(root) && (stringContains(summary, root)))
}

func stringContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestSetupAdapter_UnavailableServiceBlocksRatherThanPanics(t *testing.T) {
	var adapter *SetupAdapter
	readiness, err := adapter.Evaluate(context.Background(), stepFor(workspace.SetupStepKindReadiness))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !readiness.Blocked || readiness.ErrorCategory != setupwizard.ErrorCategoryUnavailable {
		t.Fatalf("an unwired adapter blocks with a safe category: %+v", readiness)
	}
	if _, err := adapter.Confirm(context.Background(), stepFor(workspace.SetupStepKindAutomationReview), setupwizard.StepAction{}); err == nil {
		t.Fatal("an unwired adapter must refuse to act")
	}
}

// TestSetupAdapter_PausingAfterApprovalIsNotUnfinishedSetup separates the two
// questions that look alike from the outside: "has the user agreed to
// unattended work?" and "is it running right now?".
//
// Pausing is an operational choice about something already approved. If setup
// read it as unfinished, using the pause button would nag the user to set up a
// workspace they had already set up.
func TestSetupAdapter_PausingAfterApprovalIsNotUnfinishedSetup(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	adapter.SetAutomation(&countingWatcher{})
	service.SetAutomationStatus(fakeAutomationStatus{registered: true})
	ctx := context.Background()

	paused := true
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Confirm(ctx, stepFor(workspace.SetupStepKindAutomationReview), setupwizard.StepAction{}); err != nil {
		t.Fatal(err)
	}

	// The user pauses later, from the Janitor's own surface.
	if _, err := service.SetPaused("ws-1", true); err != nil {
		t.Fatal(err)
	}

	automation, _ := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindAutomationReview))
	if !automation.Ready {
		t.Fatalf("a paused-after-approval workspace is set up, just quiet: %+v", automation)
	}
	overall, _ := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindReadiness))
	if !overall.Ready {
		t.Fatalf("pausing must not reopen setup: %+v", overall)
	}
}

// TestSetupAdapter_LegacyRunningWorkspaceCountsAsApproved covers workspaces
// configured before the approval timestamp existed: one already running
// unattended was set up by someone who saw the same disclosure, so setup must
// not re-ask.
func TestSetupAdapter_LegacyRunningWorkspaceCountsAsApproved(t *testing.T) {
	adapter, service, root := newAdapterFixture(t)
	service.SetAutomationStatus(fakeAutomationStatus{registered: true})
	ctx := context.Background()

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatal(err)
	}
	status, _ := service.Status("ws-1")
	if !status.Settings.AutomationApprovedAt.IsZero() {
		t.Fatal("precondition: a legacy setup records no approval timestamp")
	}

	automation, _ := adapter.Evaluate(ctx, stepFor(workspace.SetupStepKindAutomationReview))
	if !automation.Ready {
		t.Fatalf("a workspace already watching a folder must not be asked again: %+v", automation)
	}
}
