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
	ListResources(ctx context.Context, ownerUserID, runID string) ([]Resource, error)
	StartWorkspace(ctx context.Context, ownerUserID, runID, operationID string) error
	Accept(ctx context.Context, acceptance Acceptance) (*Run, *Operation, bool, error)
	CompleteWorkspace(ctx context.Context, ownerUserID, runID, operationID string, result WorkspaceResult) (*Run, error)
	ClaimFolderIntent(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Run, *Operation, error)
	StartFolderGrant(ctx context.Context, authorization FolderGrantAuthorization) (*Run, *Operation, error)
	CompleteFolderGrant(ctx context.Context, authorization FolderGrantAuthorization, result FolderGrantResult) (*Run, error)
	AdoptCurrentFolder(ctx context.Context, ownerUserID, runID string, ifVersion int64, result FolderGrantResult) (*Run, error)
	ReconcileAdoptedFolder(ctx context.Context, ownerUserID, runID string, ifVersion int64, result FolderGrantResult) (*Run, bool, error)
	RecordFolderGrantFailure(ctx context.Context, authorization FolderGrantAuthorization, safeCode string) (*Run, error)
	ClaimPrepareReview(ctx context.Context, ownerUserID, runID string, ifVersion int64, reviewDigest string) (*Run, *Operation, *Operation, error)
	CompletePrepareReview(ctx context.Context, request PrepareReviewRequest, result PrepareReviewResult) (*Run, error)
	RecordPrepareReviewFailure(ctx context.Context, request PrepareReviewRequest, result PrepareReviewResult, safeCode string) (*Run, error)
	MarkWorkspaceUnresolved(ctx context.Context, ownerUserID, runID, operationID, safeCode string) (*Run, error)
	DeferRun(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Run, error)
	ResumeRun(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Run, error)
	InvalidateRun(ctx context.Context, ownerUserID, runID string, ifVersion int64, safeCode string) (*Run, error)
	ListDueRetries(ctx context.Context, now time.Time, limit int) ([]Run, error)
	ClearRetry(ctx context.Context, ownerUserID, runID string, ifVersion int64, safeCode string) error
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
		  AND status IN ('active','deferred','reconcile_required','first_result','invalidated')
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

// ListResources returns the durable receipts of one run, owner-scoped and in a
// stable order. It is read-only; receipts are written only by the operations
// that own them.
func (s *SQLiteStore) ListResources(ctx context.Context, ownerUserID, runID string) ([]Resource, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.operation_id, r.run_id, r.workspace_id, r.resource_kind,
			r.resource_id, r.ownership, r.store_origin, r.version_digest, r.created_at
		FROM assistant_setup_resources r
		JOIN assistant_setup_runs run ON run.id = r.run_id
		WHERE run.owner_user_id = ? AND r.run_id = ?
		ORDER BY r.created_at, r.resource_kind, r.resource_id`, strings.TrimSpace(ownerUserID), strings.TrimSpace(runID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	resources := make([]Resource, 0)
	for rows.Next() {
		var resource Resource
		if scanErr := rows.Scan(&resource.OperationID, &resource.RunID, &resource.WorkspaceID, &resource.Kind,
			&resource.ResourceID, &resource.Ownership, &resource.StoreOrigin, &resource.VersionDigest, &resource.CreatedAt); scanErr != nil {
			return nil, scanErr
		}
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

// StartWorkspace records that preparation is being attempted: a claimed or
// previously failed workspace operation becomes running, counts the attempt,
// and keeps its first start time. An operation that is already running,
// unresolved, or succeeded is left untouched, so replay is idempotent.
func (s *SQLiteStore) StartWorkspace(ctx context.Context, ownerUserID, runID, operationID string) error {
	if err := s.configured(); err != nil {
		return err
	}
	now := s.now().UTC()
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		op, err := getOperationWith(ctx, tx, strings.TrimSpace(ownerUserID), strings.TrimSpace(runID), OperationWorkspace)
		if err != nil {
			return err
		}
		if op.ID != strings.TrimSpace(operationID) {
			return ErrConflict
		}
		if op.Status != OperationClaimed && op.Status != OperationFailed {
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE assistant_setup_operations
			SET status = 'running', attempt_count = attempt_count + 1,
				started_at = COALESCE(started_at, ?), updated_at = ?
			WHERE id = ? AND owner_user_id = ? AND status IN ('claimed','failed')`,
			now, now, op.ID, op.OwnerUserID)
		return err
	})
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

