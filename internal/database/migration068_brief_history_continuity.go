package database

import (
	"context"
	"fmt"
)

// Brief revision creation, replacement of the current selection, and history
// retention/removal must dirty their exact workspace. A date-grouped Today
// projection cannot act as the source of a portable snapshot.
func (db *DB) migration068BriefHistoryContinuity(ctx context.Context) error {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='daily_brief_revision'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	} // Older partial-schema migration fixtures.
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_daily_brief_continuity_owner ON daily_brief_revision(workspace_id,id)`,
		`CREATE TRIGGER IF NOT EXISTS continuity_brief_revision_insert AFTER INSERT ON daily_brief_revision BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT NEW.workspace_id,1 WHERE COALESCE(NEW.workspace_id,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_brief_revision_update AFTER UPDATE ON daily_brief_revision BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT OLD.workspace_id,1 WHERE COALESCE(OLD.workspace_id,'')<>'' AND OLD.workspace_id!=NEW.workspace_id
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error='';
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT NEW.workspace_id,1 WHERE COALESCE(NEW.workspace_id,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_brief_revision_delete BEFORE DELETE ON daily_brief_revision BEGIN
			INSERT INTO continuity_dirty(workspace_id,sequence) SELECT OLD.workspace_id,1 WHERE COALESCE(OLD.workspace_id,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''; END`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("install brief revision continuity: %w", err)
		}
	}
	return nil
}
