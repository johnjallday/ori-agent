package assistantsetup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

// Store is the coordinator's bounded persistence boundary. Consequential
// domain writes are deliberately absent.
type Store interface {
	FindActiveRun(ctx context.Context, ownerUserID, capabilityID string) (*Run, error)
	GetRun(ctx context.Context, ownerUserID, runID string) (*Run, error)
	ListOperations(ctx context.Context, ownerUserID, runID string) ([]Operation, error)
	Accept(ctx context.Context, acceptance Acceptance) (*Run, *Operation, bool, error)
	CompleteWorkspace(ctx context.Context, ownerUserID, runID, operationID string, result WorkspaceResult) (*Run, error)
	MarkWorkspaceUnresolved(ctx context.Context, ownerUserID, runID, operationID, safeCode string) (*Run, error)
	DeferRun(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Run, error)
	ResumeRun(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Run, error)
}

type SQLiteStore struct {
	db  *database.DB
	now func() time.Time
}

func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db, now: time.Now}
}

func (s *SQLiteStore) configured() error {
	if s == nil || s.db == nil {
		return ErrUnavailable
	}
	return nil
}

const runColumns = `id, owner_user_id, assistant_id, capability_id, status, current_step,
	revision, proposal_revision, blueprint_id, blueprint_version, blueprint_digest,
	team_plan_revision, team_role_id, team_role_name, team_role_action,
	team_provider, team_model, team_model_configured, team_config_digest, team_warning,
	target_mode, target_workspace_id, last_error_code, failed_step, deferred_at,
	retry_after, reconcile_after, created_at, updated_at`

const operationColumns = `id, run_id, owner_user_id, kind, status, attempt_count,
	idempotency_digest, review_digest, expected_run_revision, workspace_id,
	profile_provenance_id, safe_outcome_code, safe_error_code, created_at, started_at, completed_at, updated_at`

func (s *SQLiteStore) FindActiveRun(ctx context.Context, ownerUserID, capabilityID string) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM assistant_setup_runs
		WHERE owner_user_id = ? AND capability_id = ?
		  AND status IN ('active','deferred','reconcile_required')
		ORDER BY created_at DESC LIMIT 1`, strings.TrimSpace(ownerUserID), strings.TrimSpace(capabilityID))
	return scanRun(row)
}

func (s *SQLiteStore) GetRun(ctx context.Context, ownerUserID, runID string) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM assistant_setup_runs
		WHERE owner_user_id = ? AND id = ?`, strings.TrimSpace(ownerUserID), strings.TrimSpace(runID))
	return scanRun(row)
}

