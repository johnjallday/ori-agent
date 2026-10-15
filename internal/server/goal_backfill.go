package server

import (
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// runGoalBackfill copies each workspace's setup-time goal into its empty
// Workspace.Mission, once per data dir (workspace-goal-suggestions FR6), and
// logs one summary line. Non-fatal: a failure leaves workspaces unchanged.
func runGoalBackfill(store workspace.Store) {
	if store == nil {
		return
	}
	report, err := workspace.BackfillGoalsFromBootstrap(store, config.DefaultDataDir())
	if err != nil {
		logger.Warn("Workspace goal backfill did not run", logger.Fields{"error": err})
		return
	}
	if report.AlreadyDone {
		return
	}
	fields := logger.Fields{"scanned": report.Scanned, "written": report.Written, "error_count": len(report.Errors)}
	if len(report.Errors) > 0 {
		fields["first_error"] = report.Errors[0].Error()
	}
	logger.Info("Workspace goal backfill complete", fields)
}
