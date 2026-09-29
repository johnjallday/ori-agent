package workspacecontinuity

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrMutationPending = errors.New("continuity canonical file mutation requires completion or reconciliation")

// FileMutation is a durable barrier, not a portable lock or execution grant.
// The canonical owner must serialize writes to target and retain its reset work
// permit through Complete. Losing this handle/crashing leaves the barrier in SQL;
// neither elapsed time nor a new process is proof that the write completed.
type FileMutation struct {
	store                      *LocalStore
	workspaceID, target, token string
	mu                         sync.Mutex
	finished, succeeded        bool
}

// BeginFileMutation precedes the first canonical file change. A full replacement
// may supersede a reported FAILED attempt under the same owner lock, but cannot
// supersede a still-pending writer. Markers are bounded to one per owner target.
// Use a separate target for an enclosing multi-store operation (e.g. sync-save).
func (s *LocalStore) BeginFileMutation(ctx context.Context, workspaceID, target string) (*FileMutation, error) {
	if !ValidID(workspaceID) || !canonicalPath(target) || len(target) > 1024 || target == nativeCreationTarget {
		return nil, ErrInvalid
	}
	mutation := &FileMutation{store: s, workspaceID: workspaceID, target: target, token: uuid.NewString()}
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := invalidateAdmitted(ctx, tx, workspaceID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO continuity_file_mutations(workspace_id,target,token,state,started_at)
			VALUES (?,?,?,'pending',?) ON CONFLICT(workspace_id,target) DO UPDATE
			SET token=excluded.token,state='pending',started_at=excluded.started_at WHERE continuity_file_mutations.state='failed'`,
			workspaceID, target, mutation.token, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := requireOne(result); err != nil {
			return ErrMutationPending
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return mutation, nil
}

// invalidateAdmitted never inserts metadata. A late completion after reset,
// detachment, deletion or ownership loss cannot reconstruct admission/dirty rows.
func invalidateAdmitted(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	result, err := tx.ExecContext(ctx, `UPDATE continuity_dirty SET sequence=sequence+1,last_error=''
		WHERE workspace_id=? AND EXISTS(SELECT 1 FROM workspaces w JOIN continuity_attachments a ON a.workspace_id=w.id
		WHERE w.id=continuity_dirty.workspace_id AND w.owner_user_id='local' AND w.deleted_at IS NULL
		AND a.state IN ('native','imported_inactive','imported_active'))`, workspaceID)
	if err != nil {
		return err
	}
	return requireOne(result)
}

// Complete records the WHOLE owner's outcome, including cleanup and callbacks.
// A failure remains a barrier even if another SQL trigger clears last_error.
// Retry this method after a bookkeeping error; it never changes canonical work.
// Use a bounded cancellation-independent context while retaining the reset permit.
func (m *FileMutation) Complete(ctx context.Context, succeeded bool) error {
	if m == nil || m.store == nil {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished {
		if m.succeeded != succeeded {
			return ErrConflict
		}
		return nil
	}
	err := m.store.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := invalidateAdmitted(ctx, tx, m.workspaceID); err != nil {
			return err
		}
		query := `DELETE FROM continuity_file_mutations WHERE workspace_id=? AND target=? AND token=? AND state='pending'`
		if !succeeded {
			query = `UPDATE continuity_file_mutations SET state='failed' WHERE workspace_id=? AND target=? AND token=? AND state='pending'`
		}
		result, err := tx.ExecContext(ctx, query, m.workspaceID, m.target, m.token)
		if err != nil {
			return err
		}
		return requireOne(result)
	})
	if err == nil {
		m.finished, m.succeeded = true, succeeded
	}
	return err
}

// FileMutationBarrier contains only local coordination, never file content.
type FileMutationBarrier struct {
	Target    string
	Token     string
	Failed    bool
	StartedAt time.Time
}

func (s *LocalStore) FileMutationBarriers(ctx context.Context, workspaceID string) ([]FileMutationBarrier, error) {
	if !ValidID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT target,token,state,started_at FROM continuity_file_mutations
		WHERE workspace_id=? ORDER BY target LIMIT ?`, workspaceID, MaxFiles+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []FileMutationBarrier{}
	for rows.Next() {
		var item FileMutationBarrier
		var state string
		if err := rows.Scan(&item.Target, &item.Token, &state, &item.StartedAt); err != nil {
			return nil, err
		}
		item.Failed = state == "failed"
		result = append(result, item)
		if len(result) > MaxFiles {
			return nil, ErrLimit
		}
	}
	return result, rows.Err()
}

// ReconcileFileMutation is an explicit canonical-owner recovery operation, not
// an expiry or startup-discovery hook. The caller holds the installation lease,
// target's writer lock and reset work permit, and proves no prior writer can
// still publish. Validate must verify/repair canonical SQL/file agreement using
// this shared Queryer (never the single-connection pool). It must not grant
// readiness or register a retained directory. Only this exact marker is cleared;
// final preparation still requires a fresh snapshot and publication fence.
func (s *LocalStore) ReconcileFileMutation(ctx context.Context, workspaceID string, barrier FileMutationBarrier, validate func(Queryer) error) error {
	if !ValidID(workspaceID) || !canonicalPath(barrier.Target) || !validGeneration(barrier.Token) || validate == nil {
		return ErrInvalid
	}
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := invalidateAdmitted(ctx, tx, workspaceID); err != nil {
			return err
		}
		var token string
		err := tx.QueryRowContext(ctx, `SELECT token FROM continuity_file_mutations WHERE workspace_id=? AND target=?`, workspaceID, barrier.Target).Scan(&token)
		if errors.Is(err, sql.ErrNoRows) || err == nil && token != barrier.Token {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if barrier.Target == nativeCreationTarget {
			if err := validateNativeCompletion(ctx, tx, workspaceID); err != nil {
				return err
			}
		}
		if err := validate(tx); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM continuity_file_mutations WHERE workspace_id=? AND target=? AND token=?`, workspaceID, barrier.Target, barrier.Token)
		if err != nil {
			return err
		}
		return requireOne(result)
	})
}
