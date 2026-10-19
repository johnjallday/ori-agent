package filejanitor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

func manualSetupFixture(t *testing.T) (*Service, *SetupAdapter, Status) {
	t.Helper()
	adapter, service, root := newAdapterFixture(t)
	paused := true
	status, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused})
	if err != nil {
		t.Fatal(err)
	}
	return service, adapter, status
}

func chooseManual(t *testing.T, service *Service, status Status) Status {
	t.Helper()
	result, err := service.RecordManualSetupDecision("ws-1", status.Settings.RootID, status.Settings.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestManualSetupDecision_CompletesCanonicalReadinessWithoutAutomation(t *testing.T) {
	service, adapter, status := manualSetupFixture(t)
	status = chooseManual(t, service, status)
	if !status.Settings.ManualScansOnly() || !status.Settings.Paused || !status.Settings.AutomationApprovedAt.IsZero() {
		t.Fatalf("manual mode fabricated monitoring approval: %+v", status.Settings)
	}
	if status.Readiness.State != ReadinessReady {
		t.Fatalf("manual readiness = %+v", status.Readiness)
	}
	for _, check := range status.Readiness.Checks {
		if strings.Contains(check.Message, "Paused by you") {
			t.Fatalf("new manual setup described as a user pause: %+v", check)
		}
	}
	for _, adapterID := range []string{LegacySetupAdapterID, workspacecapability.FileJanitorSetupAdapterID} {
		for _, kind := range []string{workspace.SetupStepKindDirectory, workspace.SetupStepKindAutomationReview, workspace.SetupStepKindReadiness, workspace.SetupStepKindSummary} {
			req := stepFor(kind)
			req.Step.Adapter = adapterID
			ready, err := adapter.Evaluate(context.Background(), req)
			if err != nil || !ready.Ready || ready.Blocked {
				t.Fatalf("persisted %s/%s = %+v, %v", adapterID, kind, ready, err)
			}
		}
	}
	batches, err := service.ListBatches("ws-1")
	if err != nil || len(batches) != 0 {
		t.Fatalf("mode choice itself scanned: %+v, %v", batches, err)
	}
}

func TestManualSetupDecision_SurvivesStoreRestart(t *testing.T) {
	service, _, status := manualSetupFixture(t)
	status = chooseManual(t, service, status)
	restarted := NewStore(service.store.resolver)
	settings, err := restarted.LoadSettings("ws-1")
	if err != nil || !settings.ManualScansOnly() || !reflect.DeepEqual(settings.SetupDecision, status.Settings.SetupDecision) {
		t.Fatalf("decision not durable: %+v, %v", settings, err)
	}
	if settings.SchemaVersion != 3 {
		t.Fatalf("schema = %d", settings.SchemaVersion)
	}
}

func TestManualSetupDecision_RejectsStaleRootOrSettingsWithoutWrites(t *testing.T) {
	for _, change := range []string{"root", "settings", "missing_revision", "privacy", "approved", "unpaused", "revoked"} {
		t.Run(change, func(t *testing.T) {
			service, _, status := manualSetupFixture(t)
			rootID, reviewedAt := status.Settings.RootID, status.Settings.UpdatedAt
			switch change {
			case "root":
				rootID = "another-root"
			case "settings":
				value := "10:30"
				if _, err := service.UpdateSettings("ws-1", SettingsUpdate{DailyScanLocalTime: &value}); err != nil {
					t.Fatal(err)
				}
			case "missing_revision":
				reviewedAt = time.Time{}
			case "privacy":
				mode := ContentModeLocalModel
				changed, err := service.UpdateSettings("ws-1", SettingsUpdate{ContentMode: &mode})
				if err != nil {
					t.Fatal(err)
				}
				reviewedAt = changed.Settings.UpdatedAt
			case "approved":
				changed, err := service.RecordAutomationApprovalPaused("ws-1")
				if err != nil {
					t.Fatal(err)
				}
				reviewedAt = changed.Settings.UpdatedAt
			case "unpaused":
				changed, err := service.SetPaused("ws-1", false)
				if err != nil {
					t.Fatal(err)
				}
				reviewedAt = changed.Settings.UpdatedAt
			case "revoked":
				if _, err := service.RevokeAccess(nil, "ws-1"); err != nil {
					t.Fatal(err)
				}
			}
			before, err := service.store.LoadSettings("ws-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.RecordManualSetupDecision("ws-1", rootID, reviewedAt); err == nil {
				t.Fatal("stale or incompatible decision accepted")
			}
			after, err := service.store.LoadSettings("ws-1")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("refusal changed settings: %+v, %v", after, err)
			}
		})
	}
}

func TestManualSetupDecision_ResumeRequiresMonitoringReview(t *testing.T) {
	service, _, status := manualSetupFixture(t)
	status = chooseManual(t, service, status)
	unpaused := false
	for _, apply := range []func() error{
		func() error { _, err := service.SetPaused("ws-1", false); return err },
		func() error { _, err := service.UpdateSettings("ws-1", SettingsUpdate{Paused: &unpaused}); return err },
		func() error {
			_, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: status.Settings.RootPath, Paused: &unpaused})
			return err
		},
	} {
		var setupError *SetupError
		if err := apply(); !errors.As(err, &setupError) || setupError.Code != CodePrivacyReviewRequired {
			t.Fatalf("unreviewed activation = %v", err)
		}
	}
	approved, err := service.ApproveAutomation("ws-1")
	if err != nil || approved.Settings.ManualScansOnly() || approved.Settings.Paused || approved.Settings.AutomationApprovedAt.IsZero() {
		t.Fatalf("explicit approval = %+v, %v", approved.Settings, err)
	}
	if approved.Settings.SetupDecision == nil || approved.Settings.SetupDecision.Mode != SetupModeMonitoring {
		t.Fatalf("monitoring decision missing: %+v", approved.Settings)
	}
}

