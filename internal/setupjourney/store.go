package setupjourney

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

// Store is the bounded setup-progress persistence contract. Consequence owners
// are intentionally absent from this interface.
type Store interface {
	CreateOrGetRoot(ctx context.Context, spec RootSpec) (*Run, bool, error)
	GetRoot(ctx context.Context, ownerUserID, relationshipID, specialistSlug, journeyID string) (*Run, error)
	CreateOrGetChild(ctx context.Context, rootRunID string) (*Run, bool, error)
	GetRun(ctx context.Context, runID string) (*Run, error)
	ListChildRuns(ctx context.Context, rootRunID string) ([]*Run, error)
	CompareAndSwapRun(ctx context.Context, run *Run, expectedRevision int64) (*Run, error)
	ApplyDeclarationMigration(ctx context.Context, kind RunKind, runID string, expectedRevision int64, toSchemaVersion, toDeclarationVersion int, targetStepIDs []string, stepMappingDigest string) (*Run, *DeclarationMigrationReceipt, error)
	CreateOrGetReviewReceipt(ctx context.Context, spec ReviewReceiptSpec) (*ReviewReceipt, bool, error)
	GetReviewReceipt(ctx context.Context, token string) (*ReviewReceipt, error)
	ClaimOperation(ctx context.Context, claim OperationClaim) (*OperationReceipt, *Run, bool, error)
	GetOperationReceipt(ctx context.Context, kind RunKind, runID, idempotencyKey string) (*OperationReceipt, error)
	GetBusyOperationReceipt(ctx context.Context, kind RunKind, runID string) (*OperationReceipt, error)
	FinalizeOperation(ctx context.Context, run *Run, idempotencyKey string, completion OperationCompletion) (*OperationReceipt, *Run, bool, error)
	MarkOperationReconcileRequired(ctx context.Context, kind RunKind, runID, idempotencyKey string) (*OperationReceipt, error)
}

// SQLiteStore persists setup journey state in the shared application database.
type SQLiteStore struct {
	db  *database.DB
	now func() time.Time
}

func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db, now: time.Now}
}

var _ Store = (*SQLiteStore)(nil)

const runColumns = `id, run_kind, root_run_id, owner_user_id, relationship_id,
	specialist_slug, journey_id, declaration_schema_version, declaration_version,
	state_revision, lifecycle_state, current_step_id, step_states_json, dismissed,
	integration_plugin_id, integration_version, home_workspace_id,
	project_workspace_id, selected_mode_id, first_opened_at, last_dismissed_at,
	first_completed_at, created_at, updated_at`

const operationColumns = `run_kind, run_id, idempotency_key, step_id, action_id,
	input_digest, review_digest, status, result_code, reason_code, result_json,
	run_revision_before, run_revision_after, created_at, completed_at`

const reviewColumns = `token, run_kind, run_id, idempotency_key, step_id, action_id,
	input_digest, run_revision, owner_revision_digest, disclosure_digest,
	created_at, expires_at, consumed_at, consumed_by_idempotency_key`

type rowScanner interface {
	Scan(dest ...any) error
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// CreateOrGetRoot atomically creates one inert server-identified root for the
// exact accepted relationship/declaration identity or returns the existing row.
func (s *SQLiteStore) CreateOrGetRoot(ctx context.Context, spec RootSpec) (*Run, bool, error) {
	if err := s.configured(); err != nil {
		return nil, false, err
	}
	normalized, err := normalizeRootSpec(spec)
	if err != nil {
		return nil, false, err
	}
	now := s.now().UTC()
	run := &Run{
		ID:                       uuid.New().String(),
		Kind:                     RunKindRoot,
		OwnerUserID:              normalized.OwnerUserID,
		RelationshipID:           normalized.RelationshipID,
		SpecialistSlug:           normalized.SpecialistSlug,
		JourneyID:                normalized.JourneyID,
		DeclarationSchemaVersion: normalized.DeclarationSchemaVersion,
		DeclarationVersion:       normalized.DeclarationVersion,
		StateRevision:            1,
		Lifecycle:                LifecycleNotStarted,
		CurrentStepID:            normalized.StepStates[0].StepID,
		StepStates:               normalized.StepStates,
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	stepJSON, err := encodeStepStates(run.StepStates)
	if err != nil {
		return nil, false, err
	}

	var result *Run
	created := false
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		execResult, execErr := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO setup_journey_run (
				id, run_kind, root_run_id, owner_user_id, relationship_id,
				specialist_slug, journey_id, declaration_schema_version, declaration_version,
				state_revision, lifecycle_state, current_step_id, step_states_json, dismissed,
				created_at, updated_at
			) VALUES (?, 'root', NULL, ?, ?, ?, ?, ?, ?, 1, 'not_started', ?, ?, 0, ?, ?)
		`, run.ID, run.OwnerUserID, run.RelationshipID, run.SpecialistSlug, run.JourneyID,
			run.DeclarationSchemaVersion, run.DeclarationVersion, run.CurrentStepID,
			stepJSON, run.CreatedAt, run.UpdatedAt)
		if execErr != nil {
			return execErr
		}
		rows, rowsErr := execResult.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		created = rows == 1
		result, execErr = getRootWith(ctx, tx, run.OwnerUserID, run.RelationshipID, run.SpecialistSlug, run.JourneyID)
		return execErr
	})
	if err != nil {
		return nil, false, fmt.Errorf("setup journey: create root: %w", err)
	}
	return result.Clone(), created, nil
}

func (s *SQLiteStore) GetRoot(ctx context.Context, ownerUserID, relationshipID, specialistSlug, journeyID string) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	spec, err := normalizeRootIdentity(ownerUserID, relationshipID, specialistSlug, journeyID)
	if err != nil {
		return nil, err
	}
	run, err := getRootWith(ctx, s.db, spec.OwnerUserID, spec.RelationshipID, spec.SpecialistSlug, spec.JourneyID)
	if err != nil {
		return nil, err
	}
	return run.Clone(), nil
}

// CreateOrGetChild returns the one resumable unbound child when present;
// otherwise it creates a fresh independently revisioned child before any
// workspace consequence exists.
func (s *SQLiteStore) CreateOrGetChild(ctx context.Context, rootRunID string) (*Run, bool, error) {
	if err := s.configured(); err != nil {
		return nil, false, err
	}
	rootRunID = strings.TrimSpace(rootRunID)
	if !validRunID(rootRunID) {
		return nil, false, ErrInvalid
	}
	var result *Run
	created := false
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		root, getErr := getRunWith(ctx, tx, rootRunID)
		if getErr != nil {
			return getErr
		}
		if root.Kind != RunKindRoot {
			return ErrInvalid
		}
		if root.NeedsNormalization || len(root.StepStates) == 0 {
			return ErrMalformed
		}

		existing, getErr := getUnboundChildWith(ctx, tx, rootRunID)
		if getErr == nil {
			result = existing
			return nil
		}
		if !errors.Is(getErr, ErrNotFound) {
			return getErr
		}

		now := s.now().UTC()
		states := make([]StepState, len(root.StepStates))
		for index, state := range root.StepStates {
			states[index] = StepState{StepID: state.StepID, Status: StepPending}
		}
		stepJSON, encodeErr := encodeStepStates(states)
		if encodeErr != nil {
			return encodeErr
		}
		candidateID := uuid.New().String()
		execResult, execErr := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO setup_journey_run (
				id, run_kind, root_run_id, owner_user_id, relationship_id,
				specialist_slug, journey_id, declaration_schema_version, declaration_version,
				state_revision, lifecycle_state, current_step_id, step_states_json, dismissed,
				created_at, updated_at
			) VALUES (?, 'child', ?, '', '', '', ?, ?, ?, 1, 'not_started', ?, ?, 0, ?, ?)
		`, candidateID, rootRunID, root.JourneyID, root.DeclarationSchemaVersion,
			root.DeclarationVersion, states[0].StepID, stepJSON, now, now)
		if execErr != nil {
			return execErr
		}
		rows, rowsErr := execResult.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		created = rows == 1
		result, execErr = getUnboundChildWith(ctx, tx, rootRunID)
		return execErr
	})
	if err != nil {
		return nil, false, fmt.Errorf("setup journey: create child: %w", err)
	}
	return result.Clone(), created, nil
}

