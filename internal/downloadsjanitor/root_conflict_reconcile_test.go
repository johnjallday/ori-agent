package downloadsjanitor

import (
	"path/filepath"
	"testing"
	"time"
)

// legacyOverlapService builds workspaces that ALREADY overlap, which is only
// possible because nothing prevented it before this release. Setup's own
// conflict check is bypassed by writing settings directly, exactly as an older
// build would have left them.
func legacyOverlapService(t *testing.T) (*Service, string) {
	t.Helper()
	store, _ := newTestStore(t)
	service := NewService(store, listableFakeStore{newFakeWorkspaceStore("ws-a", "ws-b", "ws-c")})
	return service, tempDirCanonical(t)
}

func seedLegacySetup(t *testing.T, service *Service, workspaceID, root string, setupAt time.Time) {
	t.Helper()
	mkdir(t, root)
	if _, err := service.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		settings.WorkspaceID = workspaceID
		settings.RootPath = root
		settings.DirectoryReferenceID = "dir-" + workspaceID
		settings.FilingRootName = DefaultFilingRootName
		settings.SetupCompletedAt = setupAt
		return nil
	}); err != nil {
		t.Fatalf("seed %s: %v", workspaceID, err)
	}
}

// TestReconcile_KeepsTheEarliestOwnerAndPausesTheLater is task 3.14. Two
// workspaces already manage the same folder; migration must not pick a folder
// for anyone. It keeps the first owner running and pauses the second with a
// repairable explanation.
func TestReconcile_KeepsTheEarliestOwnerAndPausesTheLater(t *testing.T) {
	service, base := legacyOverlapService(t)
	shared := filepath.Join(base, "Downloads")
	earlier := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	seedLegacySetup(t, service, "ws-a", shared, earlier)
	seedLegacySetup(t, service, "ws-b", shared, earlier.Add(48*time.Hour))

	result := service.ReconcileOverlappingRoots([]string{"ws-a", "ws-b"})
	if result.Configured != 2 {
		t.Fatalf("configured = %d", result.Configured)
	}
	if result.Paused != 1 {
		t.Fatalf("paused = %d, want exactly the later workspace", result.Paused)
	}

	// The earliest owner is untouched and still running.
	first, err := service.store.LoadSettings("ws-a")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if first.Paused {
		t.Fatal("the earliest owner must not be paused")
	}
	if first.RootConflictWorkspaceID != "" {
		t.Fatalf("the earliest owner was flagged: %q", first.RootConflictWorkspaceID)
	}
	if first.RootPath != shared {
		t.Fatal("reconciliation changed the earliest owner's folder")
	}

	// The later one is paused, flagged, and — critically — still points at its
	// own folder. Choosing a different one is the user's call.
	second, err := service.store.LoadSettings("ws-b")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !second.Paused {
		t.Fatal("the later workspace should be paused")
	}
	if second.RootConflictWorkspaceID != "ws-a" {
		t.Fatalf("conflict owner = %q, want ws-a", second.RootConflictWorkspaceID)
	}
	if second.RootPath != shared {
		t.Fatalf("reconciliation silently changed a folder: %q", second.RootPath)
	}
	if second.DirectoryReferenceID == "" {
		t.Fatal("the folder grant was released; reconciliation must preserve all data")
	}
}