func (s *SQLiteStore) ClaimFolderIntent(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Run, *Operation, error) {
	if err := s.configured(); err != nil {
		return nil, nil, err
	}
	if ifVersion < 1 {
		return nil, nil, ErrInvalid
	}
	now := s.now().UTC()
	var run *Run
	var operation *Operation
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		current, err := getRunWith(ctx, tx, ownerUserID, runID)
		if err != nil {
			return err
		}
		if current.Status != RunActive || current.CurrentStep != StepFolder {
			return ErrInvalidAction
		}
		if current.Revision != ifVersion {
			return ErrStaleRun
		}
		existing, err := getOperationWith(ctx, tx, ownerUserID, runID, OperationFolderGrant)
		if err == nil {
			if existing.WorkspaceID != current.TargetWorkspaceID || existing.ExpectedRunRevision != current.Revision {
				return ErrConflict
			}
			run, operation = current, existing
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		operationID := uuid.NewString()
		idempotencyDigest := current.ID + ":folder:" + current.TargetWorkspaceID
		if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_setup_operations(
			id, run_id, owner_user_id, kind, status, attempt_count, idempotency_digest,
			review_digest, expected_run_revision, workspace_id, created_at, updated_at
		) VALUES(?, ?, ?, 'folder_grant', 'awaiting_user', 0, ?, ?, ?, ?, ?, ?)`,
			operationID, current.ID, current.OwnerUserID, idempotencyDigest,
			current.ProposalRevision, current.Revision, current.TargetWorkspaceID, now, now); err != nil {
			return err
		}
		operation, err = getOperationWith(ctx, tx, ownerUserID, runID, OperationFolderGrant)
		run = current
		return err
	})
	return run, operation, err
}

func (s *SQLiteStore) StartFolderGrant(ctx context.Context, authorization FolderGrantAuthorization) (*Run, *Operation, error) {
	if err := s.configured(); err != nil {
		return nil, nil, err
	}
	now := s.now().UTC()
	var run *Run
	var operation *Operation
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		current, err := getRunWith(ctx, tx, authorization.OwnerUserID, authorization.RunID)
		if err != nil {
			return err
		}
		op, err := getOperationWith(ctx, tx, authorization.OwnerUserID, current.ID, OperationFolderGrant)
		if err != nil {
			return err
		}
		if op.ID != authorization.OperationID || op.ExpectedRunRevision != authorization.RunRevision ||
			op.WorkspaceID != authorization.WorkspaceID || current.TargetWorkspaceID != authorization.WorkspaceID {
			return ErrConflict
		}
		if op.Status == OperationSucceeded {
			run, operation = current, op
			return nil
		}
		if current.Status != RunActive || current.CurrentStep != StepFolder || current.Revision != authorization.RunRevision {
			return ErrStaleRun
		}
		result, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations
			SET status='running', attempt_count=attempt_count+1,
				started_at=COALESCE(started_at, ?), safe_error_code='', updated_at=?
			WHERE id=? AND run_id=? AND owner_user_id=? AND status IN ('awaiting_user','running','failed')`,
			now, now, op.ID, current.ID, authorization.OwnerUserID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			if err != nil {
				return err
			}
			return ErrConflict
		}
		operation, err = getOperationWith(ctx, tx, authorization.OwnerUserID, current.ID, OperationFolderGrant)
		run = current
		return err
	})
	return run, operation, err
}