func (s *SQLiteStore) GetRun(ctx context.Context, runID string) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	runID = strings.TrimSpace(runID)
	if !validRunID(runID) {
		return nil, ErrInvalid
	}
	run, err := getRunWith(ctx, s.db, runID)
	if err != nil {
		return nil, err
	}
	return run.Clone(), nil
}

func (s *SQLiteStore) ListChildRuns(ctx context.Context, rootRunID string) ([]*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	rootRunID = strings.TrimSpace(rootRunID)
	if !validRunID(rootRunID) {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+runColumns+` FROM setup_journey_run
		WHERE run_kind = 'child' AND root_run_id = ?
		ORDER BY created_at ASC, id ASC
	`, rootRunID)
	if err != nil {
		return nil, fmt.Errorf("setup journey: list children: %w", err)
	}
	defer func() { _ = rows.Close() }()

	children := make([]*Run, 0)
	for rows.Next() {
		child, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		children = append(children, child)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("setup journey: iterate children: %w", err)
	}
	return children, nil
}

// CompareAndSwapRun persists a reconciled structural projection. It refuses to
// race a claimed/uncertain operation and increments the run revision exactly
// once when the expected revision wins.
func (s *SQLiteStore) CompareAndSwapRun(ctx context.Context, run *Run, expectedRevision int64) (*Run, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if run == nil || expectedRevision <= 0 || run.StateRevision != expectedRevision {
		return nil, ErrInvalid
	}
	candidate, stepJSON, err := normalizeRunForWrite(run)
	if err != nil {
		return nil, err
	}
	var updated *Run
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		current, getErr := getRunWith(ctx, tx, candidate.ID)
		if getErr != nil {
			return getErr
		}
		if current.StateRevision != expectedRevision {
			return ErrConflict
		}
		if !sameRunIdentity(current, candidate) {
			return ErrInvalid
		}
		if busy, busyErr := hasBusyOperation(ctx, tx, current.Kind, current.ID); busyErr != nil {
			return busyErr
		} else if busy {
			return ErrOperationBusy
		}
		if preserveErr := preserveHistoricalTimestamps(current, candidate); preserveErr != nil {
			return preserveErr
		}
		now := s.now().UTC()
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE setup_journey_run SET
				declaration_schema_version = ?, declaration_version = ?,
				state_revision = state_revision + 1, lifecycle_state = ?, current_step_id = ?,
				step_states_json = ?, dismissed = ?, integration_plugin_id = ?,
				integration_version = ?, home_workspace_id = ?, project_workspace_id = ?,
				selected_mode_id = ?, first_opened_at = ?, last_dismissed_at = ?,
				first_completed_at = ?, updated_at = ?
			WHERE id = ? AND state_revision = ?
		`, candidate.DeclarationSchemaVersion, candidate.DeclarationVersion,
			candidate.Lifecycle, candidate.CurrentStepID, stepJSON, boolInt(candidate.Dismissed),
			candidate.IntegrationPluginID, candidate.IntegrationVersion, candidate.HomeWorkspaceID,
			candidate.ProjectWorkspaceID, candidate.SelectedModeID, candidate.FirstOpenedAt,
			candidate.LastDismissedAt, candidate.FirstCompletedAt, now, candidate.ID, expectedRevision)
		if updateErr != nil {
			if isUniqueConstraint(updateErr) {
				return ErrConflict
			}
			return updateErr
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if changed != 1 {
			return ErrConflict
		}
		updated, updateErr = getRunWith(ctx, tx, candidate.ID)
		return updateErr
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) ||
			errors.Is(err, ErrOperationBusy) || errors.Is(err, ErrInvalid) {
			return nil, err
		}
		return nil, fmt.Errorf("setup journey: compare and swap run: %w", err)
	}
	return updated.Clone(), nil
}

// ApplyDeclarationMigration records one exact compiled migration and resets the
// target declaration's structural steps to unfinished. Canonical identifiers,
// presentation history, and first-completion history are not rewritten; the
// service immediately reconciles the migrated shape from canonical owners.
func (s *SQLiteStore) ApplyDeclarationMigration(
	ctx context.Context,
	kind RunKind,
	runID string,
	expectedRevision int64,
	toSchemaVersion int,
	toDeclarationVersion int,
	targetStepIDs []string,
	stepMappingDigest string,
) (*Run, *DeclarationMigrationReceipt, error) {
	if err := s.configured(); err != nil {
		return nil, nil, err
	}
	runID = strings.TrimSpace(runID)
	stepMappingDigest = strings.ToLower(strings.TrimSpace(stepMappingDigest))
	states, err := normalizeStepIDs(targetStepIDs)
	if !validateRunKind(kind) || !validRunID(runID) || expectedRevision <= 0 ||
		toSchemaVersion <= 0 || toSchemaVersion > 1_000_000 ||
		toDeclarationVersion <= 0 || toDeclarationVersion > 1_000_000 ||
		!validateDigest(stepMappingDigest, false) || err != nil {
		return nil, nil, ErrInvalid
	}
	stepJSON, err := encodeStepStates(states)
	if err != nil {
		return nil, nil, err
	}

	var migrated *Run
	var receipt *DeclarationMigrationReceipt
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		current, getErr := getRunWith(ctx, tx, runID)
		if getErr != nil {
			return getErr
		}
		if current.Kind != kind {
			return ErrNotFound
		}
		if current.StateRevision != expectedRevision {
			return ErrConflict
		}
		if busy, busyErr := hasBusyOperation(ctx, tx, kind, runID); busyErr != nil {
			return busyErr
		} else if busy {
			return ErrOperationBusy
		}
		now := s.now().UTC()
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE setup_journey_run SET
				declaration_schema_version = ?, declaration_version = ?,
				state_revision = state_revision + 1, lifecycle_state = 'needs_attention',
				current_step_id = ?, step_states_json = ?, updated_at = ?
			WHERE id = ? AND run_kind = ? AND state_revision = ?
		`, toSchemaVersion, toDeclarationVersion, states[0].StepID, stepJSON, now,
			runID, kind, expectedRevision)
		if updateErr != nil {
			return updateErr
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if changed != 1 {
			return ErrConflict
		}
		receipt = &DeclarationMigrationReceipt{
			RunKind: kind, RunID: runID,
			FromSchemaVersion:      current.DeclarationSchemaVersion,
			FromDeclarationVersion: current.DeclarationVersion,
			ToSchemaVersion:        toSchemaVersion, ToDeclarationVersion: toDeclarationVersion,
			StepMappingDigest: stepMappingDigest, RunRevisionBefore: expectedRevision,
			RunRevisionAfter: expectedRevision + 1, CreatedAt: now,
		}
		_, insertErr := tx.ExecContext(ctx, `
			INSERT INTO setup_journey_declaration_migration_receipt (
				run_kind, run_id, from_schema_version, from_declaration_version,
				to_schema_version, to_declaration_version, step_mapping_digest,
				run_revision_before, run_revision_after, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, receipt.RunKind, receipt.RunID, receipt.FromSchemaVersion,
			receipt.FromDeclarationVersion, receipt.ToSchemaVersion,
			receipt.ToDeclarationVersion, receipt.StepMappingDigest,
			receipt.RunRevisionBefore, receipt.RunRevisionAfter, receipt.CreatedAt)
		if insertErr != nil {
			if isUniqueConstraint(insertErr) {
				return ErrConflict
			}
			return insertErr
		}
		migrated, getErr = getRunWith(ctx, tx, runID)
		return getErr
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) ||
			errors.Is(err, ErrOperationBusy) || errors.Is(err, ErrInvalid) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("setup journey: apply declaration migration: %w", err)
	}
	return migrated.Clone(), receipt, nil
}

// CreateOrGetReviewReceipt persists only consent-binding digests. Repeating the
// same review idempotency key returns the original server token.
func (s *SQLiteStore) CreateOrGetReviewReceipt(ctx context.Context, spec ReviewReceiptSpec) (*ReviewReceipt, bool, error) {
	if err := s.configured(); err != nil {
		return nil, false, err
	}
	normalized, err := normalizeReviewSpec(spec)
	if err != nil {
		return nil, false, err
	}
	var receipt *ReviewReceipt
	created := false
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		existing, getErr := getReviewByIdempotencyWith(
			ctx, tx, normalized.RunKind, normalized.RunID, normalized.IdempotencyKey,
		)
		if getErr == nil {
			if !reviewMatchesSpec(existing, normalized) {
				return ErrIdempotencyConflict
			}
			receipt = existing
			return nil
		}
		if !errors.Is(getErr, ErrNotFound) {
			return getErr
		}
		run, getErr := getRunWith(ctx, tx, normalized.RunID)
		if getErr != nil {
			return getErr
		}
		if run.Kind != normalized.RunKind {
			return ErrNotFound
		}
		if run.StateRevision != normalized.RunRevision {
			return ErrConflict
		}
		if busy, busyErr := hasBusyOperation(ctx, tx, run.Kind, run.ID); busyErr != nil {
			return busyErr
		} else if busy {
			return ErrOperationBusy
		}
		now := s.now().UTC()
		receipt = &ReviewReceipt{
			Token: uuid.New().String(), RunKind: normalized.RunKind, RunID: normalized.RunID,
			IdempotencyKey: normalized.IdempotencyKey, StepID: normalized.StepID,
			ActionID: normalized.ActionID, InputDigest: normalized.InputDigest,
			RunRevision: normalized.RunRevision, OwnerRevisionDigest: normalized.OwnerRevisionDigest,
			DisclosureDigest: normalized.DisclosureDigest, CreatedAt: now,
			ExpiresAt: now.Add(normalized.TTL),
		}
		_, insertErr := tx.ExecContext(ctx, `
			INSERT INTO setup_journey_review_receipt (
				token, run_kind, run_id, idempotency_key, step_id, action_id,
				input_digest, run_revision, owner_revision_digest, disclosure_digest,
				created_at, expires_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, receipt.Token, receipt.RunKind, receipt.RunID, receipt.IdempotencyKey,
			receipt.StepID, receipt.ActionID, receipt.InputDigest, receipt.RunRevision,
			receipt.OwnerRevisionDigest, receipt.DisclosureDigest, receipt.CreatedAt,
			receipt.ExpiresAt)
		if insertErr != nil {
			if isUniqueConstraint(insertErr) {
				return ErrIdempotencyConflict
			}
			return insertErr
		}
		created = true
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) ||
			errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrOperationBusy) ||
			errors.Is(err, ErrInvalid) {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("setup journey: create review receipt: %w", err)
	}
	return receipt.Clone(), created, nil
}

