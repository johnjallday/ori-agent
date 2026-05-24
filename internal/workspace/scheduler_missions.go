package workspace

import (
	"context"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// checkMissionCadence fires the workspace's mission run when NextMissionRunAt
// has arrived. Designed to be cheap and idempotent — called once per workspace
// per poll tick. The mission system bookkeeping (LastMissionRunAt advance,
// counters, NextMissionRunAt recompute) is delegated to the MissionTrigger
// implementation so the success path stays atomic with the run creation.
//
// Behavior:
//   - Skips workspaces whose missions are disabled, have no cadence, or whose
//     next-run is in the future.
//   - Applies the existing missed-run policy: if the trigger fires after a
//     long downtime, we don't replay every missed cycle — the trigger handles
//     "one catch-up" semantics and the next NextMissionRunAt is recomputed
//     forward.
//   - If no MissionTrigger is configured, this is a no-op (the scheduler
//     boots before the bridge in some test paths; staying silent is safer
//     than failing).
func (ts *TaskScheduler) checkMissionCadence(ws *Workspace, now time.Time) {
	if ws == nil || !ws.MissionEnabled {
		return
	}
	if ws.NextMissionRunAt == nil {
		return
	}
	if ws.NextMissionRunAt.After(now) {
		return
	}
	if ts.missionTrigger == nil {
		// No bridge wired up yet. Note it once at debug so we don't spam
		// the log every minute for every mission-enabled workspace.
		logger.Debug("mission cadence due but no MissionTrigger configured",
			logger.Fields{"workspace_id": ws.ID})
		return
	}

	// CycleOrdinal is 1 + the number of previously-executed mission runs.
	cycleOrdinal := ws.MissionExecutionCount + 1

	// Best-effort run creation. The trigger is responsible for setting
	// LastMissionRunAt/NextMissionRunAt/counters via ApplyMissionRunOutcome
	// inside an atomic store Update; failures here just log and let the
	// next poll tick retry on the same NextMissionRunAt (since the trigger
	// did not advance it).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID, err := ts.missionTrigger.TriggerMissionRun(ctx, ws.ID, cycleOrdinal)
	if err != nil {
		logger.Warn("mission trigger failed", logger.Fields{
			"workspace_id":  ws.ID,
			"cycle_ordinal": cycleOrdinal,
			"error":         err,
		})
		// The trigger records the outcome (counters + LastMissionRunAt +
		// NextMissionRunAt advance) for every path where a run was actually
		// created, so applying it again here would inflate MissionExecutionCount
		// and MissionFailureCount (and skew cycleOrdinal). Only record the
		// outcome ourselves when the trigger failed *before* it could — e.g. a
		// workspace-load or config error — which we detect by NextMissionRunAt
		// still being in the past. This fixes the double-count while still
		// advancing state so the scheduler doesn't re-fire every poll tick.
		_ = ts.workspaceStore.Update(ws.ID, func(w *Workspace) error {
			if w.NextMissionRunAt != nil && !w.NextMissionRunAt.After(now) {
				ApplyMissionRunOutcome(w, MissionRunOutcome{StartedAt: now, Succeeded: false})
			}
			return nil
		})
		return
	}

	logger.Info("mission run triggered", logger.Fields{
		"workspace_id":  ws.ID,
		"cycle_ordinal": cycleOrdinal,
		"run_id":        runID,
	})
	// Success bookkeeping is the trigger's responsibility — but as a
	// belt-and-braces guard against triggers that forget to advance state,
	// re-check whether NextMissionRunAt was bumped. If still in the past,
	// nudge it forward so we don't loop. (No counter changes here — those
	// belong to the trigger so the counts stay consistent with the run.)
	if updated, err := ts.workspaceStore.Get(ws.ID); err == nil {
		if updated.NextMissionRunAt != nil && !updated.NextMissionRunAt.After(now) && updated.Cadence != nil {
			_ = ts.workspaceStore.Update(ws.ID, func(w *Workspace) error {
				next := CalculateNextRun(*w.Cadence, now)
				w.NextMissionRunAt = next
				return nil
			})
		}
	}
}

// TriggerMissionManually fires a mission run on demand regardless of cadence.
// Returns the new run's ID. Used by both the manual-trigger HTTP endpoint and
// the "Run baseline now" UI action. Increments the cycle ordinal just like a
// cadence-triggered run so manual + scheduled runs share a monotonic counter.
//
// Lives on TaskScheduler because TaskScheduler already owns the MissionTrigger
// reference — keeping the dependency in one place avoids passing the trigger
// around as a function argument everywhere it might be needed.
func (ts *TaskScheduler) TriggerMissionManually(ctx context.Context, workspaceID string) (string, error) {
	if ts.missionTrigger == nil {
		return "", ErrMissionTriggerNotConfigured
	}
	ws, err := ts.workspaceStore.Get(workspaceID)
	if err != nil {
		return "", err
	}
	if !MissionBindingsReady(ws) {
		return "", ErrMissionBindingsUnclassified
	}
	cycleOrdinal := ws.MissionExecutionCount + 1
	return ts.missionTrigger.TriggerMissionRun(ctx, workspaceID, cycleOrdinal)
}