func (s *SQLiteStore) CompleteFolderGrant(ctx context.Context, authorization FolderGrantAuthorization, result FolderGrantResult) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.RootGenerationID) == "" || strings.TrimSpace(result.DirectoryReference) == "" {
		return nil, ErrInvalid
	}
	now := s.now().UTC()
	var updated *Run
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		run, err := getRunWith(ctx, tx, authorization.OwnerUserID, authorization.RunID)
		if err != nil {
			return err
		}
		op, err := getOperationWith(ctx, tx, authorization.OwnerUserID, run.ID, OperationFolderGrant)
		if err != nil || op.ID != authorization.OperationID || op.WorkspaceID != authorization.WorkspaceID {
			if err != nil {
				return err
			}
			return ErrConflict
		}
		if op.Status == OperationSucceeded {
			updated = run
			return nil
		}
		if run.Status != RunActive || run.CurrentStep != StepFolder || run.Revision != authorization.RunRevision ||
			run.TargetWorkspaceID != authorization.WorkspaceID {
			return ErrStaleRun
		}
		ownership := OwnershipUpdated
		outcome := "folder_granted"
		if result.Adopted {
			ownership = OwnershipAdopted
			outcome = "folder_adopted"
		}
		resources := []Resource{
			{OperationID: op.ID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID, Kind: ResourceDirectory, ResourceID: result.DirectoryReference, Ownership: ownership, VersionDigest: result.RootGenerationID, CreatedAt: now},
			{OperationID: op.ID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID, Kind: ResourceRoot, ResourceID: result.RootGenerationID, Ownership: ownership, VersionDigest: result.DirectoryReference, CreatedAt: now},
		}
		for _, resource := range resources {
			if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_setup_resources(
				operation_id, run_id, workspace_id, resource_kind, resource_id, ownership,
				store_origin, version_digest, created_at
			) VALUES(?, ?, ?, ?, ?, ?, '', ?, ?)
			ON CONFLICT(operation_id, resource_kind, resource_id) DO NOTHING`,
				resource.OperationID, resource.RunID, resource.WorkspaceID, resource.Kind,
				resource.ResourceID, resource.Ownership, resource.VersionDigest, resource.CreatedAt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations
			SET status='succeeded', safe_outcome_code=?, safe_error_code='', completed_at=?, updated_at=?
			WHERE id=? AND owner_user_id=? AND status IN ('awaiting_user','running','failed')`,
			outcome, now, now, op.ID, authorization.OwnerUserID); err != nil {
			return err
		}
		execResult, err := tx.ExecContext(ctx, `UPDATE assistant_setup_runs
			SET current_step='monitoring', revision=revision+1, last_error_code='', failed_step='', updated_at=?
			WHERE id=? AND owner_user_id=? AND revision=? AND current_step='folder' AND status='active'`,
			now, run.ID, authorization.OwnerUserID, run.Revision)
		if err != nil {
			return err
		}
		rows, err := execResult.RowsAffected()
		if err != nil || rows != 1 {
			if err != nil {
				return err
			}
			return ErrStaleRun
		}
		updated, err = getRunWith(ctx, tx, authorization.OwnerUserID, run.ID)
		return err
	})
	return updated, err
}