func (s *SQLiteStore) GetReviewReceipt(ctx context.Context, token string) (*ReviewReceipt, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if !validRunID(token) {
		return nil, ErrInvalid
	}
	receipt, err := getReviewWith(ctx, s.db, token)
	if err != nil {
		return nil, err
	}
	return receipt.Clone(), nil
}

// ClaimOperation first checks for replay, then atomically inserts the operation
// claim, consumes an exact review when required, and advances the run CAS token
// before any canonical owner is called.
func (s *SQLiteStore) ClaimOperation(ctx context.Context, claim OperationClaim) (*OperationReceipt, *Run, bool, error) {
	if err := s.configured(); err != nil {
		return nil, nil, false, err
	}
	normalized, err := normalizeOperationClaim(claim)
	if err != nil {
		return nil, nil, false, err
	}
	var receipt *OperationReceipt
	var run *Run
	replayed := false
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		existing, getErr := getOperationWith(ctx, tx, normalized.RunKind, normalized.RunID, normalized.IdempotencyKey)
		if getErr == nil {
			if !operationMatchesClaim(existing, normalized) {
				return ErrIdempotencyConflict
			}
			receipt = existing
			run, getErr = getRunWith(ctx, tx, normalized.RunID)
			replayed = true
			return getErr
		}
		if !errors.Is(getErr, ErrNotFound) {
			return getErr
		}

		current, getErr := getRunWith(ctx, tx, normalized.RunID)
		if getErr != nil {
			return getErr
		}
		if current.Kind != normalized.RunKind {
			return ErrNotFound
		}
		if current.StateRevision != normalized.IfRevision {
			return ErrConflict
		}
		if busy, busyErr := hasBusyOperation(ctx, tx, current.Kind, current.ID); busyErr != nil {
			return busyErr
		} else if busy {
			return ErrOperationBusy
		}

		now := s.now().UTC()
		var review *ReviewReceipt
		if normalized.ReviewToken != "" {
			review, getErr = getReviewWith(ctx, tx, normalized.ReviewToken)
			if getErr != nil {
				return ErrConflict
			}
			if review.RunKind != normalized.RunKind || review.RunID != normalized.RunID ||
				review.StepID != normalized.StepID || review.ActionID != normalized.ActionID ||
				review.InputDigest != normalized.InputDigest ||
				review.DisclosureDigest != normalized.ReviewDigest ||
				review.RunRevision != normalized.IfRevision || review.ConsumedAt != nil ||
				!review.ExpiresAt.After(now) {
				return ErrConflict
			}
		}
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE setup_journey_run
			SET state_revision = state_revision + 1, updated_at = ?
			WHERE id = ? AND run_kind = ? AND state_revision = ?
		`, now, current.ID, current.Kind, normalized.IfRevision)
		if updateErr != nil {
			return updateErr
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if changed != 1 {
			return ErrConflict
		}
		receipt = &OperationReceipt{
			RunKind: normalized.RunKind, RunID: normalized.RunID,
			IdempotencyKey: normalized.IdempotencyKey, StepID: normalized.StepID,
			ActionID: normalized.ActionID, InputDigest: normalized.InputDigest,
			ReviewDigest: normalized.ReviewDigest, Status: OperationClaimed,
			RunRevisionBefore: normalized.IfRevision, RunRevisionAfter: normalized.IfRevision + 1,
			CreatedAt: now,
		}
		_, insertErr := tx.ExecContext(ctx, `
			INSERT INTO setup_journey_operation_receipt (
				run_kind, run_id, idempotency_key, step_id, action_id,
				input_digest, review_digest, status, result_code, reason_code,
				result_json, run_revision_before, run_revision_after, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'claimed', '', '', '{}', ?, ?, ?)
		`, receipt.RunKind, receipt.RunID, receipt.IdempotencyKey, receipt.StepID,
			receipt.ActionID, receipt.InputDigest, receipt.ReviewDigest,
			receipt.RunRevisionBefore, receipt.RunRevisionAfter, receipt.CreatedAt)
		if insertErr != nil {
			if isUniqueConstraint(insertErr) {
				return ErrOperationBusy
			}
			return insertErr
		}
		if review != nil {
			consumeResult, consumeErr := tx.ExecContext(ctx, `
				UPDATE setup_journey_review_receipt
				SET consumed_at = ?, consumed_by_idempotency_key = ?
				WHERE token = ? AND consumed_at IS NULL
			`, now, normalized.IdempotencyKey, review.Token)
			if consumeErr != nil {
				return consumeErr
			}
			consumed, rowsErr := consumeResult.RowsAffected()
			if rowsErr != nil {
				return rowsErr
			}
			if consumed != 1 {
				return ErrConflict
			}
		}
		run, getErr = getRunWith(ctx, tx, normalized.RunID)
		return getErr
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) ||
			errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrOperationBusy) ||
			errors.Is(err, ErrInvalid) {
			return nil, nil, false, err
		}
		return nil, nil, false, fmt.Errorf("setup journey: claim operation: %w", err)
	}
	return receipt.Clone(), run.Clone(), replayed, nil
}

func (s *SQLiteStore) GetOperationReceipt(ctx context.Context, kind RunKind, runID, idempotencyKey string) (*OperationReceipt, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	kind, runID, idempotencyKey, err := normalizeOperationAddress(kind, runID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	receipt, err := getOperationWith(ctx, s.db, kind, runID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return receipt.Clone(), nil
}

func (s *SQLiteStore) GetBusyOperationReceipt(ctx context.Context, kind RunKind, runID string) (*OperationReceipt, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	runID = strings.TrimSpace(runID)
	if !validateRunKind(kind) || !validRunID(runID) {
		return nil, ErrInvalid
	}
	receipt, err := scanOperationRow(s.db.QueryRowContext(ctx, `
		SELECT `+operationColumns+` FROM setup_journey_operation_receipt
		WHERE run_kind = ? AND run_id = ? AND status IN ('claimed', 'reconcile_required')
		ORDER BY created_at ASC LIMIT 1
	`, kind, runID))
	if err != nil {
		return nil, err
	}
	return receipt.Clone(), nil
}

// FinalizeOperation stores a terminal receipt and the reconciled run projection
// in one transaction. The revision claimed before the side effect remains the
// request's single after-revision; finalization does not consume another token.
func (s *SQLiteStore) FinalizeOperation(ctx context.Context, run *Run, idempotencyKey string, completion OperationCompletion) (*OperationReceipt, *Run, bool, error) {
	if err := s.configured(); err != nil {
		return nil, nil, false, err
	}
	if run == nil {
		return nil, nil, false, ErrInvalid
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !validateCanonicalRef(idempotencyKey, false) || len(idempotencyKey) > MaxIdempotencyKeyBytes {
		return nil, nil, false, ErrInvalid
	}
	normalizedRun, stepJSON, err := normalizeRunForWrite(run)
	if err != nil {
		return nil, nil, false, err
	}
	normalizedCompletion, resultJSON, err := normalizeOperationCompletion(completion)
	if err != nil {
		return nil, nil, false, err
	}

	var receipt *OperationReceipt
	var updated *Run
	replayed := false
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		currentReceipt, getErr := getOperationWith(ctx, tx, normalizedRun.Kind, normalizedRun.ID, idempotencyKey)
		if getErr != nil {
			return getErr
		}
		if currentReceipt.Status == OperationSucceeded || currentReceipt.Status == OperationFailed {
			if !operationMatchesCompletion(currentReceipt, normalizedCompletion) {
				return ErrIdempotencyConflict
			}
			receipt = currentReceipt
			updated, getErr = getRunWith(ctx, tx, normalizedRun.ID)
			replayed = true
			return getErr
		}
		if currentReceipt.Status != OperationClaimed && currentReceipt.Status != OperationReconcileRequired {
			return ErrConflict
		}
		current, getErr := getRunWith(ctx, tx, normalizedRun.ID)
		if getErr != nil {
			return getErr
		}
		if current.StateRevision != currentReceipt.RunRevisionAfter ||
			normalizedRun.StateRevision != currentReceipt.RunRevisionAfter {
			return ErrConflict
		}
		if !sameRunIdentity(current, normalizedRun) {
			return ErrInvalid
		}
		if preserveErr := preserveHistoricalTimestamps(current, normalizedRun); preserveErr != nil {
			return preserveErr
		}
		now := s.now().UTC()
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE setup_journey_run SET
				declaration_schema_version = ?, declaration_version = ?,
				lifecycle_state = ?, current_step_id = ?, step_states_json = ?, dismissed = ?,
				integration_plugin_id = ?, integration_version = ?, home_workspace_id = ?,
				project_workspace_id = ?, selected_mode_id = ?, first_opened_at = ?,
				last_dismissed_at = ?, first_completed_at = ?, updated_at = ?
			WHERE id = ? AND run_kind = ? AND state_revision = ?
		`, normalizedRun.DeclarationSchemaVersion, normalizedRun.DeclarationVersion,
			normalizedRun.Lifecycle, normalizedRun.CurrentStepID, stepJSON, boolInt(normalizedRun.Dismissed),
			normalizedRun.IntegrationPluginID, normalizedRun.IntegrationVersion, normalizedRun.HomeWorkspaceID,
			normalizedRun.ProjectWorkspaceID, normalizedRun.SelectedModeID, normalizedRun.FirstOpenedAt,
			normalizedRun.LastDismissedAt, normalizedRun.FirstCompletedAt, now,
			normalizedRun.ID, normalizedRun.Kind, currentReceipt.RunRevisionAfter)
		if updateErr != nil {
			if isUniqueConstraint(updateErr) {
				return ErrConflict
			}
			return updateErr
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if changed != 1 {
			return ErrConflict
		}
		result, updateErr = tx.ExecContext(ctx, `
			UPDATE setup_journey_operation_receipt SET
				status = ?, result_code = ?, reason_code = ?, result_json = ?, completed_at = ?
			WHERE run_kind = ? AND run_id = ? AND idempotency_key = ?
				AND status IN ('claimed', 'reconcile_required')
		`, normalizedCompletion.Status, normalizedCompletion.ResultCode,
			normalizedCompletion.ReasonCode, resultJSON, now, normalizedRun.Kind,
			normalizedRun.ID, idempotencyKey)
		if updateErr != nil {
			return updateErr
		}
		changed, rowsErr = result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if changed != 1 {
			return ErrConflict
		}
		receipt, getErr = getOperationWith(ctx, tx, normalizedRun.Kind, normalizedRun.ID, idempotencyKey)
		if getErr != nil {
			return getErr
		}
		updated, getErr = getRunWith(ctx, tx, normalizedRun.ID)
		return getErr
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) ||
			errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrInvalid) {
			return nil, nil, false, err
		}
		return nil, nil, false, fmt.Errorf("setup journey: finalize operation: %w", err)
	}
	return receipt.Clone(), updated.Clone(), replayed, nil
}

