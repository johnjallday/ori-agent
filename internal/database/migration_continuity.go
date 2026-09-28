package database

import (
	"context"
	"database/sql"
	"fmt"
)

// Continuity metadata is local coordination, never a second live domain store.
// In particular, the dirty queue cannot attach a discovered directory or grant
// execution. Attachment/admission must be written separately after validation.
func (db *DB) migration064WorkspaceContinuity(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS continuity_dirty (
			workspace_id TEXT PRIMARY KEY,
			sequence INTEGER NOT NULL DEFAULT 1 CHECK(sequence > 0),
			prepared_sequence INTEGER NOT NULL DEFAULT 0 CHECK(prepared_sequence >= 0),
			generation TEXT NOT NULL DEFAULT '',
			checkpoint_at DATETIME,
			last_error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS continuity_operations (
			id TEXT PRIMARY KEY,
			tree_digest TEXT NOT NULL,
			destination_digest TEXT NOT NULL,
			owner_user_id TEXT NOT NULL,
			action TEXT NOT NULL CHECK(action IN ('continue','workspace_only')),
			status TEXT NOT NULL CHECK(status IN ('restoring','interrupted','complete')),
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(tree_digest, owner_user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS continuity_attachments (
			workspace_id TEXT PRIMARY KEY,
			state TEXT NOT NULL CHECK(state IN ('native','unreviewed','restoring','imported_inactive','imported_active','detached')),
			disposition TEXT NOT NULL CHECK(disposition IN ('ordinary','adopted_hq','workspace_only_hq')),
			operation_id TEXT NOT NULL DEFAULT '',
			source_generation TEXT NOT NULL DEFAULT '',
			source_digest TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS continuity_components (
			operation_id TEXT NOT NULL REFERENCES continuity_operations(id),
			workspace_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('restoring','restored','unavailable','unsupported','conflict','failed','interrupted')),
			record_count INTEGER NOT NULL DEFAULT 0 CHECK(record_count >= 0),
			reason TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			PRIMARY KEY(operation_id, workspace_id, domain)
		)`,
		`CREATE TABLE IF NOT EXISTS continuity_records (
			domain TEXT NOT NULL,
			family TEXT NOT NULL,
			record_id TEXT NOT NULL,
			operation_id TEXT NOT NULL REFERENCES continuity_operations(id),
			workspace_id TEXT NOT NULL,
			source_digest TEXT NOT NULL,
			PRIMARY KEY(domain, family, record_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_continuity_records_operation ON continuity_records(operation_id, workspace_id)`,
		`CREATE INDEX IF NOT EXISTS idx_continuity_attachments_operation ON continuity_attachments(operation_id)`,
	}
	// These identifiers are compiled constants, never persisted or user-supplied
	// SQL. BEFORE DELETE captures ownership before the canonical row disappears.
	for _, owner := range []struct{ table, column string }{
		{"workspaces", "id"},
		{"personal_assistant_state", "hq_workspace_id"},
		{"daily_brief_config", "workspace_id"},
		{"users", "personal_workspace_id"},
	} {
		// Older partial installations (and focused migration fixtures) may lack
		// a domain entirely. Do not invent its canonical table here. Its adapter
		// remains unavailable; current full schemas always install every trigger.
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, owner.table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			continue
		}
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			refs := []string{"NEW"}
			when := "AFTER"
			switch event {
			case "DELETE":
				refs, when = []string{"OLD"}, "BEFORE"
			case "UPDATE":
				refs = []string{"OLD", "NEW"}
			}
			body := ""
			for _, ref := range refs {
				body += fmt.Sprintf(`INSERT INTO continuity_dirty(workspace_id, sequence)
					SELECT %[1]s.%[2]s, 1 WHERE COALESCE(%[1]s.%[2]s, '') <> ''
					ON CONFLICT(workspace_id) DO UPDATE SET sequence = sequence + 1, last_error = '';`, ref, owner.column)
			}
			if owner.table == "workspaces" && event == "DELETE" {
				body += `UPDATE continuity_attachments SET state='detached',version=version+1,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=OLD.id;`
			}
			statements = append(statements, fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS continuity_%s_%s %s %s ON %s BEGIN %s END`, owner.table, event, when, event, owner.table, body))
		}
	}
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("create continuity metadata: %w", err)
			}
		}
		return nil
	})
}
