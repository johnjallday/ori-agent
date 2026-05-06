package macwake

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newTestService(t *testing.T) (*Service, *config.Manager, string) {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "settings.json")
	manager := config.NewManager(configPath)
	if err := manager.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}

	service := NewService(manager)
	service.goos = func() string { return "darwin" }
	service.euid = func() int { return 0 }
	service.eventLister = func() []string { return nil }
	return service, manager, configPath
}

func TestUpdateSettingsNormalizesAndPersists(t *testing.T) {
	service, _, configPath := newTestService(t)

	enabled := true
	leadMinutes := 500
	fallback := "not-real"
	status, err := service.UpdateSettings(&enabled, &leadMinutes, &fallback)
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}

	if !status.Enabled {
		t.Fatal("expected wake scheduling to be enabled")
	}
	if status.DefaultLeadMinutes != 120 {
		t.Fatalf("expected lead minutes to clamp to 120, got %d", status.DefaultLeadMinutes)
	}
	if status.FallbackPolicy != "run_on_next_wake" {
		t.Fatalf("expected fallback policy default, got %q", status.FallbackPolicy)
	}

	reloaded := config.NewManager(configPath)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	cfg := reloaded.Get()
	if !cfg.MacWake.Enabled || cfg.MacWake.DefaultLeadMinutes != 120 {
		t.Fatalf("unexpected persisted mac wake config: %#v", cfg.MacWake)
	}
}

func TestSyncNextWakeSchedulesEarliestCandidate(t *testing.T) {
	service, manager, _ := newTestService(t)
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	cfg := manager.Get()
	cfg.MacWake.Enabled = true
	cfg.MacWake.AdminApprovalGranted = true
	if err := manager.Update(cfg); err != nil {
		t.Fatalf("update config: %v", err)
	}

	var calls [][]string
	service.pmsetRunner = func(args []string, _ bool) error {
		copied := append([]string{}, args...)
		calls = append(calls, copied)
		return nil
	}

	err := service.SyncNextWake([]workspace.WakeCandidate{
		{
			TaskID:      "later",
			RunAt:       now.Add(3 * time.Hour),
			LeadMinutes: 5,
		},
		{
			TaskID:      "earlier",
			RunAt:       now.Add(2 * time.Hour),
			LeadMinutes: 10,
		},
	})
	if err != nil {
		t.Fatalf("sync next wake: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 pmset call, got %d: %#v", len(calls), calls)
	}
	got := calls[0]
	if len(got) != 4 || got[0] != "schedule" || got[1] != "wakeorpoweron" || got[3] != ownerName {
		t.Fatalf("unexpected pmset call: %#v", got)
	}
	expectedWake := now.Add(2*time.Hour - 10*time.Minute).Format("01/02/06 15:04:05")
	if got[2] != expectedWake {
		t.Fatalf("expected wake time %q, got %q", expectedWake, got[2])
	}

	status := service.Status()
	if status.NextWakeTaskID != "earlier" {
		t.Fatalf("expected earliest task to be stored, got %q", status.NextWakeTaskID)
	}
}

func TestSyncNextWakeCancelsStoredWakeWhenDisabled(t *testing.T) {
	service, manager, _ := newTestService(t)
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	storedWake := now.Add(time.Hour)

	cfg := manager.Get()
	cfg.MacWake.Enabled = false
	cfg.MacWake.AdminApprovalGranted = true
	cfg.MacWake.LastScheduledWakeAt = &storedWake
	cfg.MacWake.LastScheduledTaskID = "task-1"
	if err := manager.Update(cfg); err != nil {
		t.Fatalf("update config: %v", err)
	}

	var calls [][]string
	service.pmsetRunner = func(args []string, _ bool) error {
		calls = append(calls, append([]string{}, args...))
		return nil
	}

	if err := service.SyncNextWake(nil); err != nil {
		t.Fatalf("sync next wake: %v", err)
	}
	if len(calls) != 1 || calls[0][1] != "cancel" {
		t.Fatalf("expected cancel pmset call, got %#v", calls)
	}
	if service.Status().NextWakeAt != nil {
		t.Fatal("expected stored wake to be cleared")
	}
}