// MarkOperationReconcileRequired records an uncertain owner outcome without
// inventing success or releasing the per-run busy guard.
func (s *SQLiteStore) MarkOperationReconcileRequired(ctx context.Context, kind RunKind, runID, idempotencyKey string) (*OperationReceipt, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	kind, runID, idempotencyKey, err := normalizeOperationAddress(kind, runID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	var receipt *OperationReceipt
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		current, getErr := getOperationWith(ctx, tx, kind, runID, idempotencyKey)
		if getErr != nil {
			return getErr
		}
		switch current.Status {
		case OperationReconcileRequired:
			receipt = current
			return nil
		case OperationClaimed:
			if _, updateErr := tx.ExecContext(ctx, `
				UPDATE setup_journey_operation_receipt SET status = 'reconcile_required'
				WHERE run_kind = ? AND run_id = ? AND idempotency_key = ? AND status = 'claimed'
			`, kind, runID, idempotencyKey); updateErr != nil {
				return updateErr
			}
			receipt, getErr = getOperationWith(ctx, tx, kind, runID, idempotencyKey)
			return getErr
		default:
			return ErrConflict
		}
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) {
			return nil, err
		}
		return nil, fmt.Errorf("setup journey: mark operation for reconciliation: %w", err)
	}
	return receipt.Clone(), nil
}

