package server

import (
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// runTicketMigration evolves persisted Tasks into canonical Tickets at startup
// (tasks/prd-workspace-ticket-management.md FR-105, FR-106, FR-126).
//
// It never returns an error and never blocks boot. That is deliberate: a
// workspace the migration cannot read is left exactly as it was, and the
// server still starts. Refusing to boot over one unmigratable workspace would
// take the whole app away from a user whose other workspaces are fine, and the
// canonical read path already tolerates unmigrated records — CanonicalState
// falls back to the legacy status, so those tickets still work.
//
// Repair findings are logged rather than silently discarded, so an
// unrecognized status or an unreadable board date is discoverable after the
// fact.
func runTicketMigration(store workspace.Store) {
	if store == nil {
		return
	}

	results, err := workspace.MigrateAllWorkspaceTickets(store)
	if err != nil {
		logger.Warn("Ticket migration could not enumerate workspaces; existing records are unchanged",
			logger.Fields{"error": err})
		return
	}
	if len(results) == 0 {
		// Every workspace was already at the current version. This is the
		// steady state on all boots after the first.
		return
	}

	migrated, numbered, findings := 0, 0, 0
	for _, result := range results {
		migrated += result.Migrated
		numbered += result.Numbered
		findings += len(result.Findings)

		for _, finding := range result.Findings {
			// Findings carry IDs and field names only, never record content,
			// so they are safe to log.
			logger.Warn("Ticket migration needs review", logger.Fields{
				"workspace_id": result.WorkspaceID,
				"ticket_id":    finding.TicketID,
				"field":        finding.Field,
				"severity":     finding.Severity,
				"summary":      finding.Summary,
			})
		}
	}

	logger.Info("Migrated workspace tasks to canonical tickets", logger.Fields{
		"workspaces": len(results),
		"tickets":    migrated,
		"numbered":   numbered,
		"findings":   findings,
	})
}