func (s *SQLiteStore) ListOperations(ctx context.Context, ownerUserID, runID string) ([]Operation, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+operationColumns+` FROM assistant_setup_operations
		WHERE owner_user_id = ? AND run_id = ? ORDER BY created_at, id`, strings.TrimSpace(ownerUserID), strings.TrimSpace(runID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	operations := make([]Operation, 0)
	for rows.Next() {
		op, scanErr := scanOperation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		operations = append(operations, *op)
	}
	return operations, rows.Err()
}

// Accept creates the accepted run and its preallocated workspace operation in
// one transaction. A replay of the exact accepted proposal returns the same
// claim; a different proposal cannot replace active work.
func (s *SQLiteStore) Accept(ctx context.Context, acceptance Acceptance) (*Run, *Operation, bool, error) {
	if err := s.configured(); err != nil {
		return nil, nil, false, err
	}
	if err := validateAcceptance(acceptance); err != nil {
		return nil, nil, false, err
	}
	now := s.now().UTC()
	runID := uuid.NewString()
	operationID := strings.TrimSpace(acceptance.WorkspaceOperationID)
	if operationID == "" {
		operationID = uuid.NewString()
	}
	var run *Run
	var operation *Operation
	created := false
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		existing, getErr := findActiveRunWith(ctx, tx, acceptance.OwnerUserID, CapabilityID)
		if getErr == nil {
			if !acceptanceMatchesRun(acceptance, existing) {
				return ErrConflict
			}
			existingOperation, opErr := getOperationWith(ctx, tx, acceptance.OwnerUserID, existing.ID, OperationWorkspace)
			if opErr != nil {
				return opErr
			}
			run, operation = existing, existingOperation
			return nil
		}
		if !errors.Is(getErr, ErrNotFound) {
			return getErr
		}

		_, insertErr := tx.ExecContext(ctx, `INSERT INTO assistant_setup_runs(
			id, owner_user_id, assistant_id, capability_id, status, current_step,
			revision, proposal_revision, blueprint_id, blueprint_version, blueprint_digest,
			team_plan_revision, team_role_id, team_role_name, team_role_action,
			team_provider, team_model, team_model_configured, team_config_digest, team_warning,
			target_mode, target_workspace_id, created_at, updated_at
		) VALUES(?, ?, ?, ?, 'active', 'workspace', 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, acceptance.OwnerUserID, acceptance.AssistantID, CapabilityID,
			acceptance.ProposalRevision, acceptance.BlueprintID, acceptance.BlueprintVersion,
			acceptance.BlueprintDigest, acceptance.TeamPlanRevision,
			acceptance.TeamRole.RoleID, acceptance.TeamRole.Name, acceptance.TeamRole.Action,
			acceptance.TeamRole.Provider, acceptance.TeamRole.Model, acceptance.TeamRole.ModelConfigured,
			acceptance.TeamRole.ConfigDigest, acceptance.TeamRole.Warning,
			acceptance.TargetMode, acceptance.TargetWorkspaceID, now, now)
		if insertErr != nil {
			if strings.Contains(insertErr.Error(), "UNIQUE constraint") {
				return ErrConflict
			}
			return insertErr
		}
		_, insertErr = tx.ExecContext(ctx, `INSERT INTO assistant_setup_operations(
			id, run_id, owner_user_id, kind, status, attempt_count, idempotency_digest,
			review_digest, expected_run_revision, workspace_id, profile_provenance_id, created_at, updated_at
		) VALUES(?, ?, ?, 'workspace', 'claimed', 0, ?, ?, 1, ?, ?, ?, ?)`,
			operationID, runID, acceptance.OwnerUserID, acceptance.ProposalRevision,
			acceptance.ReviewDigest, acceptance.TargetWorkspaceID, acceptance.ProfileProvenanceID, now, now)
		if insertErr != nil {
			return insertErr
		}
		run, getErr = getRunWith(ctx, tx, acceptance.OwnerUserID, runID)
		if getErr != nil {
			return getErr
		}
		operation, getErr = getOperationWith(ctx, tx, acceptance.OwnerUserID, runID, OperationWorkspace)
		created = true
		return getErr
	})
	if err != nil {
		return nil, nil, false, err
	}
	return run, operation, created, nil
}

func validateAcceptance(value Acceptance) error {
	if strings.TrimSpace(value.OwnerUserID) == "" || strings.TrimSpace(value.AssistantID) == "" ||
		strings.TrimSpace(value.ProposalRevision) == "" || strings.TrimSpace(value.ReviewDigest) == "" ||
		strings.TrimSpace(value.TargetWorkspaceID) == "" {
		return ErrInvalid
	}
	if value.TargetMode != TargetCreate && value.TargetMode != TargetAdopt {
		return ErrInvalid
	}
	if value.TargetMode == TargetCreate {
		if value.BlueprintID != BlueprintID || value.BlueprintVersion < 1 ||
			strings.TrimSpace(value.BlueprintDigest) == "" || strings.TrimSpace(value.TeamPlanRevision) == "" ||
			strings.TrimSpace(value.TeamRole.RoleID) == "" || strings.TrimSpace(value.TeamRole.ConfigDigest) == "" {
			return ErrInvalid
		}
		if value.TeamRole.Action != "create" && value.TeamRole.Action != "reuse" {
			return ErrInvalid
		}
	}
	return nil
}

func acceptanceMatchesRun(value Acceptance, run *Run) bool {
	return run != nil && run.AssistantID == value.AssistantID &&
		run.ProposalRevision == value.ProposalRevision && run.TargetMode == value.TargetMode &&
		run.TargetWorkspaceID == value.TargetWorkspaceID && run.BlueprintID == value.BlueprintID &&
		run.BlueprintVersion == value.BlueprintVersion && run.BlueprintDigest == value.BlueprintDigest &&
		run.TeamPlanRevision == value.TeamPlanRevision && run.TeamRole.RoleID == value.TeamRole.RoleID &&
		run.TeamRole.Name == value.TeamRole.Name && run.TeamRole.Action == value.TeamRole.Action &&
		run.TeamRole.ConfigDigest == value.TeamRole.ConfigDigest
}

func (s *SQLiteStore) CompleteWorkspace(ctx context.Context, ownerUserID, runID, operationID string, result WorkspaceResult) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.WorkspaceID) == "" || strings.TrimSpace(result.AgentInstanceID) == "" {
		return nil, ErrInvalid
	}
	now := s.now().UTC()
	var updated *Run
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		run, err := getRunWith(ctx, tx, ownerUserID, runID)
		if err != nil {
			return err
		}
		op, err := getOperationWith(ctx, tx, ownerUserID, runID, OperationWorkspace)
		if err != nil || op.ID != strings.TrimSpace(operationID) {
			if err != nil {
				return err
			}
			return ErrConflict
		}
		if op.Status == OperationSucceeded {
			updated = run
			return nil
		}
		if run.Status != RunActive || run.CurrentStep != StepWorkspace || run.TargetWorkspaceID != result.WorkspaceID {
			return ErrConflict
		}
		ownership := OwnershipCreated
		if run.TargetMode == TargetAdopt {
			ownership = OwnershipAdopted
		}
		resources := []Resource{
			{OperationID: op.ID, RunID: run.ID, WorkspaceID: result.WorkspaceID, Kind: ResourceWorkspace, ResourceID: result.WorkspaceID, Ownership: ownership, VersionDigest: run.BlueprintDigest, CreatedAt: now},
			{OperationID: op.ID, RunID: run.ID, WorkspaceID: result.WorkspaceID, Kind: ResourceAgentInstance, ResourceID: result.AgentInstanceID, Ownership: map[bool]ResourceOwnership{true: OwnershipCreated, false: OwnershipAdopted}[result.ProfileCreated], VersionDigest: result.ConfigurationDigest, CreatedAt: now},
		}
		if result.ProfileCreated {
			if strings.TrimSpace(result.ProfileProvenanceID) == "" || strings.TrimSpace(result.ProfileStoreOrigin) == "" {
				return ErrReconcileRequired
			}
			resources = append(resources, Resource{OperationID: op.ID, RunID: run.ID, WorkspaceID: result.WorkspaceID, Kind: ResourceAgentProfile, ResourceID: result.ProfileProvenanceID, Ownership: OwnershipCreated, StoreOrigin: result.ProfileStoreOrigin, VersionDigest: result.ConfigurationDigest, CreatedAt: now})
		}
		for _, resource := range resources {
			if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_setup_resources(
				operation_id, run_id, workspace_id, resource_kind, resource_id, ownership,
				store_origin, version_digest, created_at
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(operation_id, resource_kind, resource_id) DO NOTHING`,
				resource.OperationID, resource.RunID, resource.WorkspaceID, resource.Kind,
				resource.ResourceID, resource.Ownership, resource.StoreOrigin, resource.VersionDigest, resource.CreatedAt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations
			SET status = 'succeeded', attempt_count = CASE WHEN attempt_count < 1 THEN 1 ELSE attempt_count END,
				safe_outcome_code = 'workspace_ready', safe_error_code = '', completed_at = ?, updated_at = ?
			WHERE id = ? AND run_id = ? AND owner_user_id = ? AND status IN ('claimed','running','failed')`,
			now, now, op.ID, run.ID, ownerUserID); err != nil {
			return err
		}
		resultExec, err := tx.ExecContext(ctx, `UPDATE assistant_setup_runs
			SET current_step = 'folder', revision = revision + 1, last_error_code = '', failed_step = '', updated_at = ?
			WHERE id = ? AND owner_user_id = ? AND revision = ? AND current_step = 'workspace' AND status = 'active'`,
			now, run.ID, ownerUserID, run.Revision)
		if err != nil {
			return err
		}
		rows, err := resultExec.RowsAffected()
		if err != nil || rows != 1 {
			if err != nil {
				return err
			}
			return ErrStaleRun
		}
		updated, err = getRunWith(ctx, tx, ownerUserID, run.ID)
		return err
	})
	return updated, err
}

func (s *SQLiteStore) DeferRun(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Run, error) {
	return s.updateRunLifecycle(ctx, ownerUserID, runID, ifVersion, RunActive, RunDeferred)
}

func (s *SQLiteStore) ResumeRun(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Run, error) {
	return s.updateRunLifecycle(ctx, ownerUserID, runID, ifVersion, RunDeferred, RunActive)
}

func (s *SQLiteStore) updateRunLifecycle(ctx context.Context, ownerUserID, runID string, ifVersion int64, from, to RunStatus) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if ifVersion < 1 {
		return nil, ErrInvalid
	}
	now := s.now().UTC()
	deferred := any(nil)
	if to == RunDeferred {
		deferred = now
	}
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_setup_runs
		SET status=?, revision=revision+1, deferred_at=?, updated_at=?
		WHERE id=? AND owner_user_id=? AND revision=? AND status=?`,
		to, deferred, now, strings.TrimSpace(runID), strings.TrimSpace(ownerUserID), ifVersion, from)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows != 1 {
		if _, getErr := s.GetRun(ctx, ownerUserID, runID); errors.Is(getErr, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrStaleRun
	}
	return s.GetRun(ctx, ownerUserID, runID)
}

func (s *SQLiteStore) MarkWorkspaceUnresolved(ctx context.Context, ownerUserID, runID, operationID, safeCode string) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	safeCode = strings.TrimSpace(safeCode)
	if safeCode == "" {
		safeCode = "workspace_outcome_unresolved"
	}
	now := s.now().UTC()
	var updated *Run
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		run, err := getRunWith(ctx, tx, ownerUserID, runID)
		if err != nil {
			return err
		}
		op, err := getOperationWith(ctx, tx, ownerUserID, runID, OperationWorkspace)
		if err != nil || op.ID != operationID {
			if err != nil {
				return err
			}
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations SET status='unresolved',
			attempt_count = CASE WHEN attempt_count < 1 THEN 1 ELSE attempt_count END,
			safe_error_code=?, updated_at=? WHERE id=? AND owner_user_id=?`, safeCode, now, op.ID, ownerUserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_runs SET status='reconcile_required',
			last_error_code=?, failed_step='workspace', reconcile_after=?, revision=revision+1, updated_at=?
			WHERE id=? AND owner_user_id=?`, safeCode, now, now, run.ID, ownerUserID); err != nil {
			return err
		}
		updated, err = getRunWith(ctx, tx, ownerUserID, run.ID)
		return err
	})
	return updated, err
}

