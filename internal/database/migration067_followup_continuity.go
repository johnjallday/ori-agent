package database

import (
	"context"
	"fmt"
)

// Imported foreign source identifiers are historical provenance, never local
// account or deduplication authority. The canonical follow-up stores denied
// source_account_id/dedup_key; this installation-local sidecar preserves their
// exact historical values for later reviewed reconnection and re-export. It
// follows canonical deletion and is never a portable trust/approval record.
func (db *DB) migration067FollowupContinuity(ctx context.Context) error {
	// Historic partial-schema upgrade fixtures can lack the entire domain.
	// As with migration 64, never fabricate a missing canonical owner table.
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='personal_hq_followup'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS followup_continuity_source_refs (
		followup_id TEXT PRIMARY KEY REFERENCES personal_hq_followup(id) ON DELETE CASCADE,
		workspace_id TEXT NOT NULL,
		operation_id TEXT NOT NULL REFERENCES continuity_operations(id) ON DELETE CASCADE,
		source_account_id TEXT NOT NULL DEFAULT '',
		source_dedup_key TEXT NOT NULL DEFAULT '',
		related_workspace_id TEXT NOT NULL DEFAULT '',
		related_task_id TEXT NOT NULL DEFAULT '',
		saved_at DATETIME NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create follow-up continuity provenance: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_personal_hq_followup_continuity_owner
		ON personal_hq_followup(workspace_id,id)`); err != nil {
		return fmt.Errorf("index follow-up continuity owner: %w", err)
	}
	// Canonical mutations dirty the exact owner, including the former owner
	// when a follow-up moves between workspaces. Pure same-owner edits need
	// one new sequence, not a full database scan. The inert source sidecar is
	// also dirty when a user later reconciles a historical reference.
	for _, stmt := range []string{
		`CREATE TRIGGER IF NOT EXISTS continuity_followup_insert AFTER INSERT ON personal_hq_followup BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT NEW.workspace_id,1 WHERE COALESCE(NEW.workspace_id,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_followup_update AFTER UPDATE ON personal_hq_followup BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT OLD.workspace_id,1 WHERE COALESCE(OLD.workspace_id,'')<>'' AND OLD.workspace_id!=NEW.workspace_id
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error='';
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT NEW.workspace_id,1 WHERE COALESCE(NEW.workspace_id,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_followup_delete BEFORE DELETE ON personal_hq_followup BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT OLD.workspace_id,1 WHERE COALESCE(OLD.workspace_id,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_followup_reference_insert AFTER INSERT ON followup_continuity_source_refs BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT NEW.workspace_id,1 WHERE COALESCE(NEW.workspace_id,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_followup_reference_update AFTER UPDATE ON followup_continuity_source_refs BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT OLD.workspace_id,1 WHERE COALESCE(OLD.workspace_id,'')<>'' AND OLD.workspace_id!=NEW.workspace_id
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error='';
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT NEW.workspace_id,1 WHERE COALESCE(NEW.workspace_id,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_followup_reference_delete BEFORE DELETE ON followup_continuity_source_refs BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT OLD.workspace_id,1 WHERE COALESCE(OLD.workspace_id,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("install follow-up continuity dirty trigger: %w", err)
		}
	}
	return nil
}
