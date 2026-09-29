package workspacecontinuity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// nativeCreationTarget is reserved to the explicit canonical creation owner.
// Reusing the durable file barrier avoids an independently expiring lease:
// pending AND failed creation remain non-runnable across process restarts.
const nativeCreationTarget = "native-create"

// NativeCreation grants only installation-local persistence during provisioning.
// It is not runtime admission. The canonical owner retains its reset permit and
// writer locks through insertion, folder/profile publication and Complete/Fail.
// A crash leaves an exact-token barrier for explicit owner reconciliation.
type NativeCreation struct {
	store    *LocalStore
	mutation *FileMutation
}

// BeginNativeCreation is ONLY for explicit creation, never discovery/import or
// Save of an arbitrary unknown ID. The supplied canonical insertion and local
// provisional attachment share one transaction. It cannot adopt an existing,
// deleted, detached or imported identity, or promote a directory by its marker.
// insert must use tx, not the single-connection store pool, and insert only this
// owner's new canonical record. Rollback removes every effect on any failure.
func (s *LocalStore) BeginNativeCreation(ctx context.Context, workspaceID string, insert func(*sql.Tx) error) (*NativeCreation, error) {
	if !ValidID(workspaceID) || insert == nil {
		return nil, ErrInvalid
	}
	mutation := &FileMutation{store: s, workspaceID: workspaceID, target: nativeCreationTarget, token: uuid.NewString()}
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM workspaces WHERE id=?) +
			(SELECT COUNT(*) FROM continuity_attachments WHERE workspace_id=?)`, workspaceID, workspaceID).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			return ErrConflict
		}
		if err := insert(tx); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE id=? AND owner_user_id='local' AND deleted_at IS NULL`, workspaceID).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 {
			return ErrConflict
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO continuity_attachments(workspace_id,state,disposition,updated_at) VALUES (?,'native','ordinary',?)`, workspaceID, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO continuity_dirty(workspace_id,sequence) VALUES (?,1)
			ON CONFLICT(workspace_id) DO UPDATE SET sequence=sequence+1,last_error=''`, workspaceID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO continuity_file_mutations(workspace_id,target,token,state,started_at) VALUES (?,?,?,'pending',?)`, workspaceID, nativeCreationTarget, mutation.token, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &NativeCreation{store: s, mutation: mutation}, nil
}

// Complete requires final canonical SQL/file validation. Only its exact barrier
// is cleared. It never recreates reset/deleted state, calls a provider, generates
// a checkpoint, or blesses another concurrent mutation. Repeat completion is a
// conflict: callers recover from the canonical outcome, not an old token.
func (n *NativeCreation) Complete(ctx context.Context, validate func(Queryer) error) error {
	if n == nil || n.store == nil || n.mutation == nil || validate == nil {
		return ErrInvalid
	}
	return n.store.ReconcileFileMutation(ctx, n.mutation.workspaceID,
		FileMutationBarrier{Target: nativeCreationTarget, Token: n.mutation.token}, validate)
}

// Applied on both live completion and exact-token recovery after a crash.
func validateNativeCompletion(ctx context.Context, q Queryer, workspaceID string) error {
	a, err := readAttachment(ctx, q, workspaceID)
	if err != nil {
		return err
	}
	if a.State != Native || !a.Provisioning {
		return ErrConflict
	}
	// Clearing the outer creation barrier cannot hide inner failed cleanup.
	var other int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_file_mutations WHERE workspace_id=? AND target<>?`, workspaceID, nativeCreationTarget).Scan(&other); err != nil {
		return err
	}
	if other != 0 {
		return ErrMutationPending
	}
	return nil
}

// Fail leaves the canonical row and any durable new folder recoverable but
// inactive. Cleanup must retain the reset permit; request cancellation does not
// authorize forgetting the partial outcome. No pre-existing data is deleted.
func (n *NativeCreation) Fail(ctx context.Context, cause error) error {
	if n == nil || n.mutation == nil {
		return ErrInvalid
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return errors.Join(cause, n.mutation.Complete(cleanup, false))
}
