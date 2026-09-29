package workspacecontinuity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

var ErrConflict = errors.New("continuity destination conflict")

// Queryer is deliberately satisfied by a single *sql.Tx. Domain exporters must
// share this reader, not perform independent reads through their normal stores.
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type AttachmentState string

const (
	Native           AttachmentState = "native"
	Unreviewed       AttachmentState = "unreviewed"
	Restoring        AttachmentState = "restoring"
	ImportedInactive AttachmentState = "imported_inactive"
	ImportedActive   AttachmentState = "imported_active"
	Detached         AttachmentState = "detached"
)

type Disposition string

const (
	Ordinary        Disposition = "ordinary"
	AdoptedHQ       Disposition = "adopted_hq"
	WorkspaceOnlyHQ Disposition = "workspace_only_hq"
)

type Attachment struct {
	WorkspaceID      string
	State            AttachmentState
	Disposition      Disposition
	OperationID      string
	SourceGeneration string
	SourceDigest     string
	Version          int64
	UpdatedAt        time.Time
	Provisioning     bool // durable native-create barrier; not a portable flag
}

func (a Attachment) AllowsAutomatic() bool {
	return !a.Provisioning && (a.State == Native || a.State == ImportedActive)
}
func (a Attachment) AllowsPreparation() bool {
	return !a.Provisioning && (a.State == Native || a.State == ImportedInactive || a.State == ImportedActive)
}
func (a Attachment) AllowsManual() bool { return a.AllowsPreparation() }

type LocalStore struct{ db *database.DB }

func NewLocalStore(db *database.DB) *LocalStore { return &LocalStore{db: db} }

func readAttachment(ctx context.Context, query Queryer, workspaceID string) (Attachment, error) {
	var result Attachment
	err := query.QueryRowContext(ctx, `SELECT workspace_id,
		CASE WHEN state IN ('native','imported_inactive','imported_active') AND NOT EXISTS(
			SELECT 1 FROM workspaces w WHERE w.id=a.workspace_id AND w.owner_user_id='local' AND w.deleted_at IS NULL)
		THEN 'detached' ELSE state END,
		disposition, operation_id,
		source_generation, source_digest, version, updated_at,
		EXISTS(SELECT 1 FROM continuity_file_mutations m WHERE m.workspace_id=a.workspace_id AND m.target='native-create')
		FROM continuity_attachments a WHERE workspace_id = ?`, workspaceID).Scan(
		&result.WorkspaceID, &result.State, &result.Disposition, &result.OperationID,
		&result.SourceGeneration, &result.SourceDigest, &result.Version, &result.UpdatedAt, &result.Provisioning)
	if errors.Is(err, sql.ErrNoRows) {
		// Absence is never implicit native permission, even under an approved root.
		return Attachment{WorkspaceID: workspaceID, State: Unreviewed}, nil
	}
	return result, err
}

func (s *LocalStore) Attachment(ctx context.Context, workspaceID string) (Attachment, error) {
	if !ValidID(workspaceID) {
		return Attachment{}, ErrInvalid
	}
	return readAttachment(ctx, s.db, workspaceID)
}

// RegisterNative is for a bounded upgrade inventory of already-authorized,
// fully provisioned local identities. New creation uses BeginNativeCreation so
// authority is not granted before its folder/configuration exists. Discovery
// and rescan must never call either API. A pending creation is not an upgrade.
func (s *LocalStore) RegisterNative(ctx context.Context, workspaceID string) error {
	if !ValidID(workspaceID) {
		return ErrInvalid
	}
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE id = ? AND owner_user_id='local' AND deleted_at IS NULL`, workspaceID).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 {
			return ErrConflict
		}
		attachment, err := readAttachment(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
		if attachment.Version != 0 {
			if attachment.State == Native && !attachment.Provisioning {
				return nil
			}
			return ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO continuity_attachments(workspace_id,state,disposition,updated_at) VALUES (?,'native','ordinary',?)`, workspaceID, time.Now().UTC()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO continuity_dirty(workspace_id,sequence) VALUES (?,1) ON CONFLICT(workspace_id) DO NOTHING`, workspaceID)
		return err
	})
}

// SetImportedActive is called only after a separate local activation review has
// validated current dependencies/grants and source pause intent. CAS prevents a
// stale activation review from changing a newly detached/restoring workspace.
func (s *LocalStore) SetImportedActive(ctx context.Context, workspaceID string, expectedVersion int64, active bool) error {
	if !ValidID(workspaceID) || expectedVersion < 1 {
		return ErrInvalid
	}
	from, to := ImportedInactive, ImportedActive
	if !active {
		from, to = ImportedActive, ImportedInactive
	}
	result, err := s.db.ExecContext(ctx, `UPDATE continuity_attachments SET state=?, version=version+1, updated_at=?
		WHERE workspace_id=? AND version=? AND state=? AND EXISTS(SELECT 1 FROM workspaces w
		WHERE w.id=continuity_attachments.workspace_id AND w.owner_user_id='local' AND w.deleted_at IS NULL)`, to, time.Now().UTC(), workspaceID, expectedVersion, from)
	if err != nil {
		return err
	}
	return requireOne(result)
}

func requireOne(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

// MarkDirty invalidates preparation for a completed canonical change. File
// writers must also hold BeginFileMutation through publication and cleanup;
// a pre-write sequence alone does not fence unfinished writes. SQL domains use
// triggers in the mutation transaction, including deletes and ownership moves.
func (s *LocalStore) MarkDirty(ctx context.Context, workspaceID string) error {
	if !ValidID(workspaceID) {
		return ErrInvalid
	}
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		return invalidateAdmitted(ctx, tx, workspaceID)
	})
}

type Preparation struct {
	Sequence         int64
	PreparedSequence int64
	Generation       string
	CheckpointAt     *time.Time
	LastError        string
	Admitted         bool
	PendingMutation  bool // includes failed writes awaiting replacement/reconciliation
	MutationFailed   bool
}

func (p Preparation) Ready() bool {
	return p.Admitted && !p.PendingMutation && p.Sequence > 0 && p.Sequence == p.PreparedSequence && p.Generation != "" && p.LastError == ""
}

func (s *LocalStore) Preparation(ctx context.Context, workspaceID string) (Preparation, error) {
	var result Preparation
	if !ValidID(workspaceID) {
		return result, ErrInvalid
	}
	var at sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT sequence,prepared_sequence,generation,checkpoint_at,last_error,
		EXISTS(SELECT 1 FROM workspaces w JOIN continuity_attachments a ON a.workspace_id=w.id WHERE w.id=d.workspace_id
		AND w.owner_user_id='local' AND w.deleted_at IS NULL AND a.state IN ('native','imported_inactive','imported_active')
		AND NOT EXISTS(SELECT 1 FROM continuity_file_mutations p WHERE p.workspace_id=w.id AND p.target='native-create')),
		EXISTS(SELECT 1 FROM continuity_file_mutations m WHERE m.workspace_id=d.workspace_id),
		EXISTS(SELECT 1 FROM continuity_file_mutations m WHERE m.workspace_id=d.workspace_id AND m.state='failed')
		FROM continuity_dirty d WHERE workspace_id=?`, workspaceID).Scan(
		&result.Sequence, &result.PreparedSequence, &result.Generation, &at, &result.LastError,
		&result.Admitted, &result.PendingMutation, &result.MutationFailed)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if at.Valid {
		result.CheckpointAt = &at.Time
	}
	return result, err
}

