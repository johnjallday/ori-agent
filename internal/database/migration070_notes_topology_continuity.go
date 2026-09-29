package database

import (
	"context"
	"database/sql"
	"fmt"
)

// migration070NotesTopologyContinuity dirties the owning workspace when a
// note or its tags change, and dirties a parent when a child workspace is
// added, removed or moved: a parent's checkpoint names its physical children.
// Like the earlier continuity triggers these only bump a local counter; they
// never grant attachment or execution.
func (db *DB) migration070NotesTopologyContinuity(ctx context.Context) error {
	// continuity_installs is a confirmed import's install plan: where each
	// reviewed member's folder goes, so a retried or restarted operation
	// resumes into the same place. It is local receipt state, reset with it.
	statements := []string{`CREATE TABLE IF NOT EXISTS continuity_installs (
		operation_id TEXT NOT NULL REFERENCES continuity_operations(id),
		workspace_id TEXT NOT NULL,
		parent_id TEXT NOT NULL DEFAULT '',
		source_dir TEXT NOT NULL,
		folder_slug TEXT NOT NULL,
		in_place INTEGER NOT NULL DEFAULT 0,
		installed INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(operation_id, workspace_id)
	)`}
	exists := func(table string) (bool, error) {
		var count int
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
		return count == 1, err
	}
	dirty := func(expr string) string {
		return fmt.Sprintf(`INSERT INTO continuity_dirty(workspace_id,sequence) SELECT %[1]s,1 WHERE COALESCE(%[1]s,'')<>''
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error='';`, expr)
	}
	if ok, err := exists("workspace_notes"); err != nil {
		return err
	} else if ok {
		statements = append(statements,
			`CREATE TRIGGER IF NOT EXISTS continuity_workspace_notes_insert AFTER INSERT ON workspace_notes BEGIN `+dirty("NEW.workspace_id")+` END`,
			`CREATE TRIGGER IF NOT EXISTS continuity_workspace_notes_update AFTER UPDATE ON workspace_notes BEGIN `+dirty("OLD.workspace_id")+dirty("NEW.workspace_id")+` END`,
			`CREATE TRIGGER IF NOT EXISTS continuity_workspace_notes_delete BEFORE DELETE ON workspace_notes BEGIN `+dirty("OLD.workspace_id")+` END`,
		)
		if ok, err := exists("note_tags"); err != nil {
			return err
		} else if ok {
			owner := func(ref string) string {
				return dirty("(SELECT n.workspace_id FROM workspace_notes n WHERE n.id=" + ref + ".note_id)")
			}
			statements = append(statements,
				`CREATE TRIGGER IF NOT EXISTS continuity_note_tags_insert AFTER INSERT ON note_tags BEGIN `+owner("NEW")+` END`,
				`CREATE TRIGGER IF NOT EXISTS continuity_note_tags_delete BEFORE DELETE ON note_tags BEGIN `+owner("OLD")+` END`,
			)
		}
	}
	// Older focused fixtures carry a workspaces table without hierarchy.
	var parentColumns int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('workspaces') WHERE name IN ('parent_id','folder_slug','deleted_at')`).Scan(&parentColumns); err != nil {
		return err
	}
	if parentColumns == 3 {
		statements = append(statements,
			`CREATE TRIGGER IF NOT EXISTS continuity_workspace_parent_insert AFTER INSERT ON workspaces BEGIN `+dirty("NEW.parent_id")+` END`,
			`CREATE TRIGGER IF NOT EXISTS continuity_workspace_parent_update AFTER UPDATE OF parent_id, folder_slug, deleted_at ON workspaces BEGIN `+dirty("OLD.parent_id")+dirty("NEW.parent_id")+` END`,
			`CREATE TRIGGER IF NOT EXISTS continuity_workspace_parent_delete BEFORE DELETE ON workspaces BEGIN `+dirty("OLD.parent_id")+` END`,
		)
	}
	return db.InTransaction(ctx, func(tx *sql.Tx) error {
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("install notes/topology continuity: %w", err)
			}
		}
		return nil
	})
}