func (s *SQLiteStore) configured() error {
	if s == nil || s.db == nil || s.now == nil {
		return fmt.Errorf("setup journey: store is not configured")
	}
	return nil
}

type normalizedRootSpec struct {
	RootSpec
	StepStates []StepState
}

func normalizeRootSpec(spec RootSpec) (normalizedRootSpec, error) {
	identity, err := normalizeRootIdentity(spec.OwnerUserID, spec.RelationshipID, spec.SpecialistSlug, spec.JourneyID)
	if err != nil {
		return normalizedRootSpec{}, err
	}
	if spec.DeclarationSchemaVersion <= 0 || spec.DeclarationSchemaVersion > 1_000_000 ||
		spec.DeclarationVersion <= 0 || spec.DeclarationVersion > 1_000_000 {
		return normalizedRootSpec{}, ErrInvalid
	}
	states, err := normalizeStepIDs(spec.StepIDs)
	if err != nil {
		return normalizedRootSpec{}, err
	}
	identity.DeclarationSchemaVersion = spec.DeclarationSchemaVersion
	identity.DeclarationVersion = spec.DeclarationVersion
	return normalizedRootSpec{RootSpec: identity, StepStates: states}, nil
}

func normalizeRootIdentity(ownerUserID, relationshipID, specialistSlug, journeyID string) (RootSpec, error) {
	spec := RootSpec{
		OwnerUserID:    strings.TrimSpace(ownerUserID),
		RelationshipID: strings.TrimSpace(relationshipID),
		SpecialistSlug: strings.ToLower(strings.TrimSpace(specialistSlug)),
		JourneyID:      strings.ToLower(strings.TrimSpace(journeyID)),
	}
	if !validateCanonicalRef(spec.OwnerUserID, false) ||
		!validateCanonicalRef(spec.RelationshipID, false) ||
		!validateStableID(spec.SpecialistSlug) || !validateStableID(spec.JourneyID) {
		return RootSpec{}, ErrInvalid
	}
	return spec, nil
}