// AdoptCurrentFolder reconciles a manual or lost-response grant while a run is
// still at its folder checkpoint. The domain grant is already canonical; this
// transaction records adoption without invoking the picker or filesystem.
func (s *SQLiteStore) AdoptCurrentFolder(ctx context.Context, ownerUserID, runID string, ifVersion int64, result FolderGrantResult) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.RootGenerationID) == "" || strings.TrimSpace(result.DirectoryReference) == "" {
		return nil, ErrInvalid
	}
	now := s.now().UTC()
	var updated *Run
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		run, err := getRunWith(ctx, tx, ownerUserID, runID)
		if err != nil {
			return err
		}
		if run.Status != RunActive || run.CurrentStep != StepFolder || run.Revision != ifVersion {
			return ErrStaleRun
		}
		op, err := getOperationWith(ctx, tx, ownerUserID, run.ID, OperationFolderGrant)
		if errors.Is(err, ErrNotFound) {
			_, err = tx.ExecContext(ctx, `INSERT INTO assistant_setup_operations(
				id, run_id, owner_user_id, kind, status, attempt_count, idempotency_digest,
				review_digest, expected_run_revision, workspace_id, created_at, updated_at
			) VALUES(?, ?, ?, 'folder_grant', 'awaiting_user', 0, ?, ?, ?, ?, ?, ?)`,
				uuid.NewString(), run.ID, run.OwnerUserID, run.ID+":folder:"+run.TargetWorkspaceID,
				run.ProposalRevision, run.Revision, run.TargetWorkspaceID, now, now)
			if err != nil {
				return err
			}
			op, err = getOperationWith(ctx, tx, ownerUserID, run.ID, OperationFolderGrant)
		}
		if err != nil {
			return err
		}
		for _, resource := range []Resource{
			{OperationID: op.ID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID, Kind: ResourceDirectory, ResourceID: result.DirectoryReference, Ownership: OwnershipAdopted, VersionDigest: result.RootGenerationID, CreatedAt: now},
			{OperationID: op.ID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID, Kind: ResourceRoot, ResourceID: result.RootGenerationID, Ownership: OwnershipAdopted, VersionDigest: result.DirectoryReference, CreatedAt: now},
		} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_setup_resources(
				operation_id, run_id, workspace_id, resource_kind, resource_id, ownership,
				store_origin, version_digest, created_at
			) VALUES(?, ?, ?, ?, ?, ?, '', ?, ?)
			ON CONFLICT(operation_id, resource_kind, resource_id) DO NOTHING`,
				resource.OperationID, resource.RunID, resource.WorkspaceID, resource.Kind,
				resource.ResourceID, resource.Ownership, resource.VersionDigest, resource.CreatedAt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations SET status='succeeded',
			attempt_count=CASE WHEN attempt_count<1 THEN 1 ELSE attempt_count END,
			expected_run_revision=?, safe_outcome_code='folder_adopted', safe_error_code='',
			completed_at=COALESCE(completed_at, ?), updated_at=?
			WHERE id=? AND owner_user_id=?`, run.Revision, now, now, op.ID, ownerUserID); err != nil {
			return err
		}
		execResult, err := tx.ExecContext(ctx, `UPDATE assistant_setup_runs SET current_step='monitoring',
			revision=revision+1, last_error_code='', failed_step='', updated_at=?
			WHERE id=? AND owner_user_id=? AND revision=? AND status='active' AND current_step='folder'`,
			now, run.ID, ownerUserID, run.Revision)
		if err != nil {
			return err
		}
		rows, err := execResult.RowsAffected()
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

// ReconcileAdoptedFolder records a canonically verified manual re-link without
// claiming the assistant granted it. It advances no external consequence; it
// only moves the existing run back to a fresh monitoring review.
func (s *SQLiteStore) ReconcileAdoptedFolder(ctx context.Context, ownerUserID, runID string, ifVersion int64, result FolderGrantResult) (*Run, bool, error) {
	if err := s.configured(); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(result.RootGenerationID) == "" || strings.TrimSpace(result.DirectoryReference) == "" {
		return nil, false, ErrInvalid
	}
	now := s.now().UTC()
	var updated *Run
	changed := false
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		run, err := getRunWith(ctx, tx, ownerUserID, runID)
		if err != nil {
			return err
		}
		if run.Status != RunActive || run.CurrentStep != StepMonitoring || run.Revision != ifVersion {
			return ErrStaleRun
		}
		op, err := getOperationWith(ctx, tx, ownerUserID, run.ID, OperationFolderGrant)
		if err != nil || op.Status != OperationSucceeded {
			if err != nil {
				return err
			}
			return ErrConflict
		}
		var exists int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM assistant_setup_resources
			WHERE operation_id=? AND resource_kind=? AND resource_id=? LIMIT 1`,
			op.ID, ResourceRoot, result.RootGenerationID).Scan(&exists)
		if err == nil {
			updated = run
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		for _, resource := range []Resource{
			{OperationID: op.ID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID, Kind: ResourceDirectory, ResourceID: result.DirectoryReference, Ownership: OwnershipAdopted, VersionDigest: result.RootGenerationID, CreatedAt: now},
			{OperationID: op.ID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID, Kind: ResourceRoot, ResourceID: result.RootGenerationID, Ownership: OwnershipAdopted, VersionDigest: result.DirectoryReference, CreatedAt: now},
		} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_setup_resources(
				operation_id, run_id, workspace_id, resource_kind, resource_id, ownership,
				store_origin, version_digest, created_at
			) VALUES(?, ?, ?, ?, ?, ?, '', ?, ?)
			ON CONFLICT(operation_id, resource_kind, resource_id) DO NOTHING`,
				resource.OperationID, resource.RunID, resource.WorkspaceID, resource.Kind,
				resource.ResourceID, resource.Ownership, resource.VersionDigest, resource.CreatedAt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations
			SET safe_outcome_code='folder_adopted_after_manual_change', safe_error_code='', updated_at=?
			WHERE id=? AND owner_user_id=? AND status='succeeded'`, now, op.ID, ownerUserID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE assistant_setup_runs
			SET revision=revision+1, last_error_code='', failed_step='', updated_at=?
			WHERE id=? AND owner_user_id=? AND revision=? AND status='active' AND current_step='monitoring'`,
			now, run.ID, ownerUserID, run.Revision)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			if err != nil {
				return err
			}
			return ErrStaleRun
		}
		changed = true
		updated, err = getRunWith(ctx, tx, ownerUserID, run.ID)
		return err
	})
	return updated, changed, err
}

