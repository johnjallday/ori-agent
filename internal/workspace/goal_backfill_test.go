package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func saveGoalFixture(t *testing.T, store Store, name, mission, bootstrapGoal string) string {
	t.Helper()
	ws := NewWorkspace(CreateWorkspaceParams{Name: name})
	ws.Mission = mission
	if bootstrapGoal != "" {
		ws.SharedData = map[string]any{"workspace_bootstrap": map[string]any{"goal": bootstrapGoal, "systems": "Figma"}}
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
	return ws.ID
}

// SM3: three workspaces (bootstrap goal only / mission only / both) — exactly
// one Mission is written, and the marker stops a second pass.
func TestBackfillGoalsFromBootstrap(t *testing.T) {
	store := NewInMemoryStore()
	dataDir := t.TempDir()
	bootstrapOnly := saveGoalFixture(t, store, "Bootstrap Only", "", "Ship the launch")
	missionOnly := saveGoalFixture(t, store, "Mission Only", "Grow the newsletter", "")
	both := saveGoalFixture(t, store, "Both", "Keep the brand voice consistent", "Old setup goal")

	report, err := BackfillGoalsFromBootstrap(store, dataDir)
	if err != nil {
		t.Fatalf("BackfillGoalsFromBootstrap: %v", err)
	}
	if report.AlreadyDone || report.Scanned != 3 || report.Written != 1 || len(report.Errors) != 0 {
		t.Fatalf("report = %+v", report)
	}
	for id, want := range map[string]string{
		bootstrapOnly: "Ship the launch",
		missionOnly:   "Grow the newsletter",
		both:          "Keep the brand voice consistent",
	} {
		ws, err := store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if ws.Mission != want {
			t.Fatalf("%s Mission = %q, want %q", ws.Name, ws.Mission, want)
		}
		if ws.MissionEnabled || ws.Cadence != nil || ws.MissionExecutionCount != 0 || ws.GoalFirstRunPending {
			t.Fatalf("%s: backfill must not enable, run or queue the goal: %+v", ws.Name, ws)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, GoalBackfillMarker)); err != nil {
		t.Fatalf("marker not written: %v", err)
	}

	// The user clears the backfilled goal; a second startup must not refill it.
	if err := store.Update(bootstrapOnly, func(ws *Workspace) error {
		ws.Mission = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	second, err := BackfillGoalsFromBootstrap(store, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !second.AlreadyDone || second.Scanned != 0 || second.Written != 0 {
		t.Fatalf("second pass = %+v, want a no-op", second)
	}
	if ws, _ := store.Get(bootstrapOnly); ws.Mission != "" {
		t.Fatalf("a cleared goal was refilled: %q", ws.Mission)
	}
}

func TestBackfillGoalsFromBootstrapNeedsAStoreAndADataDir(t *testing.T) {
	if _, err := BackfillGoalsFromBootstrap(nil, t.TempDir()); err == nil {
		t.Fatal("expected an error without a store")
	}
	if _, err := BackfillGoalsFromBootstrap(NewInMemoryStore(), " "); err == nil {
		t.Fatal("expected an error without a data dir")
	}
}

func TestBootstrapGoal(t *testing.T) {
	if got := BootstrapGoal(nil); got != "" {
		t.Fatalf("nil workspace goal = %q", got)
	}
	ws := &Workspace{SharedData: map[string]any{"workspace_bootstrap": map[string]any{"goal": "  Ship it  "}}}
	if got := BootstrapGoal(ws); got != "Ship it" {
		t.Fatalf("BootstrapGoal = %q", got)
	}
	ws.SharedData["workspace_bootstrap"] = "not a map"
	if got := BootstrapGoal(ws); got != "" {
		t.Fatalf("malformed bootstrap goal = %q", got)
	}
}