func normalizeRunForWrite(source *Run) (*Run, string, error) {
	if source == nil {
		return nil, "", ErrInvalid
	}
	run := source.Clone()
	run.RootRunID = strings.TrimSpace(run.RootRunID)
	run.OwnerUserID = strings.TrimSpace(run.OwnerUserID)
	run.RelationshipID = strings.TrimSpace(run.RelationshipID)
	run.SpecialistSlug = strings.ToLower(strings.TrimSpace(run.SpecialistSlug))
	run.JourneyID = strings.ToLower(strings.TrimSpace(run.JourneyID))
	run.CurrentStepID = strings.ToLower(strings.TrimSpace(run.CurrentStepID))
	run.IntegrationPluginID = strings.TrimSpace(run.IntegrationPluginID)
	run.IntegrationVersion = strings.TrimSpace(run.IntegrationVersion)
	run.HomeWorkspaceID = strings.TrimSpace(run.HomeWorkspaceID)
	run.ProjectWorkspaceID = strings.TrimSpace(run.ProjectWorkspaceID)
	run.SelectedModeID = strings.TrimSpace(run.SelectedModeID)
	run.NeedsNormalization = false

	if !validRunID(run.ID) || !validateRunKind(run.Kind) || run.StateRevision <= 0 ||
		run.DeclarationSchemaVersion <= 0 || run.DeclarationSchemaVersion > 1_000_000 ||
		run.DeclarationVersion <= 0 || run.DeclarationVersion > 1_000_000 ||
		!validateStableID(run.JourneyID) || !validateLifecycle(run.Lifecycle) {
		return nil, "", ErrInvalid
	}
	if run.CurrentStepID != "" && !validateStableID(run.CurrentStepID) {
		return nil, "", ErrInvalid
	}
	if !validateCanonicalRef(run.IntegrationPluginID, true) ||
		!validateCanonicalRef(run.IntegrationVersion, true) ||
		!validateCanonicalRef(run.HomeWorkspaceID, true) ||
		!validateCanonicalRef(run.ProjectWorkspaceID, true) ||
		!validateCanonicalRef(run.SelectedModeID, true) {
		return nil, "", ErrInvalid
	}
	if run.Kind == RunKindRoot {
		if run.RootRunID != "" || !validateCanonicalRef(run.OwnerUserID, false) ||
			!validateCanonicalRef(run.RelationshipID, false) || !validateStableID(run.SpecialistSlug) {
			return nil, "", ErrInvalid
		}
	} else {
		if !validRunID(run.RootRunID) || run.OwnerUserID != "" || run.RelationshipID != "" ||
			run.SpecialistSlug != "" || run.IntegrationPluginID != "" ||
			run.IntegrationVersion != "" || run.HomeWorkspaceID != "" {
			return nil, "", ErrInvalid
		}
		if run.ProjectWorkspaceID == "" && run.Lifecycle == LifecycleReady {
			return nil, "", ErrInvalid
		}
	}
	states, err := normalizeStepStates(run.StepStates)
	if err != nil {
		return nil, "", err
	}
	run.StepStates = states
	if run.CurrentStepID != "" && !containsStep(states, run.CurrentStepID) {
		return nil, "", ErrInvalid
	}
	if run.Lifecycle == LifecycleReady && (run.CurrentStepID != "" || run.FirstCompletedAt == nil) {
		return nil, "", ErrInvalid
	}
	if run.Lifecycle == LifecycleNotStarted && (run.FirstOpenedAt != nil || run.FirstCompletedAt != nil) {
		return nil, "", ErrInvalid
	}
	if run.Dismissed && run.LastDismissedAt == nil {
		return nil, "", ErrInvalid
	}
	stepJSON, err := encodeStepStates(states)
	if err != nil {
		return nil, "", err
	}
	return run, stepJSON, nil
}

func containsStep(states []StepState, stepID string) bool {
	for _, state := range states {
		if state.StepID == stepID {
			return true
		}
	}
	return false
}