// TestReconcile_SurfacesTheConflictAsNeedsAttention proves the pause is
// explained rather than silent: readiness reports a failing check with a repair.
func TestReconcile_SurfacesTheConflictAsNeedsAttention(t *testing.T) {
	service, base := legacyOverlapService(t)
	shared := filepath.Join(base, "Downloads")
	seedLegacySetup(t, service, "ws-a", shared, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	seedLegacySetup(t, service, "ws-b", shared, time.Date(2026, 1, 3, 9, 0, 0, 0, time.UTC))

	service.ReconcileOverlappingRoots([]string{"ws-a", "ws-b"})

	status, err := service.Status("ws-b")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Readiness.State != ReadinessNeedsAttention {
		t.Fatalf("state = %q, want needs_attention", status.Readiness.State)
	}
	failing := status.Readiness.Failing()
	if len(failing) == 0 {
		t.Fatal("a paused conflict must surface as a failing check")
	}
	found := false
	for _, check := range failing {
		if check.Code == CodeFolderConflict {
			found = true
			if check.Repair == "" {
				t.Fatal("the conflict must offer a repair")
			}
			if check.Message == "" {
				t.Fatal("the conflict must explain itself")
			}
		}
	}
	if !found {
		t.Fatalf("no folder-conflict check among %+v", failing)
	}
}

func TestReconcile_DetectsNestedOverlapsNotJustExactMatches(t *testing.T) {
	service, base := legacyOverlapService(t)
	parent := filepath.Join(base, "Downloads")
	child := filepath.Join(base, "Downloads", "Invoices")

	seedLegacySetup(t, service, "ws-a", parent, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	seedLegacySetup(t, service, "ws-b", child, time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC))

	if result := service.ReconcileOverlappingRoots([]string{"ws-a", "ws-b"}); result.Paused != 1 {
		t.Fatalf("paused = %d, want the nested workspace", result.Paused)
	}
	second, _ := service.store.LoadSettings("ws-b")
	if !second.Paused || second.RootConflictWorkspaceID != "ws-a" {
		t.Fatalf("nested overlap not detected: %+v", second)
	}
}

func TestReconcile_LeavesNonOverlappingWorkspacesAlone(t *testing.T) {
	service, base := legacyOverlapService(t)
	seedLegacySetup(t, service, "ws-a", filepath.Join(base, "Downloads"), time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	seedLegacySetup(t, service, "ws-b", filepath.Join(base, "Scans"), time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC))

	result := service.ReconcileOverlappingRoots([]string{"ws-a", "ws-b"})
	if result.Paused != 0 {
		t.Fatalf("paused = %d, want 0", result.Paused)
	}
	for _, id := range []string{"ws-a", "ws-b"} {
		settings, _ := service.store.LoadSettings(id)
		if settings.Paused || settings.RootConflictWorkspaceID != "" {
			t.Fatalf("%s was disturbed: %+v", id, settings)
		}
	}
}

// TestReconcile_IsIdempotentAcrossRestarts proves repeated startups converge
// rather than accumulating pauses or flipping the winner.
func TestReconcile_IsIdempotentAcrossRestarts(t *testing.T) {
	service, base := legacyOverlapService(t)
	shared := filepath.Join(base, "Downloads")
	seedLegacySetup(t, service, "ws-a", shared, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	seedLegacySetup(t, service, "ws-b", shared, time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC))

	first := service.ReconcileOverlappingRoots([]string{"ws-a", "ws-b"})
	if first.Paused != 1 {
		t.Fatalf("first pass paused %d", first.Paused)
	}
	for i := range 3 {
		again := service.ReconcileOverlappingRoots([]string{"ws-a", "ws-b"})
		if again.Paused != 0 {
			t.Fatalf("pass %d paused %d workspaces again", i+2, again.Paused)
		}
	}

	// The same workspace is still the winner.
	a, _ := service.store.LoadSettings("ws-a")
	b, _ := service.store.LoadSettings("ws-b")
	if a.Paused || !b.Paused {
		t.Fatalf("the winner flipped between passes: a.paused=%v b.paused=%v", a.Paused, b.Paused)
	}
}

// TestReconcile_ClearsAResolvedConflict covers recovery: once the other
// workspace releases the folder, the flag goes away and readiness recovers.
//
// It deliberately does not un-pause — the user may have paused it themselves
// afterwards, and resuming unattended file work on their behalf is not a
// reconciliation pass's decision.
func TestReconcile_ClearsAResolvedConflict(t *testing.T) {
	service, base := legacyOverlapService(t)
	shared := filepath.Join(base, "Downloads")
	seedLegacySetup(t, service, "ws-a", shared, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	seedLegacySetup(t, service, "ws-b", shared, time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC))
	service.ReconcileOverlappingRoots([]string{"ws-a", "ws-b"})

	// ws-a releases the folder.
	if _, err := service.RevokeAccess(nil, "ws-a"); err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}

	result := service.ReconcileOverlappingRoots([]string{"ws-a", "ws-b"})
	if result.Cleared != 1 {
		t.Fatalf("cleared = %d, want the resolved conflict", result.Cleared)
	}
	b, _ := service.store.LoadSettings("ws-b")
	if b.RootConflictWorkspaceID != "" {
		t.Fatalf("the resolved conflict is still recorded: %q", b.RootConflictWorkspaceID)
	}
	if !b.Paused {
		t.Fatal("reconciliation resumed unattended work on the user's behalf")
	}
}

func TestReconcile_IgnoresUnconfiguredWorkspaces(t *testing.T) {
	service, base := legacyOverlapService(t)
	seedLegacySetup(t, service, "ws-a", filepath.Join(base, "Downloads"), time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))

	result := service.ReconcileOverlappingRoots([]string{"ws-a", "ws-b", "ws-c"})
	if result.Configured != 1 {
		t.Fatalf("configured = %d, want only the set-up workspace", result.Configured)
	}
	if result.Paused != 0 {
		t.Fatalf("paused = %d", result.Paused)
	}
}