func (s *SQLiteStore) RecordFolderGrantFailure(ctx context.Context, authorization FolderGrantAuthorization, safeCode string) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	safeCode = strings.TrimSpace(safeCode)
	if safeCode == "" {
		safeCode = "folder_grant_failed"
	}
	now := s.now().UTC()
	var updated *Run
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		run, err := getRunWith(ctx, tx, authorization.OwnerUserID, authorization.RunID)
		if err != nil {
			return err
		}
		op, err := getOperationWith(ctx, tx, authorization.OwnerUserID, run.ID, OperationFolderGrant)
		if err != nil || op.ID != authorization.OperationID || op.WorkspaceID != authorization.WorkspaceID {
			if err != nil {
				return err
			}
			return ErrConflict
		}
		if op.Status == OperationSucceeded {
			updated = run
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations
			SET status='awaiting_user', safe_error_code=?, updated_at=? WHERE id=? AND owner_user_id=?`,
			safeCode, now, op.ID, authorization.OwnerUserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_runs
			SET last_error_code=?, failed_step='folder', updated_at=? WHERE id=? AND owner_user_id=?`,
			safeCode, now, run.ID, authorization.OwnerUserID); err != nil {
			return err
		}
		updated, err = getRunWith(ctx, tx, authorization.OwnerUserID, run.ID)
		return err
	})
	return updated, err
}