func preserveHistoricalTimestamps(current, candidate *Run) error {
	if !sameOptionalTime(current.FirstOpenedAt, candidate.FirstOpenedAt) && current.FirstOpenedAt != nil {
		return ErrInvalid
	}
	if !sameOptionalTime(current.FirstCompletedAt, candidate.FirstCompletedAt) && current.FirstCompletedAt != nil {
		return ErrInvalid
	}
	if candidate.FirstCompletedAt != nil && current.FirstCompletedAt == nil && candidate.Lifecycle != LifecycleReady {
		return ErrInvalid
	}
	candidate.CreatedAt = current.CreatedAt
	return nil
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func sameRunIdentity(left, right *Run) bool {
	return left.ID == right.ID && left.Kind == right.Kind && left.RootRunID == right.RootRunID &&
		left.OwnerUserID == right.OwnerUserID && left.RelationshipID == right.RelationshipID &&
		left.SpecialistSlug == right.SpecialistSlug && left.JourneyID == right.JourneyID
}

func normalizeReviewSpec(spec ReviewReceiptSpec) (ReviewReceiptSpec, error) {
	spec.RunID = strings.TrimSpace(spec.RunID)
	spec.IdempotencyKey = strings.TrimSpace(spec.IdempotencyKey)
	spec.StepID = strings.ToLower(strings.TrimSpace(spec.StepID))
	spec.ActionID = strings.ToLower(strings.TrimSpace(spec.ActionID))
	spec.InputDigest = strings.ToLower(strings.TrimSpace(spec.InputDigest))
	spec.OwnerRevisionDigest = strings.ToLower(strings.TrimSpace(spec.OwnerRevisionDigest))
	spec.DisclosureDigest = strings.ToLower(strings.TrimSpace(spec.DisclosureDigest))
	if !validateRunKind(spec.RunKind) || !validRunID(spec.RunID) ||
		!validateCanonicalRef(spec.IdempotencyKey, false) ||
		len(spec.IdempotencyKey) > MaxIdempotencyKeyBytes || !validateStableID(spec.StepID) ||
		!validateStableID(spec.ActionID) || !validateDigest(spec.InputDigest, false) ||
		!validateDigest(spec.OwnerRevisionDigest, false) || !validateDigest(spec.DisclosureDigest, false) ||
		spec.RunRevision <= 0 || spec.TTL <= 0 || spec.TTL > time.Hour {
		return ReviewReceiptSpec{}, ErrInvalid
	}
	return spec, nil
}

func reviewMatchesSpec(receipt *ReviewReceipt, spec ReviewReceiptSpec) bool {
	return receipt.RunKind == spec.RunKind && receipt.RunID == spec.RunID &&
		receipt.IdempotencyKey == spec.IdempotencyKey && receipt.StepID == spec.StepID &&
		receipt.ActionID == spec.ActionID && receipt.InputDigest == spec.InputDigest &&
		receipt.RunRevision == spec.RunRevision &&
		receipt.OwnerRevisionDigest == spec.OwnerRevisionDigest &&
		receipt.DisclosureDigest == spec.DisclosureDigest
}

func normalizeOperationClaim(claim OperationClaim) (OperationClaim, error) {
	claim.RunID = strings.TrimSpace(claim.RunID)
	claim.IdempotencyKey = strings.TrimSpace(claim.IdempotencyKey)
	claim.StepID = strings.ToLower(strings.TrimSpace(claim.StepID))
	claim.ActionID = strings.ToLower(strings.TrimSpace(claim.ActionID))
	claim.InputDigest = strings.ToLower(strings.TrimSpace(claim.InputDigest))
	claim.ReviewToken = strings.TrimSpace(claim.ReviewToken)
	claim.ReviewDigest = strings.ToLower(strings.TrimSpace(claim.ReviewDigest))
	if !validateRunKind(claim.RunKind) || !validRunID(claim.RunID) || claim.IfRevision <= 0 ||
		!validateCanonicalRef(claim.IdempotencyKey, false) || len(claim.IdempotencyKey) > MaxIdempotencyKeyBytes ||
		!validateStableID(claim.StepID) || !validateStableID(claim.ActionID) ||
		!validateDigest(claim.InputDigest, false) || !validateDigest(claim.ReviewDigest, true) ||
		(claim.ReviewToken == "") != (claim.ReviewDigest == "") ||
		(claim.ReviewToken != "" && !validRunID(claim.ReviewToken)) {
		return OperationClaim{}, ErrInvalid
	}
	return claim, nil
}

func normalizeOperationAddress(kind RunKind, runID, idempotencyKey string) (RunKind, string, string, error) {
	runID = strings.TrimSpace(runID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !validateRunKind(kind) || !validRunID(runID) ||
		!validateCanonicalRef(idempotencyKey, false) || len(idempotencyKey) > MaxIdempotencyKeyBytes {
		return "", "", "", ErrInvalid
	}
	return kind, runID, idempotencyKey, nil
}

func normalizeOperationCompletion(completion OperationCompletion) (OperationCompletion, string, error) {
	completion.ResultCode = ResultCode(strings.ToLower(strings.TrimSpace(string(completion.ResultCode))))
	if completion.Status != OperationSucceeded && completion.Status != OperationFailed {
		return OperationCompletion{}, "", ErrInvalid
	}
	if !validateResultCode(completion.ResultCode) {
		return OperationCompletion{}, "", ErrInvalid
	}
	if completion.Status == OperationSucceeded {
		if completion.ReasonCode != "" {
			return OperationCompletion{}, "", ErrInvalid
		}
	} else if !validateReasonCode(completion.ReasonCode, false) {
		return OperationCompletion{}, "", ErrInvalid
	}
	result, resultJSON, err := normalizeCanonicalResult(completion.Result)
	if err != nil {
		return OperationCompletion{}, "", err
	}
	completion.Result = result
	return completion, resultJSON, nil
}

func operationMatchesClaim(receipt *OperationReceipt, claim OperationClaim) bool {
	return receipt.RunKind == claim.RunKind && receipt.RunID == claim.RunID &&
		receipt.IdempotencyKey == claim.IdempotencyKey && receipt.StepID == claim.StepID &&
		receipt.ActionID == claim.ActionID && receipt.InputDigest == claim.InputDigest &&
		receipt.ReviewDigest == claim.ReviewDigest
}

func operationMatchesCompletion(receipt *OperationReceipt, completion OperationCompletion) bool {
	return receipt.Status == completion.Status && receipt.ResultCode == completion.ResultCode &&
		receipt.ReasonCode == completion.ReasonCode && reflect.DeepEqual(receipt.Result, completion.Result)
}

func getRootWith(ctx context.Context, query queryRower, ownerUserID, relationshipID, specialistSlug, journeyID string) (*Run, error) {
	return scanRunRow(query.QueryRowContext(ctx, `
		SELECT `+runColumns+` FROM setup_journey_run
		WHERE run_kind = 'root' AND owner_user_id = ? AND relationship_id = ?
			AND specialist_slug = ? AND journey_id = ?
	`, ownerUserID, relationshipID, specialistSlug, journeyID))
}

func getRunWith(ctx context.Context, query queryRower, runID string) (*Run, error) {
	return scanRunRow(query.QueryRowContext(ctx, `
		SELECT `+runColumns+` FROM setup_journey_run WHERE id = ?
	`, runID))
}

func getUnboundChildWith(ctx context.Context, query queryRower, rootRunID string) (*Run, error) {
	return scanRunRow(query.QueryRowContext(ctx, `
		SELECT `+runColumns+` FROM setup_journey_run
		WHERE run_kind = 'child' AND root_run_id = ? AND project_workspace_id = ''
			AND lifecycle_state != 'ready'
		ORDER BY created_at ASC, id ASC LIMIT 1
	`, rootRunID))
}

func scanRunRow(row rowScanner) (*Run, error) {
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return run, err
}

func scanRun(row rowScanner) (*Run, error) {
	var run Run
	var rootRunID sql.NullString
	var lifecycle, stepJSON string
	var dismissed int
	var firstOpened, lastDismissed, firstCompleted sql.NullTime
	if err := row.Scan(
		&run.ID, &run.Kind, &rootRunID, &run.OwnerUserID, &run.RelationshipID,
		&run.SpecialistSlug, &run.JourneyID, &run.DeclarationSchemaVersion, &run.DeclarationVersion,
		&run.StateRevision, &lifecycle, &run.CurrentStepID, &stepJSON, &dismissed,
		&run.IntegrationPluginID, &run.IntegrationVersion, &run.HomeWorkspaceID,
		&run.ProjectWorkspaceID, &run.SelectedModeID, &firstOpened, &lastDismissed,
		&firstCompleted, &run.CreatedAt, &run.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if rootRunID.Valid {
		run.RootRunID = rootRunID.String
	}
	if firstOpened.Valid {
		run.FirstOpenedAt = cloneTime(&firstOpened.Time)
	}
	if lastDismissed.Valid {
		run.LastDismissedAt = cloneTime(&lastDismissed.Time)
	}
	if firstCompleted.Valid {
		run.FirstCompletedAt = cloneTime(&firstCompleted.Time)
	}
	run.CreatedAt = run.CreatedAt.UTC()
	run.UpdatedAt = run.UpdatedAt.UTC()

	if !validRunID(run.ID) || !validateRunKind(run.Kind) || run.StateRevision <= 0 ||
		!validateStableID(run.JourneyID) || run.CreatedAt.IsZero() || run.UpdatedAt.IsZero() {
		return nil, ErrMalformed
	}
	if run.Kind == RunKindRoot {
		if run.RootRunID != "" || !validateCanonicalRef(run.OwnerUserID, false) ||
			!validateCanonicalRef(run.RelationshipID, false) || !validateStableID(run.SpecialistSlug) {
			return nil, ErrMalformed
		}
	} else if !validRunID(run.RootRunID) || run.OwnerUserID != "" || run.RelationshipID != "" || run.SpecialistSlug != "" {
		return nil, ErrMalformed
	}

	if validateLifecycle(LifecycleState(lifecycle)) {
		run.Lifecycle = LifecycleState(lifecycle)
	} else {
		run.Lifecycle = LifecycleNeedsAttention
		run.NeedsNormalization = true
	}
	if dismissed == 0 || dismissed == 1 {
		run.Dismissed = dismissed == 1
	} else {
		run.NeedsNormalization = true
	}
	if run.DeclarationSchemaVersion <= 0 || run.DeclarationVersion <= 0 {
		run.NeedsNormalization = true
		run.Lifecycle = LifecycleNeedsAttention
	}
	if run.CurrentStepID != "" && !validateStableID(run.CurrentStepID) {
		run.CurrentStepID = ""
		run.NeedsNormalization = true
	}
	for _, ref := range []*string{
		&run.IntegrationPluginID, &run.IntegrationVersion, &run.HomeWorkspaceID,
		&run.ProjectWorkspaceID, &run.SelectedModeID,
	} {
		if !validateCanonicalRef(*ref, true) {
			*ref = ""
			run.NeedsNormalization = true
		}
	}
	if run.Kind == RunKindChild && (run.IntegrationPluginID != "" || run.IntegrationVersion != "" || run.HomeWorkspaceID != "") {
		run.IntegrationPluginID = ""
		run.IntegrationVersion = ""
		run.HomeWorkspaceID = ""
		run.NeedsNormalization = true
	}
	if (run.Lifecycle == LifecycleReady && (run.CurrentStepID != "" || run.FirstCompletedAt == nil)) ||
		(run.Kind == RunKindChild && run.ProjectWorkspaceID == "" && run.Lifecycle == LifecycleReady) ||
		(run.Lifecycle == LifecycleNotStarted && (run.FirstOpenedAt != nil || run.FirstCompletedAt != nil)) ||
		(run.Dismissed && run.LastDismissedAt == nil) {
		run.NeedsNormalization = true
	}
	states, valid := decodePersistedStepStates(stepJSON)
	if !valid {
		run.StepStates = nil
		run.CurrentStepID = ""
		run.NeedsNormalization = true
	} else {
		run.StepStates = states
		if run.CurrentStepID != "" && !containsStep(states, run.CurrentStepID) {
			run.CurrentStepID = ""
			run.NeedsNormalization = true
		}
	}
	if run.NeedsNormalization {
		run.Lifecycle = LifecycleNeedsAttention
	}
	return run.Clone(), nil
}

func hasBusyOperation(ctx context.Context, query queryRower, kind RunKind, runID string) (bool, error) {
	var count int
	if err := query.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM setup_journey_operation_receipt
		WHERE run_kind = ? AND run_id = ? AND status IN ('claimed', 'reconcile_required')
	`, kind, runID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func getReviewWith(ctx context.Context, query queryRower, token string) (*ReviewReceipt, error) {
	return scanReviewRow(query.QueryRowContext(ctx, `
		SELECT `+reviewColumns+` FROM setup_journey_review_receipt WHERE token = ?
	`, token))
}

func getReviewByIdempotencyWith(ctx context.Context, query queryRower, kind RunKind, runID, idempotencyKey string) (*ReviewReceipt, error) {
	return scanReviewRow(query.QueryRowContext(ctx, `
		SELECT `+reviewColumns+` FROM setup_journey_review_receipt
		WHERE run_kind = ? AND run_id = ? AND idempotency_key = ?
	`, kind, runID, idempotencyKey))
}

func scanReviewRow(row rowScanner) (*ReviewReceipt, error) {
	var receipt ReviewReceipt
	var consumedAt sql.NullTime
	if err := row.Scan(
		&receipt.Token, &receipt.RunKind, &receipt.RunID, &receipt.IdempotencyKey,
		&receipt.StepID, &receipt.ActionID, &receipt.InputDigest, &receipt.RunRevision,
		&receipt.OwnerRevisionDigest, &receipt.DisclosureDigest, &receipt.CreatedAt,
		&receipt.ExpiresAt, &consumedAt, &receipt.ConsumedByKey,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if consumedAt.Valid {
		receipt.ConsumedAt = cloneTime(&consumedAt.Time)
	}
	receipt.CreatedAt = receipt.CreatedAt.UTC()
	receipt.ExpiresAt = receipt.ExpiresAt.UTC()
	if !validRunID(receipt.Token) || !validateRunKind(receipt.RunKind) ||
		!validRunID(receipt.RunID) || !validateCanonicalRef(receipt.IdempotencyKey, false) ||
		!validateStableID(receipt.StepID) || !validateStableID(receipt.ActionID) ||
		!validateDigest(receipt.InputDigest, false) || receipt.RunRevision <= 0 ||
		!validateDigest(receipt.OwnerRevisionDigest, false) ||
		!validateDigest(receipt.DisclosureDigest, false) || receipt.CreatedAt.IsZero() ||
		!receipt.ExpiresAt.After(receipt.CreatedAt) ||
		(receipt.ConsumedAt == nil && receipt.ConsumedByKey != "") ||
		(receipt.ConsumedAt != nil && !validateCanonicalRef(receipt.ConsumedByKey, false)) {
		return nil, ErrMalformed
	}
	return receipt.Clone(), nil
}

func getOperationWith(ctx context.Context, query queryRower, kind RunKind, runID, idempotencyKey string) (*OperationReceipt, error) {
	return scanOperationRow(query.QueryRowContext(ctx, `
		SELECT `+operationColumns+` FROM setup_journey_operation_receipt
		WHERE run_kind = ? AND run_id = ? AND idempotency_key = ?
	`, kind, runID, idempotencyKey))
}

func scanOperationRow(row rowScanner) (*OperationReceipt, error) {
	var receipt OperationReceipt
	var status, resultCode, reasonCode, resultJSON string
	var completedAt sql.NullTime
	if err := row.Scan(
		&receipt.RunKind, &receipt.RunID, &receipt.IdempotencyKey, &receipt.StepID,
		&receipt.ActionID, &receipt.InputDigest, &receipt.ReviewDigest, &status,
		&resultCode, &reasonCode, &resultJSON, &receipt.RunRevisionBefore,
		&receipt.RunRevisionAfter, &receipt.CreatedAt, &completedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	receipt.Status = OperationStatus(status)
	receipt.ResultCode = ResultCode(resultCode)
	receipt.ReasonCode = ReasonCode(reasonCode)
	if completedAt.Valid {
		receipt.CompletedAt = cloneTime(&completedAt.Time)
	}
	receipt.CreatedAt = receipt.CreatedAt.UTC()
	result, valid := decodeCanonicalResult(resultJSON)
	if !valid || !validateRunKind(receipt.RunKind) || !validRunID(receipt.RunID) ||
		!validateCanonicalRef(receipt.IdempotencyKey, false) || !validateStableID(receipt.StepID) ||
		!validateStableID(receipt.ActionID) || !validateDigest(receipt.InputDigest, false) ||
		!validateDigest(receipt.ReviewDigest, true) || receipt.RunRevisionBefore <= 0 ||
		receipt.RunRevisionAfter <= receipt.RunRevisionBefore {
		return nil, ErrMalformed
	}
	switch receipt.Status {
	case OperationClaimed, OperationReconcileRequired:
		if receipt.ResultCode != "" || receipt.ReasonCode != "" || receipt.CompletedAt != nil {
			return nil, ErrMalformed
		}
	case OperationSucceeded:
		if !validateResultCode(receipt.ResultCode) || receipt.ReasonCode != "" || receipt.CompletedAt == nil {
			return nil, ErrMalformed
		}
	case OperationFailed:
		if !validateResultCode(receipt.ResultCode) || !validateReasonCode(receipt.ReasonCode, false) || receipt.CompletedAt == nil {
			return nil, ErrMalformed
		}
	default:
		return nil, ErrMalformed
	}
	receipt.Result = result
	return receipt.Clone(), nil
}

func validRunID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == strings.ToLower(value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "constraint failed")
}
