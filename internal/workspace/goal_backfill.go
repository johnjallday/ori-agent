package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GoalBackfillMarker is the data-dir file that records the one-time copy of
// each workspace's setup-time goal (workspace_bootstrap.goal) into
// Workspace.Mission. Its presence makes every later startup a no-op, so a goal
// the user clears afterwards is never filled back in.
const GoalBackfillMarker = "workspace_goals_backfilled.json"

// GoalBackfillReport summarizes one backfill pass.
type GoalBackfillReport struct {
	// AlreadyDone is true when the marker existed and nothing was read.
	AlreadyDone bool
	// Scanned counts workspaces read; Written counts goals copied.
	Scanned int
	Written int
	// Errors holds per-workspace failures. The pass still completes and the
	// marker is still written: a failed copy is logged, not retried forever.
	Errors []error
}

type goalBackfillMarker struct {
	CompletedAt time.Time `json:"completed_at"`
	Written     int       `json:"written"`
}

// BootstrapGoal returns the setup-time goal stored in a workspace's legacy
// workspace_bootstrap shared data, or "". Only the backfill reads it: every
// prompt and score reads Workspace.Mission instead.
func BootstrapGoal(ws *Workspace) string {
	if ws == nil || ws.SharedData == nil {
		return ""
	}
	bootstrap, ok := ws.SharedData["workspace_bootstrap"].(map[string]any)
	if !ok {
		return ""
	}
	goal, _ := bootstrap["goal"].(string)
	return strings.TrimSpace(goal)
}

// BackfillGoalsFromBootstrap copies a non-empty bootstrap goal into every
// workspace whose Mission is empty, once per data dir. It writes through the
// given store (the composed SyncStore in production, so workspace.json and
// SQLite both update), never changes MissionEnabled or the cadence, and never
// starts a run.
func BackfillGoalsFromBootstrap(store Store, dataDir string) (GoalBackfillReport, error) {
	var report GoalBackfillReport
	if store == nil || strings.TrimSpace(dataDir) == "" {
		return report, errors.New("goal backfill needs a workspace store and a data dir")
	}
	markerPath := filepath.Join(dataDir, GoalBackfillMarker)
	if _, err := os.Stat(markerPath); err == nil {
		report.AlreadyDone = true
		return report, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return report, fmt.Errorf("read goal backfill marker: %w", err)
	}

	ids, err := store.List()
	if err != nil {
		return report, fmt.Errorf("list workspaces: %w", err)
	}
	for _, id := range ids {
		ws, err := store.Get(id)
		if err != nil || ws == nil {
			continue
		}
		report.Scanned++
		if strings.TrimSpace(ws.Mission) != "" || BootstrapGoal(ws) == "" {
			continue
		}
		wrote := false
		if err := store.Update(id, func(current *Workspace) error {
			// Re-check inside the update: a goal saved since the read wins.
			if strings.TrimSpace(current.Mission) != "" {
				return nil
			}
			goal := BootstrapGoal(current)
			if goal == "" {
				return nil
			}
			current.Mission = goal
			wrote = true
			return nil
		}); err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("workspace %s: %w", id, err))
			continue
		}
		if wrote {
			report.Written++
		}
	}

	data, err := json.MarshalIndent(goalBackfillMarker{CompletedAt: time.Now().UTC(), Written: report.Written}, "", "  ")
	if err != nil {
		return report, err
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return report, fmt.Errorf("create data dir for goal backfill marker: %w", err)
	}
	if err := os.WriteFile(markerPath, data, 0o600); err != nil {
		return report, fmt.Errorf("write goal backfill marker: %w", err)
	}
	return report, nil
}