func (s *SQLiteStore) ClaimPrepareReview(ctx context.Context, ownerUserID, runID string, ifVersion int64, reviewDigest string) (*Run, *Operation, *Operation, error) {
	if err := s.configured(); err != nil {
		return nil, nil, nil, err
	}
	reviewDigest = strings.TrimSpace(reviewDigest)
	if ifVersion < 1 || reviewDigest == "" {
		return nil, nil, nil, ErrInvalid
	}
	now := s.now().UTC()
	var run *Run
	var monitoring, scan *Operation
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		current, err := getRunWith(ctx, tx, ownerUserID, runID)
		if err != nil {
			return err
		}
		if current.Status != RunActive || current.CurrentStep != StepMonitoring {
			return ErrInvalidAction
		}
		if current.Revision != ifVersion {
			return ErrStaleRun
		}
		monitoring, err = getOperationWith(ctx, tx, ownerUserID, runID, OperationMonitoring)
		if errors.Is(err, ErrNotFound) {
			_, err = tx.ExecContext(ctx, `INSERT INTO assistant_setup_operations(
				id, run_id, owner_user_id, kind, status, attempt_count, idempotency_digest,
				review_digest, expected_run_revision, workspace_id, created_at, updated_at
			) VALUES(?, ?, ?, 'monitoring', 'claimed', 0, ?, ?, ?, ?, ?, ?)`,
				uuid.NewString(), current.ID, current.OwnerUserID, current.ID+":monitoring:"+reviewDigest,
				reviewDigest, current.Revision, current.TargetWorkspaceID, now, now)
			if err != nil {
				return err
			}
			monitoring, err = getOperationWith(ctx, tx, ownerUserID, runID, OperationMonitoring)
		}
		if err != nil {
			return err
		}
		scan, err = getOperationWith(ctx, tx, ownerUserID, runID, OperationInitialScan)
		if errors.Is(err, ErrNotFound) {
			_, err = tx.ExecContext(ctx, `INSERT INTO assistant_setup_operations(
				id, run_id, owner_user_id, kind, status, attempt_count, idempotency_digest,
				review_digest, expected_run_revision, workspace_id, created_at, updated_at
			) VALUES(?, ?, ?, 'initial_scan', 'claimed', 0, ?, ?, ?, ?, ?, ?)`,
				uuid.NewString(), current.ID, current.OwnerUserID, current.ID+":initial_scan:"+reviewDigest,
				reviewDigest, current.Revision, current.TargetWorkspaceID, now, now)
			if err != nil {
				return err
			}
			scan, err = getOperationWith(ctx, tx, ownerUserID, runID, OperationInitialScan)
		}
		if err != nil {
			return err
		}
		for _, operation := range []*Operation{monitoring, scan} {
			if operation.WorkspaceID != current.TargetWorkspaceID {
				return ErrStaleRun
			}
			if operation.Status == OperationSucceeded {
				if operation.ReviewDigest != reviewDigest || operation.ExpectedRunRevision != current.Revision {
					return ErrStaleRun
				}
				continue
			}
			if operation.ReviewDigest != reviewDigest || operation.ExpectedRunRevision != current.Revision {
				if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations
					SET review_digest=?, idempotency_digest=?, expected_run_revision=?, status='claimed', safe_error_code='', updated_at=?
					WHERE id=? AND owner_user_id=? AND status IN ('claimed','failed')`,
					reviewDigest, current.ID+":"+string(operation.Kind)+":"+reviewDigest, current.Revision, now, operation.ID, ownerUserID); err != nil {
					return err
				}
				operation.ReviewDigest = reviewDigest
				operation.ExpectedRunRevision = current.Revision
				operation.Status = OperationClaimed
			}
		}
		run = current
		return nil
	})
	return run, monitoring, scan, err
}

func (s *SQLiteStore) CompletePrepareReview(ctx context.Context, request PrepareReviewRequest, result PrepareReviewResult) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if !result.MonitoringApplied || (result.ScanOutcome != "batch" && result.ScanOutcome != "no_eligible") ||
		(result.ScanOutcome == "batch" && strings.TrimSpace(result.BatchID) == "") || result.CompletedAt.IsZero() ||
		result.Facts.RootGenerationID != request.Review.RootGenerationID || result.Facts.PrivacyMode != request.Review.PrivacyMode {
		return nil, ErrInvalid
	}
	now := s.now().UTC()
	var updated *Run
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		run, err := getRunWith(ctx, tx, request.OwnerUserID, request.RunID)
		if err != nil {
			return err
		}
		monitoring, err := getOperationWith(ctx, tx, request.OwnerUserID, run.ID, OperationMonitoring)
		if err != nil {
			return err
		}
		scan, err := getOperationWith(ctx, tx, request.OwnerUserID, run.ID, OperationInitialScan)
		if err != nil {
			return err
		}
		if monitoring.ID != request.MonitoringOperation || scan.ID != request.ScanOperation ||
			run.TargetWorkspaceID != request.WorkspaceID || monitoring.ReviewDigest != request.Review.Revision || scan.ReviewDigest != request.Review.Revision {
			return ErrConflict
		}
		if monitoring.Status == OperationSucceeded && scan.Status == OperationSucceeded {
			updated = run
			return nil
		}
		if run.Status != RunActive || run.CurrentStep != StepMonitoring || run.Revision != request.RunRevision {
			return ErrStaleRun
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations SET status='succeeded',
			attempt_count=CASE WHEN attempt_count < 1 THEN 1 ELSE attempt_count END,
			safe_outcome_code='monitoring_active', safe_error_code='', completed_at=?, updated_at=?
			WHERE id=? AND owner_user_id=?`, now, now, monitoring.ID, request.OwnerUserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations SET status='succeeded',
			attempt_count=CASE WHEN attempt_count < 1 THEN 1 ELSE attempt_count END,
			safe_outcome_code=?, safe_error_code='', completed_at=?, updated_at=?
			WHERE id=? AND owner_user_id=?`, result.ScanOutcome, result.CompletedAt.UTC(), now, scan.ID, request.OwnerUserID); err != nil {
			return err
		}
		resources := []Resource{}
		if strings.TrimSpace(result.WatcherID) != "" {
			resources = append(resources, Resource{OperationID: monitoring.ID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID, Kind: ResourceWatcher, ResourceID: result.WatcherID, Ownership: OwnershipUpdated, VersionDigest: request.Review.Revision, CreatedAt: now})
		}
		if strings.TrimSpace(result.ScheduleID) != "" {
			resources = append(resources, Resource{OperationID: monitoring.ID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID, Kind: ResourceSchedule, ResourceID: result.ScheduleID, Ownership: OwnershipUpdated, VersionDigest: request.Review.Revision, CreatedAt: now})
		}
		scanKind, scanID := ResourceScanOutcome, scan.ID
		if result.ScanOutcome == "batch" {
			scanKind, scanID = ResourceScanBatch, result.BatchID
		}
		resources = append(resources, Resource{OperationID: scan.ID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID, Kind: scanKind, ResourceID: scanID, Ownership: OwnershipCreated, VersionDigest: result.Facts.RootGenerationID, CreatedAt: now})
		for _, resource := range resources {
			if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_setup_resources(
				operation_id, run_id, workspace_id, resource_kind, resource_id, ownership,
				store_origin, version_digest, created_at
			) VALUES(?, ?, ?, ?, ?, ?, '', ?, ?)
			ON CONFLICT(operation_id, resource_kind, resource_id) DO NOTHING`,
				resource.OperationID, resource.RunID, resource.WorkspaceID, resource.Kind,
				resource.ResourceID, resource.Ownership, resource.VersionDigest, resource.CreatedAt); err != nil {
				return err
			}
		}
		execResult, err := tx.ExecContext(ctx, `UPDATE assistant_setup_runs SET status='first_result',
			current_step='result', revision=revision+1, last_error_code='', failed_step='', retry_after=NULL, updated_at=?
			WHERE id=? AND owner_user_id=? AND revision=? AND current_step='monitoring' AND status='active'`,
			now, run.ID, request.OwnerUserID, run.Revision)
		if err != nil {
			return err
		}
		rows, err := execResult.RowsAffected()
		if err != nil || rows != 1 {
			if err != nil {
				return err
			}
			return ErrStaleRun
		}
		updated, err = getRunWith(ctx, tx, request.OwnerUserID, run.ID)
		return err
	})
	return updated, err
}

