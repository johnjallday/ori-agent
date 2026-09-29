package workspacecontinuity

import (
	"context"
	"database/sql"
	"time"
)

// MarkLegacyAdoption records, in the caller's adoption transaction, that the
// user's confirmed Import Folder of an older (pre-checkpoint) Personal HQ made
// its assistant this installation's: the workspace becomes an adopted import
// whose assistant profile is the folder's own, with background routines off
// until they are turned on here. Only a native (or not yet registered)
// workspace can be marked; anything already imported, restoring or detached
// is a conflict.
func MarkLegacyAdoption(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	if tx == nil || !ValidID(workspaceID) {
		return ErrInvalid
	}
	attachment, err := readAttachment(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if attachment.Version == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO continuity_attachments(workspace_id,state,disposition,updated_at) VALUES (?,?,?,?)`,
			workspaceID, ImportedInactive, AdoptedHQ, now); err != nil {
			return err
		}
	} else {
		if attachment.State != Native || attachment.Provisioning {
			return ErrConflict
		}
		result, err := tx.ExecContext(ctx, `UPDATE continuity_attachments SET state=?,disposition=?,version=version+1,updated_at=?
			WHERE workspace_id=? AND state=? AND version=?`, ImportedInactive, AdoptedHQ, now, workspaceID, Native, attachment.Version)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return ErrConflict
		}
	}
	// Its first checkpoint here is prepared by this installation.
	_, err = tx.ExecContext(ctx, `INSERT INTO continuity_dirty(workspace_id,sequence) VALUES (?,1)
		ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1`, workspaceID)
	return err
}