// ReadSnapshot holds the canonical shared DB's single read view only during
// enumeration. Collect must use query (never the pool: it has one connection)
// and spool bounded chunks. The transaction closes BEFORE publication/fencing.
func (s *LocalStore) ReadSnapshot(ctx context.Context, workspaceID string, collect func(Queryer, uint64) error) (uint64, error) {
	if !ValidID(workspaceID) || collect == nil {
		return 0, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	attachment, err := readAttachment(ctx, tx, workspaceID)
	if err != nil {
		return 0, err
	}
	if !attachment.AllowsPreparation() {
		return 0, ErrConflict
	}
	var sequence uint64
	var blocked, owned bool
	if err := tx.QueryRowContext(ctx, `SELECT sequence,
		EXISTS(SELECT 1 FROM continuity_file_mutations m WHERE m.workspace_id=d.workspace_id),
		EXISTS(SELECT 1 FROM workspaces w WHERE w.id=d.workspace_id AND w.owner_user_id='local' AND w.deleted_at IS NULL)
		FROM continuity_dirty d WHERE workspace_id=?`, workspaceID).Scan(&sequence, &blocked, &owned); err != nil {
		return 0, err
	}
	if !owned {
		return 0, ErrConflict
	}
	if blocked {
		return 0, ErrMutationPending
	}
	if err := collect(tx, sequence); err != nil {
		return 0, err
	}
	return sequence, tx.Rollback()
}

func (s *LocalStore) SnapshotFence(workspaceID string, sequence uint64) Fence {
	return func(ctx context.Context) error {
		if !ValidID(workspaceID) || sequence == 0 || sequence > 1<<63-1 {
			return ErrInvalid
		}
		var current uint64
		var state AttachmentState
		var blocked bool
		err := s.db.QueryRowContext(ctx, `SELECT d.sequence,a.state,
			EXISTS(SELECT 1 FROM continuity_file_mutations m WHERE m.workspace_id=d.workspace_id)
			FROM continuity_dirty d JOIN continuity_attachments a ON a.workspace_id=d.workspace_id
			JOIN workspaces w ON w.id=d.workspace_id WHERE d.workspace_id=? AND w.owner_user_id='local' AND w.deleted_at IS NULL`, workspaceID).Scan(&current, &state, &blocked)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if !(Attachment{State: state}).AllowsPreparation() {
			return ErrConflict
		}
		if blocked {
			return ErrMutationPending
		}
		if sequence != current {
			return ErrChanged
		}
		return nil
	}
}

func (s *LocalStore) Acknowledge(ctx context.Context, workspaceID string, sequence uint64, generation string, at time.Time) error {
	if !ValidID(workspaceID) || sequence == 0 || sequence > 1<<63-1 || !validGeneration(generation) || at.IsZero() {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE continuity_dirty SET prepared_sequence=?,generation=?,checkpoint_at=?,last_error=''
		WHERE workspace_id=? AND sequence=?
		AND NOT EXISTS(SELECT 1 FROM continuity_file_mutations m WHERE m.workspace_id=continuity_dirty.workspace_id)
		AND EXISTS(SELECT 1 FROM workspaces w JOIN continuity_attachments a ON a.workspace_id=w.id
		WHERE w.id=continuity_dirty.workspace_id AND w.owner_user_id='local' AND w.deleted_at IS NULL
		AND a.state IN ('native','imported_inactive','imported_active'))`, sequence, generation, at.UTC(), workspaceID, sequence)
	if err != nil {
		return err
	}
	return requireOne(result)
}

func (s *LocalStore) RecordPreparationFailure(ctx context.Context, workspaceID string, sequence uint64, reason string) error {
	if !ValidID(workspaceID) || sequence == 0 || sequence > 1<<63-1 || !validLabel(reason) {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE continuity_dirty SET last_error=? WHERE workspace_id=? AND sequence=?`, reason, workspaceID, sequence)
	if err != nil {
		return err
	}
	return requireOne(result)
}