func (s *SQLiteStore) RecordPrepareReviewFailure(ctx context.Context, request PrepareReviewRequest, result PrepareReviewResult, safeCode string) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	safeCode = strings.TrimSpace(safeCode)
	if safeCode == "" {
		safeCode = "prepare_review_failed"
	}
	now := s.now().UTC()
	var updated *Run
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		run, err := getRunWith(ctx, tx, request.OwnerUserID, request.RunID)
		if err != nil {
			return err
		}
		monitoring, err := getOperationWith(ctx, tx, request.OwnerUserID, run.ID, OperationMonitoring)
		if err != nil {
			return err
		}
		scan, err := getOperationWith(ctx, tx, request.OwnerUserID, run.ID, OperationInitialScan)
		if err != nil {
			return err
		}
		if monitoring.ID != request.MonitoringOperation || scan.ID != request.ScanOperation {
			return ErrConflict
		}
		if run.Status == RunFirstResult && monitoring.Status == OperationSucceeded && scan.Status == OperationSucceeded {
			updated = run
			return nil
		}
		if run.Status != RunActive || run.CurrentStep != StepMonitoring || run.Revision != request.RunRevision {
			return ErrStaleRun
		}
		nextAttempt := monitoring.AttemptCount + 1
		if result.ScanAttempted && scan.AttemptCount+1 > nextAttempt {
			nextAttempt = scan.AttemptCount + 1
		}
		var retryAfter any
		if safeCode == "prepare_review_failed" && nextAttempt <= 2 {
			delay := 5 * time.Second
			if nextAttempt == 2 {
				delay = 30 * time.Second
			}
			retryAfter = now.Add(delay)
		}
		monitoringOutcome := ""
		if result.MonitoringRetryBound {
			monitoringOutcome = "monitoring_retry_bound"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations SET status='failed',
			attempt_count=attempt_count+1, safe_outcome_code=?, safe_error_code=?, updated_at=?
			WHERE id=? AND owner_user_id=? AND status!='succeeded'`,
			monitoringOutcome, safeCode, now, monitoring.ID, request.OwnerUserID); err != nil {
			return err
		}
		if result.ScanAttempted {
			if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_operations SET status='failed',
				attempt_count=attempt_count+1, safe_error_code=?, updated_at=? WHERE id=? AND owner_user_id=? AND status!='succeeded'`,
				safeCode, now, scan.ID, request.OwnerUserID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_setup_runs SET last_error_code=?, failed_step=?, retry_after=?, updated_at=?
			WHERE id=? AND owner_user_id=?`, safeCode, map[bool]Step{true: StepInitialScan, false: StepMonitoring}[result.ScanAttempted], retryAfter, now, run.ID, request.OwnerUserID); err != nil {
			return err
		}
		updated, err = getRunWith(ctx, tx, request.OwnerUserID, run.ID)
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
		SET status=?, revision=revision+1, deferred_at=?, retry_after=NULL, updated_at=?
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

func (s *SQLiteStore) ListDueRetries(ctx context.Context, now time.Time, limit int) ([]Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM assistant_setup_runs
		WHERE status='active' AND current_step='monitoring' AND retry_after IS NOT NULL AND retry_after<=?
		ORDER BY retry_after, created_at LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	runs := make([]Run, 0)
	for rows.Next() {
		run, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		runs = append(runs, *run)
	}
	return runs, rows.Err()
}

func (s *SQLiteStore) ClearRetry(ctx context.Context, ownerUserID, runID string, ifVersion int64, safeCode string) error {
	if err := s.configured(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_setup_runs
		SET retry_after=NULL, last_error_code=CASE WHEN ?='' THEN last_error_code ELSE ? END, updated_at=?
		WHERE id=? AND owner_user_id=? AND revision=? AND status='active'`,
		strings.TrimSpace(safeCode), strings.TrimSpace(safeCode), s.now().UTC(), strings.TrimSpace(runID), strings.TrimSpace(ownerUserID), ifVersion)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrStaleRun
	}
	return nil
}

func (s *SQLiteStore) InvalidateRun(ctx context.Context, ownerUserID, runID string, ifVersion int64, safeCode string) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if ifVersion < 1 || strings.TrimSpace(safeCode) == "" {
		return nil, ErrInvalid
	}
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_setup_runs
		SET status='invalidated', revision=revision+1, last_error_code=?, retry_after=NULL,
			reconcile_after=NULL, updated_at=?
		WHERE id=? AND owner_user_id=? AND revision=?
			AND status IN ('active','deferred','reconcile_required','first_result')`,
		strings.TrimSpace(safeCode), now, strings.TrimSpace(runID), strings.TrimSpace(ownerUserID), ifVersion)
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
		WHERE owner_user_id=? AND capability_id=? AND status IN ('active','deferred','reconcile_required','first_result')
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