type scanner interface{ Scan(...any) error }

func scanRun(row interface{ Scan(...any) error }) (*Run, error) {
	var run Run
	var modelConfigured int
	var lastError, failedStep sql.NullString
	var deferredAt, retryAfter, reconcileAfter sql.NullTime
	err := row.Scan(&run.ID, &run.OwnerUserID, &run.AssistantID, &run.CapabilityID, &run.Status, &run.CurrentStep,
		&run.Revision, &run.ProposalRevision, &run.BlueprintID, &run.BlueprintVersion, &run.BlueprintDigest,
		&run.TeamPlanRevision, &run.TeamRole.RoleID, &run.TeamRole.Name, &run.TeamRole.Action,
		&run.TeamRole.Provider, &run.TeamRole.Model, &modelConfigured, &run.TeamRole.ConfigDigest, &run.TeamRole.Warning,
		&run.TargetMode, &run.TargetWorkspaceID, &lastError, &failedStep, &deferredAt,
		&retryAfter, &reconcileAfter, &run.CreatedAt, &run.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	run.TeamRole.ModelConfigured = modelConfigured != 0
	run.LastErrorCode = lastError.String
	run.FailedStep = Step(failedStep.String)
	if deferredAt.Valid {
		run.DeferredAt = &deferredAt.Time
	}
	if retryAfter.Valid {
		run.RetryAfter = &retryAfter.Time
	}
	if reconcileAfter.Valid {
		run.ReconcileAfter = &reconcileAfter.Time
	}
	return &run, nil
}

func scanOperation(row interface{ Scan(...any) error }) (*Operation, error) {
	var operation Operation
	var startedAt, completedAt sql.NullTime
	err := row.Scan(&operation.ID, &operation.RunID, &operation.OwnerUserID, &operation.Kind, &operation.Status,
		&operation.AttemptCount, &operation.IdempotencyDigest, &operation.ReviewDigest,
		&operation.ExpectedRunRevision, &operation.WorkspaceID, &operation.ProfileProvenanceID, &operation.SafeOutcomeCode,
		&operation.SafeErrorCode, &operation.CreatedAt, &startedAt, &completedAt, &operation.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if startedAt.Valid {
		operation.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		operation.CompletedAt = &completedAt.Time
	}
	return &operation, nil
}

func findActiveRunWith(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, owner, capability string) (*Run, error) {
	return scanRun(q.QueryRowContext(ctx, `SELECT `+runColumns+` FROM assistant_setup_runs
		WHERE owner_user_id=? AND capability_id=? AND status IN ('active','deferred','reconcile_required')
		ORDER BY created_at DESC LIMIT 1`, owner, capability))
}

func getRunWith(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, owner, runID string) (*Run, error) {
	return scanRun(q.QueryRowContext(ctx, `SELECT `+runColumns+` FROM assistant_setup_runs WHERE owner_user_id=? AND id=?`, owner, runID))
}

func getOperationWith(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, owner, runID string, kind OperationKind) (*Operation, error) {
	return scanOperation(q.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM assistant_setup_operations
		WHERE owner_user_id=? AND run_id=? AND kind=?`, owner, runID, kind))
}

var _ Store = (*SQLiteStore)(nil)
var _ = fmt.Sprintf
