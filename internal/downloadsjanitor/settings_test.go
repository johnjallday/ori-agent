package downloadsjanitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingLifecycle records the order automation calls arrive in, which is the
// property the relink and revoke sequences are actually about.
type recordingLifecycle struct {
	calls []string
	fail  string
}

func (r *recordingLifecycle) EnsureWatcher(string) error {
	r.calls = append(r.calls, "ensure")
	if r.fail == "ensure" {
		return context.DeadlineExceeded
	}
	return nil
}

func (r *recordingLifecycle) PauseWatcher(string) error {
	r.calls = append(r.calls, "pause")
	if r.fail == "pause" {
		return context.DeadlineExceeded
	}
	return nil
}

func (r *recordingLifecycle) RemoveWatcher(string) error {
	r.calls = append(r.calls, "remove")
	if r.fail == "remove" {
		return context.DeadlineExceeded
	}
	return nil
}

func TestUpdateSettings_ChangesScheduleWithoutRecreatingTheWorkspace(t *testing.T) {
	service, _ := configuredService(t)

	newTime := "07:15"
	zone := "America/New_York"
	status, err := service.UpdateSettings("ws-1", SettingsUpdate{
		DailyScanLocalTime: &newTime,
		Timezone:           &zone,
	})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if status.Settings.DailyScanLocalTime != "07:15" || status.Settings.Timezone != zone {
		t.Fatalf("settings = %+v", status.Settings)
	}
	// The folder is untouched by a schedule change.
	if !status.Settings.IsSetUp() {
		t.Fatal("changing the schedule must not unconfigure the workspace")
	}

	bad := "9am"
	if _, err := service.UpdateSettings("ws-1", SettingsUpdate{DailyScanLocalTime: &bad}); err == nil {
		t.Fatal("an unusable time must be rejected")
	}
	badZone := "Mars/Olympus"
	if _, err := service.UpdateSettings("ws-1", SettingsUpdate{Timezone: &badZone}); err == nil {
		t.Fatal("an unknown timezone must be rejected")
	}
	// A rejected change leaves the previous value in place.
	after, _ := service.Status("ws-1")
	if after.Settings.DailyScanLocalTime != "07:15" {
		t.Fatalf("a rejected change must not disturb settings: %+v", after.Settings)
	}
}

// Relink stops the old automation before pointing anywhere new, and invalidates
// everything that was bound to the old folder.
func TestRelink_StopsTheOldAutomationBeforeSwitching(t *testing.T) {
	service, oldRoot := configuredService(t)
	service.SetMover(&realMover{})
	agedFile(t, oldRoot, "old.pdf", 100)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	_, candidates, _ := service.BatchDetail("ws-1", batch.ID)

	// An approval exists for the old folder.
	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatal(err)
	}

	newRoot := filepath.Join(tempDirCanonical(t), "NewInbox")
	if err := os.MkdirAll(newRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	lifecycle := &recordingLifecycle{}

	status, err := service.Relink(lifecycle, RelinkRequest{WorkspaceID: "ws-1", Path: newRoot})
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}
	if status.Settings.RootPath != filepath.Clean(newRoot) {
		t.Fatalf("root = %q, want the new folder", status.Settings.RootPath)
	}

	// Pause came first, resume last: a watcher must not fire on the old folder
	// while the switch is happening.
	if len(lifecycle.calls) < 2 || lifecycle.calls[0] != "pause" || lifecycle.calls[len(lifecycle.calls)-1] != "ensure" {
		t.Fatalf("call order = %v, want pause first and ensure last", lifecycle.calls)
	}

	// The old folder's approval is dead: it authorized an action there.
	if _, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: moveItems(candidates, ""),
	}); err == nil {
		t.Fatal("an approval for the old folder must not survive a relink")
	}
	// The old file is untouched, still in the folder the user disconnected.
	if _, err := os.Stat(filepath.Join(oldRoot, "old.pdf")); err != nil {
		t.Fatalf("the old folder's file must be left alone: %v", err)
	}

	// Pending candidates from the old folder are no longer actionable.
	_, stored, err := service.BatchDetail("ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range stored {
		if candidate.State != CandidateStale {
			t.Fatalf("old-folder candidates must be invalidated: %+v", candidate)
		}
	}
}

// If the automation cannot be paused, the folder is not switched: a half-done
// relink is worse than none.
func TestRelink_DoesNotSwitchIfTheOldAutomationCannotStop(t *testing.T) {
	service, oldRoot := configuredService(t)
	newRoot := filepath.Join(tempDirCanonical(t), "NewInbox")
	if err := os.MkdirAll(newRoot, 0o750); err != nil {
		t.Fatal(err)
	}

	lifecycle := &recordingLifecycle{fail: "pause"}
	if _, err := service.Relink(lifecycle, RelinkRequest{WorkspaceID: "ws-1", Path: newRoot}); err == nil {
		t.Fatal("a relink that cannot stop the old watcher must fail")
	}
	status, _ := service.Status("ws-1")
	if status.Settings.RootPath != filepath.Clean(oldRoot) {
		t.Fatalf("the folder must not have changed: %q", status.Settings.RootPath)
	}
}