func TestManualSetupDecision_MissingUnknownAndMismatchedAreNotConsent(t *testing.T) {
	service, _, status := manualSetupFixture(t)
	for _, decision := range []*SetupDecision{
		nil,
		{Mode: "future", RootID: status.Settings.RootID, DecidedAt: time.Now()},
		{Mode: SetupModeManual, RootID: "other", DecidedAt: time.Now()},
		{Mode: SetupModeManual, RootID: status.Settings.RootID},
	} {
		settings := status.Settings
		settings.SetupDecision = decision
		normalized := settings.Normalize()
		if normalized.ManualScansOnly() || normalized.SetupDecision != nil || !normalized.AutomationApprovedAt.IsZero() {
			t.Fatalf("invalid decision became consent: %+v", normalized)
		}
		if decision != nil && settings.Validate() == nil {
			t.Fatal("authoring accepted invalid decision")
		}
	}
	status = chooseManual(t, service, status)
	for _, schema := range []int{0, 2, SettingsSchemaVersion + 1} {
		settings := status.Settings
		settings.SchemaVersion = schema
		if normalized := settings.Normalize(); normalized.ManualScansOnly() || normalized.SetupDecision != nil {
			t.Fatalf("schema %d fabricated a known consent decision: %+v", schema, normalized)
		}
	}
	settings := status.Settings
	settings.Paused = false
	if settings.Validate() == nil || !settings.Normalize().Paused {
		t.Fatal("malformed paused flag activated manual mode")
	}
}

func TestManualSetupDecision_RootChangeAndRevokeInvalidateDecision(t *testing.T) {
	service, _, status := manualSetupFixture(t)
	status = chooseManual(t, service, status)
	paused := true
	changed, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: inboxFixture(t), Paused: &paused})
	if err != nil || changed.Settings.SetupDecision != nil || changed.Settings.RootID == status.Settings.RootID {
		t.Fatalf("new root inherited decision: %+v, %v", changed.Settings, err)
	}
	changed = chooseManual(t, service, changed)
	revoked, err := service.RevokeAccess(nil, "ws-1")
	if err != nil || revoked.Settings.SetupDecision != nil || revoked.Settings.AccessRevokedAt.IsZero() {
		t.Fatalf("revocation lost history: %+v, %v", revoked.Settings, err)
	}
	restarted := NewStore(service.store.resolver)
	loaded, err := restarted.LoadSettings("ws-1")
	if err != nil || loaded.AccessRevokedAt.IsZero() {
		t.Fatalf("revocation not durable: %+v, %v", loaded, err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{
		WorkspaceID: "ws-1", Path: changed.Settings.RootPath, Paused: &paused,
		Operation: &FolderGrantOperation{RunID: "old-run", OperationID: "old-operation"},
	}); err == nil {
		t.Fatal("stale operation revived access")
	}
	// Existing explicit picker/manual repair still works, with no inherited mode.
	repaired, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: changed.Settings.RootPath, Paused: &paused})
	if err != nil || !repaired.Settings.AccessRevokedAt.IsZero() || repaired.Settings.SetupDecision != nil {
		t.Fatalf("explicit repair = %+v, %v", repaired.Settings, err)
	}
}

func TestManualSetupDecision_PartialRevokeCannotReviveAStaleGrant(t *testing.T) {
	service, workspaces, root, _ := linkedSetupFixture(t)
	operation := FolderGrantOperation{RunID: "run", OperationID: "grant"}
	status := assistedGrant(t, service, root, operation)
	chooseManual(t, service, status)
	workspaces.updateErr = errors.New("workspace storage unavailable")
	if _, err := service.RevokeAccess(nil, "ws-1"); err == nil {
		t.Fatal("expected access-resource removal failure")
	}
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil || settings.AccessRevokedAt.IsZero() || settings.IsSetUp() || settings.PendingFolderGrant != nil {
		t.Fatalf("partial revoke kept usable authority: %+v, %v", settings, err)
	}
	workspaces.updateErr = nil
	paused := true
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root, Paused: &paused, Operation: &operation}); err == nil {
		t.Fatal("stale grant undid partial revocation")
	}
	if _, err := service.ScanNowOperation("ws-1", ScanSourceManual, ScanOperationRequest{
		OperationID: "old-scan", RootID: status.Settings.RootID, PrivacyMode: ContentModeMetadataOnly,
	}); err == nil {
		t.Fatal("scan used revoked authority")
	}
	adapter := NewSetupAdapter(service)
	ready, err := adapter.Evaluate(context.Background(), stepFor(workspace.SetupStepKindDirectory))
	if err != nil || ready.Ready {
		t.Fatalf("wizard accepted revoked binding: %+v, %v", ready, err)
	}
}

func TestManualSetupDecision_CapabilityReportsReadyNotPausedOrWatching(t *testing.T) {
	service, _, status := manualSetupFixture(t)
	chooseManual(t, service, status)
	runtime := NewCapabilityRuntime(service)
	got, err := runtime.CapabilityStatus("ws-1")
	if err != nil || got.State != workspacecapability.StatusReady || got.Detail != "Ready · Manual scans" {
		t.Fatalf("capability = %+v, %v", got, err)
	}
}
