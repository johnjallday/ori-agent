package filejanitor

import (
	"errors"
	"testing"
	"time"
)

func TestAutomationApprovalRemainsPausedUntilWatcherIsHealthy(t *testing.T) {
	service, _ := configuredService(t)
	review, err := service.ReviewAutomation("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if review.ContentMode != ContentModeMetadataOnly || review.RootID == "" || review.DailyScanLocalTime == "" ||
		len(review.WatchEvents) != 2 || review.SettlingSecond != int(SettleInterval.Seconds()) {
		t.Fatalf("review = %+v", review)
	}
	status, err := service.RecordAutomationApprovalPaused("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Settings.Paused || status.Settings.AutomationApprovedAt.IsZero() {
		t.Fatalf("approval started automation: %+v", status.Settings)
	}
	if err := RequireHealthyAutomation(status); err == nil {
		t.Fatal("paused settings were treated as healthy automation")
	}

	automation := NewAutomation(service, newFakeTriggers())
	service.SetAutomationStatus(automation)
	automation.Start(func() []string { return []string{"ws-1"} }, time.Hour)
	defer automation.Stop()
	if _, err := service.SetPaused("ws-1", false); err != nil {
		t.Fatal(err)
	}
	if err := automation.EnsureWatcher("ws-1"); err != nil {
		t.Fatal(err)
	}
	status, err = service.Status("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireHealthyAutomation(status); err != nil {
		t.Fatalf("healthy automation rejected: %v (%+v)", err, status.Readiness)
	}
	if watcherID, enabled, err := automation.WatcherRecord("ws-1"); err != nil || !enabled || watcherID == "" {
		t.Fatalf("watcher = %q enabled=%v err=%v", watcherID, enabled, err)
	}
}

func TestAssistedAutomationReceiptBindsRetryAndManualPauseClearsIt(t *testing.T) {
	service, _ := configuredService(t)
	if _, err := service.SetPaused("ws-1", true); err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewAutomation("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	operation := AssistedAutomationOperation{
		RunID: "run-1", OperationID: "operation-1", ReviewRevision: "review-1", AuthorityRevision: "authority-1",
	}
	if _, err := service.RecordReviewedAutomationApprovalPaused(
		"ws-1", review.RootID, review.ContentMode, review.DailyScanLocalTime, review.Timezone,
		false, true, operation,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivateReviewedAutomation(
		"ws-1", review.RootID, review.ContentMode, review.DailyScanLocalTime, review.Timezone, operation,
	); err != nil {
		t.Fatal(err)
	}
	if err := service.PauseReviewedAutomation(
		"ws-1", review.RootID, review.ContentMode, review.DailyScanLocalTime, review.Timezone, operation,
	); err != nil {
		t.Fatal(err)
	}
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if receipt := settings.LastAssistedAutomation; receipt == nil || receipt.State != AssistedAutomationFailedPaused || receipt.OperationID != operation.OperationID {
		t.Fatalf("receipt = %+v", receipt)
	}
	if _, err := service.SetPaused("ws-1", true); err != nil {
		t.Fatal(err)
	}
	settings, _ = service.store.LoadSettings("ws-1")
	if settings.LastAssistedAutomation != nil {
		t.Fatalf("manual pause retained retry authority: %+v", settings.LastAssistedAutomation)
	}
}

func TestReviewedTimezoneFillsOnlyAnUnsetMatchingSchedule(t *testing.T) {
	service, _ := configuredService(t)
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyReviewedAutomationTimezone("ws-1", settings.RootID, settings.ContentMode, settings.DailyScanLocalTime, "America/New_York"); err != nil {
		t.Fatal(err)
	}
	settings, _ = service.store.LoadSettings("ws-1")
	if settings.Timezone != "America/New_York" {
		t.Fatalf("timezone = %q", settings.Timezone)
	}
	if err := service.ApplyReviewedAutomationTimezone("ws-1", settings.RootID, settings.ContentMode, settings.DailyScanLocalTime, "UTC"); err == nil {
		t.Fatal("concurrent timezone change was overwritten")
	}
	settings, _ = service.store.LoadSettings("ws-1")
	if settings.Timezone != "America/New_York" {
		t.Fatalf("timezone changed = %q", settings.Timezone)
	}
	if _, err := service.RecordReviewedAutomationApprovalPaused(
		"ws-1", settings.RootID, settings.ContentMode, settings.DailyScanLocalTime,
		settings.Timezone, false, true, AssistedAutomationOperation{
			RunID: "run-1", OperationID: "operation-1", ReviewRevision: "review-1", AuthorityRevision: "authority-1",
		},
	); err == nil {
		t.Fatal("stale paused-state review was accepted")
	}
	settings, _ = service.store.LoadSettings("ws-1")
	if !settings.AutomationApprovedAt.IsZero() || settings.Paused {
		t.Fatalf("stale review changed monitoring: %+v", settings)
	}
}

func TestAutomationReviewRefusesCustomPrivacyWithoutChangingIt(t *testing.T) {
	service, _ := configuredService(t)
	if _, err := service.store.UpdateSettings("ws-1", func(settings *JanitorSettings) error {
		settings.ContentMode = ContentModeLocalModel
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := service.RecordAutomationApprovalPaused("ws-1")
	var setupError *SetupError
	if !errors.As(err, &setupError) || setupError.Code != CodePrivacyReviewRequired {
		t.Fatalf("err = %v", err)
	}
	settings, _ := service.store.LoadSettings("ws-1")
	if settings.ContentMode != ContentModeLocalModel || !settings.AutomationApprovedAt.IsZero() || settings.Paused {
		t.Fatalf("custom privacy changed: %+v", settings)
	}
}