// Revoke stops the automation before removing the access it depends on, and
// keeps the history of what already happened.
func TestRevokeAccess_StopsAutomationFirstAndKeepsHistory(t *testing.T) {
	service, root := configuredService(t)
	service.SetMover(&realMover{})
	agedFile(t, root, "report.pdf", 100)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	_, candidates, _ := service.BatchDetail("ws-1", batch.ID)
	approveAndConfirm(t, service, candidates, "")

	lifecycle := &recordingLifecycle{}
	status, err := service.RevokeAccess(lifecycle, "ws-1")
	if err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}

	if len(lifecycle.calls) == 0 || lifecycle.calls[0] != "remove" {
		t.Fatalf("the watcher must be removed first, got %v", lifecycle.calls)
	}
	if status.Settings.IsSetUp() {
		t.Fatalf("the workspace must no longer be configured: %+v", status.Settings)
	}
	if status.Readiness.State != ReadinessSetupRequired {
		t.Fatalf("readiness = %q, want setup_required", status.Readiness.State)
	}

	// The workspace no longer holds folder access.
	ws, _ := service.workspaces.Get("ws-1")
	for _, binding := range ws.MCPBindings {
		if strings.EqualFold(binding.Alias, JanitorBindingAlias) {
			t.Fatal("the Janitor binding must be gone")
		}
	}
	if len(ws.DirectoryReferences) != 0 {
		t.Fatalf("the directory reference must be gone: %+v", ws.DirectoryReferences)
	}

	// History survives: revoking access does not make what already happened
	// untrue.
	actions, err := service.ListActions("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Result != ResultApplied {
		t.Fatalf("history must survive a revoke: %+v", actions)
	}
	// And the file really was filed before access went away.
	if _, err := os.Stat(filepath.Join(root, "Filed", "Documents", "report.pdf")); err != nil {
		t.Fatalf("the filed file stays where it is: %v", err)
	}
}

// Nothing can be scanned or approved once access is revoked.
func TestRevokeAccess_StopsFurtherWork(t *testing.T) {
	service, _ := configuredService(t)
	lifecycle := &recordingLifecycle{}
	if _, err := service.RevokeAccess(lifecycle, "ws-1"); err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}

	if _, _, err := service.ScanNow("ws-1", ScanSourceManual); err == nil {
		t.Fatal("scanning must stop after a revoke")
	}
	if _, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1",
		Items: []PreviewRequestItem{{CandidateID: "anything", Operation: OperationMove}},
	}); err == nil {
		t.Fatal("approving must stop after a revoke")
	}
}

// Revoking clears content consent along with everything else: re-granting
// access must ask again.
func TestRevokeAccess_ClearsContentConsent(t *testing.T) {
	service, root := configuredService(t)
	enableContent(t, service, ContentModeCloudModel, "SomeCloud")
	if _, err := service.GrantContentConsent("ws-1", "SomeCloud"); err != nil {
		t.Fatal(err)
	}

	if _, err := service.RevokeAccess(&recordingLifecycle{}, "ws-1"); err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}
	status, _ := service.Status("ws-1")
	if status.Settings.ContentMode != ContentModeMetadataOnly {
		t.Fatalf("content mode must return to metadata-only: %q", status.Settings.ContentMode)
	}
	if status.Settings.ContentConsentProvider != "" {
		t.Fatal("consent must not survive a revoke")
	}

	// Setting the folder up again starts from the safe default.
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	status, _ = service.Status("ws-1")
	if status.Privacy.Mode != ContentModeMetadataOnly {
		t.Fatalf("a re-set-up workspace starts metadata-only: %+v", status.Privacy)
	}
}

func TestListSkipped_ShowsWhatWasDismissed(t *testing.T) {
	service, root := configuredService(t)
	agedFile(t, root, "ad.png", 10)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	_, candidates, _ := service.BatchDetail("ws-1", batch.ID)
	if _, err := service.ApplyDecisions("ws-1", []DecisionUpdate{
		{CandidateID: candidates[0].ID, Decision: DecisionSkip},
	}); err != nil {
		t.Fatal(err)
	}

	skipped, err := service.ListSkipped("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0].Name != "ad.png" {
		t.Fatalf("skipped = %+v", skipped)
	}
	if skipped[0].Key == "" || skipped[0].SkippedAt.IsZero() {
		t.Fatalf("a skipped item needs a key and a time so it can be reset: %+v", skipped[0])
	}
}

// The privacy state is part of every status response, so no surface can show
// the feature without showing what it reads.
func TestPrivacyState_TravelsWithEveryStatus(t *testing.T) {
	service, _ := configuredService(t)
	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Privacy.Headline == "" || status.Privacy.Detail == "" {
		t.Fatalf("privacy must always be stated: %+v", status.Privacy)
	}
	if !strings.Contains(status.Privacy.Headline, "names, types, sizes, and dates") {
		t.Fatalf("the default state must say exactly what is read: %q", status.Privacy.Headline)
	}
	if !strings.Contains(status.Privacy.Detail, "No file contents") {
		t.Fatalf("the default state must say contents are not read: %q", status.Privacy.Detail)
	}
	if status.Privacy.LeavesDevice {
		t.Fatal("metadata-only mode never leaves the device")
	}
}
