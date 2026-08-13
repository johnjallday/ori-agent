package workspaceplan

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// SQLiteStore is the durable Store. It is the implementation that has to
// survive restart, competing browser sessions, and concurrent approval
// attempts, so the invariants that matter are enforced in SQL rather than in
// Go: optimistic draft concurrency is a conditional UPDATE, one-shot approval
// consumption is a conditional UPDATE, and duplicate Task linkage is refused by
// a unique index (see migration 39).
type SQLiteStore struct {
	db *database.DB
}

// NewSQLiteStore returns a Plan store backed by the application database.
func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

var _ Store = (*SQLiteStore)(nil)

func (s *SQLiteStore) CreatePlan(ctx context.Context, plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("%w: plan is nil", ErrValidation)
	}
	if plan.ID == "" || plan.WorkspaceID == "" {
		return fmt.Errorf("%w: plan requires an ID and an owning workspace", ErrValidation)
	}

	draft := plan.Draft.Clone()
	questions := draft.Clarifications
	// Clarifications are stored in their own table so a later draft write
	// cannot carry an authored answer with it (FR-25).
	draft.Clarifications = nil

	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_plans (
				id, workspace_id, title, original_request, objective, status,
				draft_json, draft_revision, draft_intent, current_version, approved_version,
				superseded_by_plan_id, supersedes_plan_id, origin_json,
				created_at, updated_at, last_activity_at, archived_at, archive_reason
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			plan.ID, plan.WorkspaceID, plan.Title, plan.OriginalRequest, plan.Objective, string(plan.Status),
			mustJSON(draft), plan.DraftRevision, string(plan.DraftIntent), plan.CurrentVersion, plan.ApprovedVersion,
			plan.SupersededByPlanID, plan.SupersedesPlanID, mustJSON(plan.Origin),
			plan.CreatedAt, plan.UpdatedAt, plan.LastActivityAt, nullableTime(plan.ArchivedAt), plan.ArchiveReason)
		if err != nil {
			if isUniqueConstraint(err) {
				return fmt.Errorf("%w: %s", ErrPlanExists, plan.ID)
			}
			// The owning-workspace foreign key is what makes "every Plan
			// belongs to exactly one workspace" true in storage rather than by
			// convention (FR-2). Naming the cause beats a generic 500.
			if isForeignKeyConstraint(err) {
				return fmt.Errorf("%w: %s", ErrWorkspaceNotFound, plan.WorkspaceID)
			}
			return fmt.Errorf("create workspace plan: %w", err)
		}
		return insertClarifications(ctx, tx, plan.ID, plan.WorkspaceID, questions)
	})
}

func (s *SQLiteStore) GetPlan(ctx context.Context, workspaceID, planID string) (*Plan, error) {
	plan, err := s.scanPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if plan.Draft.Clarifications, err = s.listClarifications(ctx, workspaceID, planID); err != nil {
		return nil, err
	}
	if plan.TaskLinks, err = s.listTaskLinks(ctx, workspaceID, planID); err != nil {
		return nil, err
	}
	if plan.RunLinks, err = s.listRunLinks(ctx, workspaceID, planID); err != nil {
		return nil, err
	}
	return plan, nil
}

const planColumns = `
	id, workspace_id, title, original_request, objective, status,
	draft_json, draft_revision, draft_intent, current_version, approved_version,
	superseded_by_plan_id, supersedes_plan_id, origin_json,
	created_at, updated_at, last_activity_at, archived_at, archive_reason`

func (s *SQLiteStore) scanPlan(ctx context.Context, workspaceID, planID string) (*Plan, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+planColumns+`
		FROM workspace_plans
		WHERE workspace_id = ? AND id = ?
	`, workspaceID, planID)

	plan, err := scanPlanRow(row)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// rowScanner covers both *sql.Row and *sql.Rows so one scan path serves the
// single-record read and the list.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPlanRow(row rowScanner) (*Plan, error) {
	var (
		plan       Plan
		status     string
		draftJSON  string
		intent     string
		originJSON string
		archivedAt sql.NullTime
	)
	err := row.Scan(
		&plan.ID, &plan.WorkspaceID, &plan.Title, &plan.OriginalRequest, &plan.Objective, &status,
		&draftJSON, &plan.DraftRevision, &intent, &plan.CurrentVersion, &plan.ApprovedVersion,
		&plan.SupersededByPlanID, &plan.SupersedesPlanID, &originJSON,
		&plan.CreatedAt, &plan.UpdatedAt, &plan.LastActivityAt, &archivedAt, &plan.ArchiveReason)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrPlanNotFound
		}
		return nil, fmt.Errorf("scan workspace plan: %w", err)
	}
	plan.Status = Status(status)
	plan.DraftIntent = RevisionIntent(intent)
	plan.ArchivedAt = nullTimePtr(archivedAt)
	if err := decodeJSON(draftJSON, &plan.Draft); err != nil {
		return nil, err
	}
	if err := decodeJSON(originJSON, &plan.Origin); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (s *SQLiteStore) ListPlans(ctx context.Context, workspaceID string, filter ListFilter) ([]*Plan, error) {
	filter = filter.Normalized()

	query := `SELECT ` + planColumns + ` FROM workspace_plans WHERE workspace_id = ?`
	args := []any{workspaceID}
	switch filter.Scope {
	case ScopeActive:
		query += ` AND archived_at IS NULL`
	case ScopeHistory:
		query += ` AND archived_at IS NOT NULL`
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, status := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, string(status))
		}
		query += ` AND status IN (` + strings.Join(placeholders, ", ") + `)`
	}
	query += ` ORDER BY last_activity_at DESC, id ASC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list workspace plans: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var plans []*Plan
	for rows.Next() {
		plan, err := scanPlanRow(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace plans: %w", err)
	}

	// Hydrate after the cursor is drained: SQLite serializes reads on one
	// connection, so querying per row inside the loop would deadlock.
	for _, plan := range plans {
		if plan.Draft.Clarifications, err = s.listClarifications(ctx, workspaceID, plan.ID); err != nil {
			return nil, err
		}
		if plan.TaskLinks, err = s.listTaskLinks(ctx, workspaceID, plan.ID); err != nil {
			return nil, err
		}
		if plan.RunLinks, err = s.listRunLinks(ctx, workspaceID, plan.ID); err != nil {
			return nil, err
		}
	}
	return plans, nil
}

func (s *SQLiteStore) UpdatePlanDraft(ctx context.Context, workspaceID, planID string, expectedRevision int64, draft DraftUpdate) (int64, error) {
	content := draft.Content.Clone()
	content.Clarifications = nil

	// The revision predicate is the concurrency control: a second session
	// writing against a revision that has already moved matches zero rows and
	// is told its view is stale rather than silently winning (FR-30).
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_plans
		SET title = ?, objective = ?, draft_json = ?, draft_intent = ?,
			draft_revision = draft_revision + 1, updated_at = ?, last_activity_at = ?
		WHERE workspace_id = ? AND id = ? AND draft_revision = ?
	`, draft.Title, draft.Objective, mustJSON(content), string(draft.Intent),
		draft.UpdatedAt, draft.UpdatedAt, workspaceID, planID, expectedRevision)
	if err != nil {
		return 0, fmt.Errorf("update workspace plan draft: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("update workspace plan draft: %w", err)
	}
	if affected == 0 {
		return 0, s.explainFailedDraftWrite(ctx, workspaceID, planID, expectedRevision)
	}

	var revision int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT draft_revision FROM workspace_plans WHERE workspace_id = ? AND id = ?
	`, workspaceID, planID).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read workspace plan revision: %w", err)
	}
	return revision, nil
}

// explainFailedDraftWrite distinguishes "no such Plan here" from "someone else
// saved first", so the caller can offer recovery rather than a generic error.
func (s *SQLiteStore) explainFailedDraftWrite(ctx context.Context, workspaceID, planID string, expectedRevision int64) error {
	var current int64
	err := s.db.QueryRowContext(ctx, `
		SELECT draft_revision FROM workspace_plans WHERE workspace_id = ? AND id = ?
	`, workspaceID, planID).Scan(&current)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	if err != nil {
		return fmt.Errorf("read workspace plan revision: %w", err)
	}
	return fmt.Errorf("%w: plan is at revision %d, write carried %d", ErrStaleDraft, current, expectedRevision)
}

func (s *SQLiteStore) SetPlanStatus(ctx context.Context, workspaceID, planID string, to Status, activity Activity) error {
	// The status write and its history entry share one transaction, so a
	// status can never move without leaving a record (FR-14, FR-15).
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE workspace_plans
			SET status = ?, updated_at = ?, last_activity_at = ?
			WHERE workspace_id = ? AND id = ?
		`, string(to), activity.CreatedAt, activity.CreatedAt, workspaceID, planID)
		if err != nil {
			return fmt.Errorf("update workspace plan status: %w", err)
		}
		if err := database.CheckRowsAffectedWithError(result, "workspace_plan", ErrPlanNotFound); err != nil {
			return err
		}
		_, err = appendActivityTx(ctx, tx, activity)
		return err
	})
}

func (s *SQLiteStore) ArchivePlan(ctx context.Context, workspaceID, planID, reason string, at time.Time) error {
	// Archiving only sets the History marker. Versions, approvals, Task links,
	// Run links, and activity are untouched (FR-16).
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_plans
		SET archived_at = ?, archive_reason = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, at, reason, at, workspaceID, planID)
	if err != nil {
		return fmt.Errorf("archive workspace plan: %w", err)
	}
	return database.CheckRowsAffectedWithError(result, "workspace_plan", ErrPlanNotFound)
}

func (s *SQLiteStore) ReopenPlan(ctx context.Context, workspaceID, planID string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_plans
		SET archived_at = NULL, archive_reason = '', updated_at = ?, last_activity_at = ?
		WHERE workspace_id = ? AND id = ?
	`, now, now, workspaceID, planID)
	if err != nil {
		return fmt.Errorf("reopen workspace plan: %w", err)
	}
	return database.CheckRowsAffectedWithError(result, "workspace_plan", ErrPlanNotFound)
}

func (s *SQLiteStore) DeletePlan(ctx context.Context, workspaceID, planID string) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var approvedVersion int
		err := tx.QueryRowContext(ctx, `
			SELECT approved_version FROM workspace_plans WHERE workspace_id = ? AND id = ?
		`, workspaceID, planID).Scan(&approvedVersion)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
		}
		if err != nil {
			return fmt.Errorf("read workspace plan for deletion: %w", err)
		}
		if approvedVersion > 0 {
			return fmt.Errorf("%w: plan has an approved version", ErrPlanNotDeletable)
		}

		// Counted inside the transaction so a Task cannot be linked between the
		// check and the delete (FR-17).
		for _, check := range []struct {
			table string
			label string
		}{
			{"workspace_plan_approvals", "approval records"},
			{"workspace_plan_task_links", "linked tasks"},
			{"workspace_plan_run_links", "linked runs"},
		} {
			var count int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(1) FROM `+check.table+` WHERE plan_id = ?`, planID).Scan(&count); err != nil {
				return fmt.Errorf("count %s: %w", check.label, err)
			}
			if count > 0 {
				return fmt.Errorf("%w: plan has %d %s", ErrPlanNotDeletable, count, check.label)
			}
		}

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM workspace_plans WHERE workspace_id = ? AND id = ?`, workspaceID, planID); err != nil {
			return fmt.Errorf("delete workspace plan: %w", err)
		}
		return nil
	})
}

func (s *SQLiteStore) PutClarifications(ctx context.Context, workspaceID, planID string, questions []Clarification) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := requirePlanTx(ctx, tx, workspaceID, planID); err != nil {
			return err
		}
		// The answer columns are deliberately absent from the UPDATE arm: a
		// regenerated question set may reword a prompt, but the user's answer
		// is theirs to change (FR-25).
		return insertClarifications(ctx, tx, planID, workspaceID, questions)
	})
}

func insertClarifications(ctx context.Context, tx *sql.Tx, planID, workspaceID string, questions []Clarification) error {
	for ordinal, question := range questions {
		status := question.Status
		if status == "" {
			status = ClarificationOpen
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_plan_clarifications (
				id, plan_id, workspace_id, prompt, detail, options_json,
				required, status, round, ordinal, answer, answered_by, answered_at,
				skip_reason, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				prompt = excluded.prompt,
				detail = excluded.detail,
				options_json = excluded.options_json,
				required = excluded.required,
				round = excluded.round,
				ordinal = excluded.ordinal
		`,
			question.ID, planID, workspaceID, question.Prompt, question.Detail, mustJSON(question.Options),
			boolToInt(question.Required), string(status), question.Round, ordinal,
			question.Answer, question.AnsweredBy, nullableTime(question.AnsweredAt),
			question.SkipReason, question.CreatedAt)
		if err != nil {
			return fmt.Errorf("upsert plan clarification: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) listClarifications(ctx context.Context, workspaceID, planID string) ([]Clarification, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, prompt, detail, options_json, required, status, round,
			answer, answered_by, answered_at, skip_reason, created_at
		FROM workspace_plan_clarifications
		WHERE workspace_id = ? AND plan_id = ?
		ORDER BY round ASC, ordinal ASC
	`, workspaceID, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan clarifications: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var questions []Clarification
	for rows.Next() {
		var (
			question    Clarification
			optionsJSON string
			required    int
			status      string
			answeredAt  sql.NullTime
		)
		if err := rows.Scan(&question.ID, &question.Prompt, &question.Detail, &optionsJSON,
			&required, &status, &question.Round, &question.Answer, &question.AnsweredBy,
			&answeredAt, &question.SkipReason, &question.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan plan clarification: %w", err)
		}
		question.Required = required != 0
		question.Status = ClarificationStatus(status)
		question.AnsweredAt = nullTimePtr(answeredAt)
		if err := decodeJSON(optionsJSON, &question.Options); err != nil {
			return nil, err
		}
		questions = append(questions, question)
	}
	return questions, rows.Err()
}

func (s *SQLiteStore) AnswerClarification(ctx context.Context, workspaceID, planID, clarificationID string, answer ClarificationAnswer) error {
	status := ClarificationSkipped
	answerText := ""
	skipReason := answer.SkipReason
	if answer.Answered {
		status = ClarificationAnswered
		answerText = answer.Answer
		skipReason = ""
	}

	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE workspace_plan_clarifications
			SET status = ?, answer = ?, answered_by = ?, answered_at = ?, skip_reason = ?
			WHERE workspace_id = ? AND plan_id = ? AND id = ?
		`, string(status), answerText, answer.AnsweredBy, answer.At, skipReason,
			workspaceID, planID, clarificationID)
		if err != nil {
			return fmt.Errorf("answer plan clarification: %w", err)
		}
		if err := database.CheckRowsAffectedWithError(result, "workspace_plan_clarification", ErrPlanNotFound); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE workspace_plans SET updated_at = ?, last_activity_at = ?
			WHERE workspace_id = ? AND id = ?
		`, answer.At, answer.At, workspaceID, planID)
		if err != nil {
			return fmt.Errorf("touch workspace plan: %w", err)
		}
		return nil
	})
}

func (s *SQLiteStore) CreateVersion(ctx context.Context, version *Version) (*Version, error) {
	if version == nil {
		return nil, fmt.Errorf("%w: version is nil", ErrValidation)
	}
	stored := version.Clone()
	if stored.Status == "" {
		stored.Status = VersionInReview
	}

	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := requirePlanTx(ctx, tx, stored.WorkspaceID, stored.PlanID); err != nil {
			return err
		}
		// The number is assigned inside the transaction so two concurrent
		// review requests cannot claim the same one (FR-31).
		var highest sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(version) FROM workspace_plan_versions WHERE plan_id = ?`, stored.PlanID).Scan(&highest); err != nil {
			return fmt.Errorf("read highest plan version: %w", err)
		}
		stored.Number = int(highest.Int64) + 1

		_, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_plan_versions (
				plan_id, version, workspace_id, title, objective, content_json, content_hash,
				policy_snapshot_json, intent, status, created_at, created_by_json,
				decided_at, decided_by, decision_reason
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			stored.PlanID, stored.Number, stored.WorkspaceID, stored.Title, stored.Objective,
			mustJSON(stored.Content), stored.ContentHash, mustJSON(stored.PolicySnapshot),
			string(stored.Intent), string(stored.Status), stored.CreatedAt, mustJSON(stored.CreatedBy),
			nullableTime(stored.DecidedAt), stored.DecidedBy, stored.DecisionReason)
		if err != nil {
			return fmt.Errorf("create plan version: %w", err)
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE workspace_plans SET current_version = ?, updated_at = ?, last_activity_at = ?
			WHERE workspace_id = ? AND id = ?
		`, stored.Number, stored.CreatedAt, stored.CreatedAt, stored.WorkspaceID, stored.PlanID)
		if err != nil {
			return fmt.Errorf("update plan current version: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stored, nil
}

const versionColumns = `
	plan_id, version, workspace_id, title, objective, content_json, content_hash,
	policy_snapshot_json, intent, status, created_at, created_by_json,
	decided_at, decided_by, decision_reason`

func scanVersionRow(row rowScanner) (*Version, error) {
	var (
		version       Version
		contentJSON   string
		policyJSON    string
		intent        string
		status        string
		createdByJSON string
		decidedAt     sql.NullTime
	)
	err := row.Scan(&version.PlanID, &version.Number, &version.WorkspaceID, &version.Title, &version.Objective,
		&contentJSON, &version.ContentHash, &policyJSON, &intent, &status, &version.CreatedAt,
		&createdByJSON, &decidedAt, &version.DecidedBy, &version.DecisionReason)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrVersionNotFound
		}
		return nil, fmt.Errorf("scan plan version: %w", err)
	}
	version.Intent = RevisionIntent(intent)
	version.Status = VersionStatus(status)
	version.DecidedAt = nullTimePtr(decidedAt)
	if err := decodeJSON(contentJSON, &version.Content); err != nil {
		return nil, err
	}
	if err := decodeJSON(policyJSON, &version.PolicySnapshot); err != nil {
		return nil, err
	}
	if err := decodeJSON(createdByJSON, &version.CreatedBy); err != nil {
		return nil, err
	}
	return &version, nil
}

func (s *SQLiteStore) GetVersion(ctx context.Context, workspaceID, planID string, number int) (*Version, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+versionColumns+`
		FROM workspace_plan_versions
		WHERE workspace_id = ? AND plan_id = ? AND version = ?
	`, workspaceID, planID, number)
	return scanVersionRow(row)
}

func (s *SQLiteStore) ListVersions(ctx context.Context, workspaceID, planID string) ([]*Version, error) {
	// Every list is gated on the Plan existing in this workspace. Without the
	// check a cross-workspace or unknown ID reads as an empty list, which tells
	// the caller "this Plan has no versions" instead of "there is no such Plan
	// here" — two different answers, and only one of them is true (FR-163,
	// FR-167).
	if err := s.requirePlan(ctx, workspaceID, planID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+versionColumns+`
		FROM workspace_plan_versions
		WHERE workspace_id = ? AND plan_id = ?
		ORDER BY version ASC
	`, workspaceID, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var versions []*Version
	for rows.Next() {
		version, err := scanVersionRow(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *SQLiteStore) SetVersionDecision(ctx context.Context, workspaceID, planID string, number int, status VersionStatus, decidedBy, reason string, at time.Time) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		// Only the decision columns are written. Content and hash are never in
		// this statement, which is what keeps an approved snapshot exactly what
		// was reviewed (FR-31, FR-32).
		result, err := tx.ExecContext(ctx, `
			UPDATE workspace_plan_versions
			SET status = ?, decided_at = ?, decided_by = ?, decision_reason = ?
			WHERE workspace_id = ? AND plan_id = ? AND version = ?
		`, string(status), at, decidedBy, reason, workspaceID, planID, number)
		if err != nil {
			return fmt.Errorf("set plan version decision: %w", err)
		}
		if err := database.CheckRowsAffectedWithError(result, "workspace_plan_version", ErrVersionNotFound); err != nil {
			return err
		}
		if status == VersionApproved {
			if _, err := tx.ExecContext(ctx, `
				UPDATE workspace_plans SET approved_version = ?, updated_at = ?, last_activity_at = ?
				WHERE workspace_id = ? AND id = ?
			`, number, at, at, workspaceID, planID); err != nil {
				return fmt.Errorf("update plan approved version: %w", err)
			}
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE workspace_plans SET updated_at = ?, last_activity_at = ?
			WHERE workspace_id = ? AND id = ?
		`, at, at, workspaceID, planID); err != nil {
			return fmt.Errorf("touch workspace plan: %w", err)
		}
		return nil
	})
}

const approvalColumns = `
	id, plan_id, workspace_id, version, content_hash, effect, user_id, user_name,
	idempotency_key, created_at, consumed_at, consumed_result_json, invalidated_at, invalidated_reason`

func scanApprovalRow(row rowScanner) (*Approval, error) {
	var (
		approval      Approval
		effect        string
		consumedAt    sql.NullTime
		resultJSON    sql.NullString
		invalidatedAt sql.NullTime
	)
	err := row.Scan(&approval.ID, &approval.PlanID, &approval.WorkspaceID, &approval.Version,
		&approval.ContentHash, &effect, &approval.UserID, &approval.UserName,
		&approval.IdempotencyKey, &approval.CreatedAt, &consumedAt, &resultJSON,
		&invalidatedAt, &approval.InvalidatedReason)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrApprovalNotFound
		}
		return nil, fmt.Errorf("scan plan approval: %w", err)
	}
	approval.Effect = ApprovalEffect(effect)
	approval.ConsumedAt = nullTimePtr(consumedAt)
	approval.InvalidatedAt = nullTimePtr(invalidatedAt)
	if resultJSON.Valid && resultJSON.String != "" {
		var result ApprovalResult
		if err := decodeJSON(resultJSON.String, &result); err != nil {
			return nil, err
		}
		approval.ConsumedResult = &result
	}
	return &approval, nil
}

func (s *SQLiteStore) CreateApproval(ctx context.Context, approval *Approval) (*Approval, error) {
	if approval == nil {
		return nil, fmt.Errorf("%w: approval is nil", ErrValidation)
	}
	stored := approval.Clone()
	if stored.ID == "" {
		stored.ID = NewApprovalID()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspace_plan_approvals (
			id, plan_id, workspace_id, version, content_hash, effect,
			user_id, user_name, idempotency_key, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, stored.ID, stored.PlanID, stored.WorkspaceID, stored.Version, stored.ContentHash,
		string(stored.Effect), stored.UserID, stored.UserName, stored.IdempotencyKey, stored.CreatedAt)
	if err == nil {
		return stored, nil
	}
	if !isUniqueConstraint(err) {
		return nil, fmt.Errorf("create plan approval: %w", err)
	}

	// The unique index on (plan_id, idempotency_key) turned a retried request
	// into a conflict; return what the first request produced rather than a
	// second authorization (FR-73).
	row := s.db.QueryRowContext(ctx, `
		SELECT `+approvalColumns+`
		FROM workspace_plan_approvals
		WHERE workspace_id = ? AND plan_id = ? AND idempotency_key = ?
	`, stored.WorkspaceID, stored.PlanID, stored.IdempotencyKey)
	return scanApprovalRow(row)
}

func (s *SQLiteStore) GetApproval(ctx context.Context, workspaceID, planID, approvalID string) (*Approval, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+approvalColumns+`
		FROM workspace_plan_approvals
		WHERE workspace_id = ? AND plan_id = ? AND id = ?
	`, workspaceID, planID, approvalID)
	return scanApprovalRow(row)
}

func (s *SQLiteStore) ListApprovals(ctx context.Context, workspaceID, planID string) ([]*Approval, error) {
	if err := s.requirePlan(ctx, workspaceID, planID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+approvalColumns+`
		FROM workspace_plan_approvals
		WHERE workspace_id = ? AND plan_id = ?
		ORDER BY created_at DESC, id DESC
	`, workspaceID, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan approvals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var approvals []*Approval
	for rows.Next() {
		approval, err := scanApprovalRow(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	return approvals, rows.Err()
}

func (s *SQLiteStore) ConsumeApproval(ctx context.Context, workspaceID, planID, approvalID string, result ApprovalResult, at time.Time) error {
	// consumed_at IS NULL in the predicate is the whole guarantee: two racing
	// materializations both issue this statement, exactly one matches a row,
	// and the loser is told the approval is already spent (FR-72, FR-178).
	sqlResult, err := s.db.ExecContext(ctx, `
		UPDATE workspace_plan_approvals
		SET consumed_at = ?, consumed_result_json = ?
		WHERE workspace_id = ? AND plan_id = ? AND id = ?
			AND consumed_at IS NULL AND invalidated_at IS NULL
	`, at, mustJSON(result), workspaceID, planID, approvalID)
	if err != nil {
		return fmt.Errorf("consume plan approval: %w", err)
	}
	affected, err := sqlResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("consume plan approval: %w", err)
	}
	if affected > 0 {
		return nil
	}

	existing, err := s.GetApproval(ctx, workspaceID, planID, approvalID)
	if err != nil {
		return err
	}
	if existing.Invalidated() {
		return fmt.Errorf("%w: approval was invalidated by a later edit", ErrApprovalMismatch)
	}
	return fmt.Errorf("%w: %s", ErrApprovalConsumed, approvalID)
}

func (s *SQLiteStore) InvalidateApprovals(ctx context.Context, workspaceID, planID string, version int, reason string, at time.Time) error {
	// A consumed approval is history and is never rewritten; only outstanding
	// attempts are invalidated (FR-68).
	_, err := s.db.ExecContext(ctx, `
		UPDATE workspace_plan_approvals
		SET invalidated_at = ?, invalidated_reason = ?
		WHERE workspace_id = ? AND plan_id = ? AND version = ?
			AND consumed_at IS NULL AND invalidated_at IS NULL
	`, at, reason, workspaceID, planID, version)
	if err != nil {
		return fmt.Errorf("invalidate plan approvals: %w", err)
	}
	return nil
}

func (s *SQLiteStore) LinkTasks(ctx context.Context, workspaceID, planID string, links []TaskLink) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := requirePlanTx(ctx, tx, workspaceID, planID); err != nil {
			return err
		}
		for _, link := range links {
			// DO NOTHING plus the partial unique index makes a retried or raced
			// materialization write nothing new rather than a second Task tree
			// (FR-91, FR-178).
			_, err := tx.ExecContext(ctx, `
				INSERT INTO workspace_plan_task_links (
					plan_id, task_id, workspace_id, version, approval_id,
					group_id, item_id, role, created_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING
			`, planID, link.TaskID, workspaceID, link.Version, link.ApprovalID,
				link.GroupID, link.ItemID, string(link.Role), link.CreatedAt)
			if err != nil {
				return fmt.Errorf("link plan task: %w", err)
			}
		}
		return nil
	})
}

func (s *SQLiteStore) LinkRun(ctx context.Context, workspaceID, planID string, link RunLink) error {
	if err := s.requirePlan(ctx, workspaceID, planID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspace_plan_run_links (
			plan_id, run_id, workspace_id, version, group_id, item_id, task_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`, planID, link.RunID, workspaceID, link.Version, link.GroupID, link.ItemID, link.TaskID, link.CreatedAt)
	if err != nil {
		return fmt.Errorf("link plan run: %w", err)
	}
	return nil
}

func (s *SQLiteStore) RetireTaskLink(ctx context.Context, workspaceID, planID, taskID, replacedByTaskID, reason string, at time.Time) error {
	// Retiring marks the link; it never deletes the row or the Task (FR-78).
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_plan_task_links
		SET retired_at = ?, retired_reason = ?, replaced_by_task_id = ?
		WHERE workspace_id = ? AND plan_id = ? AND task_id = ?
	`, at, reason, replacedByTaskID, workspaceID, planID, taskID)
	if err != nil {
		return fmt.Errorf("retire plan task link: %w", err)
	}
	return database.CheckRowsAffectedWithError(result, "workspace_plan_task_link", ErrPlanNotFound)
}

const taskLinkColumns = `
	plan_id, task_id, workspace_id, version, approval_id, group_id, item_id, role,
	created_at, replaced_by_task_id, retired_at, retired_reason`

func scanTaskLinkRow(row rowScanner) (*TaskLink, error) {
	var (
		link      TaskLink
		role      string
		retiredAt sql.NullTime
	)
	err := row.Scan(&link.PlanID, &link.TaskID, &link.WorkspaceID, &link.Version, &link.ApprovalID,
		&link.GroupID, &link.ItemID, &role, &link.CreatedAt, &link.ReplacedByTaskID,
		&retiredAt, &link.RetiredReason)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrPlanNotFound
		}
		return nil, fmt.Errorf("scan plan task link: %w", err)
	}
	link.Role = LinkRole(role)
	link.RetiredAt = nullTimePtr(retiredAt)
	return &link, nil
}

func (s *SQLiteStore) listTaskLinks(ctx context.Context, workspaceID, planID string) ([]TaskLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+taskLinkColumns+`
		FROM workspace_plan_task_links
		WHERE workspace_id = ? AND plan_id = ?
		ORDER BY created_at ASC, task_id ASC
	`, workspaceID, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan task links: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var links []TaskLink
	for rows.Next() {
		link, err := scanTaskLinkRow(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, *link)
	}
	return links, rows.Err()
}

const runLinkColumns = `plan_id, run_id, workspace_id, version, group_id, item_id, task_id, created_at`

func scanRunLinkRow(row rowScanner) (*RunLink, error) {
	var link RunLink
	err := row.Scan(&link.PlanID, &link.RunID, &link.WorkspaceID, &link.Version,
		&link.GroupID, &link.ItemID, &link.TaskID, &link.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrPlanNotFound
		}
		return nil, fmt.Errorf("scan plan run link: %w", err)
	}
	return &link, nil
}

func (s *SQLiteStore) listRunLinks(ctx context.Context, workspaceID, planID string) ([]RunLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+runLinkColumns+`
		FROM workspace_plan_run_links
		WHERE workspace_id = ? AND plan_id = ?
		ORDER BY created_at ASC, run_id ASC
	`, workspaceID, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan run links: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var links []RunLink
	for rows.Next() {
		link, err := scanRunLinkRow(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, *link)
	}
	return links, rows.Err()
}

func (s *SQLiteStore) PlanForTask(ctx context.Context, workspaceID, taskID string) (*TaskLink, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+taskLinkColumns+`
		FROM workspace_plan_task_links
		WHERE workspace_id = ? AND task_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, workspaceID, taskID)
	return scanTaskLinkRow(row)
}

func (s *SQLiteStore) PlanForRun(ctx context.Context, workspaceID, runID string) (*RunLink, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+runLinkColumns+`
		FROM workspace_plan_run_links
		WHERE workspace_id = ? AND run_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, workspaceID, runID)
	return scanRunLinkRow(row)
}

func (s *SQLiteStore) AppendActivity(ctx context.Context, activity Activity) (Activity, error) {
	var stored Activity
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		stored, err = appendActivityTx(ctx, tx, activity)
		return err
	})
	return stored, err
}

// appendActivityTx assigns the next sequence and writes the entry. It runs
// inside the caller's transaction so the sequence read and the insert cannot
// interleave with another writer.
func appendActivityTx(ctx context.Context, tx *sql.Tx, activity Activity) (Activity, error) {
	if activity.ID == "" {
		activity.ID = NewActivityID()
	}
	var highest sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(sequence) FROM workspace_plan_activity WHERE plan_id = ?`, activity.PlanID).Scan(&highest); err != nil {
		return Activity{}, fmt.Errorf("read plan activity sequence: %w", err)
	}
	activity.Sequence = highest.Int64 + 1

	_, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_plan_activity (
			id, plan_id, workspace_id, sequence, kind, from_status, to_status,
			source, actor, actor_id, reason, version, approval_id, task_id, run_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, activity.ID, activity.PlanID, activity.WorkspaceID, activity.Sequence, string(activity.Kind),
		string(activity.From), string(activity.To), string(activity.Source), activity.Actor,
		activity.ActorID, activity.Reason, activity.Version, activity.ApprovalID,
		activity.TaskID, activity.RunID, activity.CreatedAt)
	if err != nil {
		return Activity{}, fmt.Errorf("append plan activity: %w", err)
	}
	return activity, nil
}

func (s *SQLiteStore) ListActivity(ctx context.Context, workspaceID, planID string, limit int) ([]Activity, error) {
	if err := s.requirePlan(ctx, workspaceID, planID); err != nil {
		return nil, err
	}
	query := `
		SELECT id, plan_id, workspace_id, sequence, kind, from_status, to_status,
			source, actor, actor_id, reason, version, approval_id, task_id, run_id, created_at
		FROM workspace_plan_activity
		WHERE workspace_id = ? AND plan_id = ?
		ORDER BY sequence ASC`
	args := []any{workspaceID, planID}
	if limit > 0 {
		// The newest entries are the ones worth keeping when a limit applies,
		// so select from the tail and re-sort ascending below.
		query = `
			SELECT id, plan_id, workspace_id, sequence, kind, from_status, to_status,
				source, actor, actor_id, reason, version, approval_id, task_id, run_id, created_at
			FROM (
				SELECT * FROM workspace_plan_activity
				WHERE workspace_id = ? AND plan_id = ?
				ORDER BY sequence DESC
				LIMIT ?
			) ORDER BY sequence ASC`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list plan activity: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []Activity
	for rows.Next() {
		var (
			entry      Activity
			kind       string
			fromStatus string
			toStatus   string
			source     string
		)
		if err := rows.Scan(&entry.ID, &entry.PlanID, &entry.WorkspaceID, &entry.Sequence, &kind,
			&fromStatus, &toStatus, &source, &entry.Actor, &entry.ActorID, &entry.Reason,
			&entry.Version, &entry.ApprovalID, &entry.TaskID, &entry.RunID, &entry.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan plan activity: %w", err)
		}
		entry.Kind = ActivityKind(kind)
		entry.From = Status(fromStatus)
		entry.To = Status(toStatus)
		entry.Source = TransitionSource(source)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *SQLiteStore) PutDraftSnapshot(ctx context.Context, snapshot *DraftSnapshot, keep int) error {
	if snapshot == nil {
		return fmt.Errorf("%w: snapshot is nil", ErrValidation)
	}
	stored := snapshot.Clone()
	if stored.ID == "" {
		stored.ID = NewDraftSnapshotID()
	}

	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := requirePlanTx(ctx, tx, stored.WorkspaceID, stored.PlanID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_plan_draft_snapshots (
				id, plan_id, workspace_id, draft_revision, title, objective, content_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, stored.ID, stored.PlanID, stored.WorkspaceID, stored.DraftRevision,
			stored.Title, stored.Objective, mustJSON(stored.Content), stored.CreatedAt)
		if err != nil {
			return fmt.Errorf("create plan draft snapshot: %w", err)
		}
		_, err = pruneSnapshotsTx(ctx, tx, stored.WorkspaceID, stored.PlanID, keep, time.Time{})
		return err
	})
}

func (s *SQLiteStore) ListDraftSnapshots(ctx context.Context, workspaceID, planID string) ([]*DraftSnapshot, error) {
	if err := s.requirePlan(ctx, workspaceID, planID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, plan_id, workspace_id, draft_revision, title, objective, content_json, created_at
		FROM workspace_plan_draft_snapshots
		WHERE workspace_id = ? AND plan_id = ?
		ORDER BY created_at DESC, id DESC
	`, workspaceID, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan draft snapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var snapshots []*DraftSnapshot
	for rows.Next() {
		var (
			snapshot    DraftSnapshot
			contentJSON string
		)
		if err := rows.Scan(&snapshot.ID, &snapshot.PlanID, &snapshot.WorkspaceID, &snapshot.DraftRevision,
			&snapshot.Title, &snapshot.Objective, &contentJSON, &snapshot.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan plan draft snapshot: %w", err)
		}
		if err := decodeJSON(contentJSON, &snapshot.Content); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, &snapshot)
	}
	return snapshots, rows.Err()
}

func (s *SQLiteStore) PruneDraftSnapshots(ctx context.Context, workspaceID, planID string, keep int, olderThan time.Time) (int, error) {
	var removed int
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		removed, err = pruneSnapshotsTx(ctx, tx, workspaceID, planID, keep, olderThan)
		return err
	})
	return removed, err
}

// pruneSnapshotsTx drops recovery points beyond the newest keep and any older
// than the cutoff. It only ever touches the snapshot table, so pruning recovery
// points can never remove review history (FR-30, FR-31).
func pruneSnapshotsTx(ctx context.Context, tx *sql.Tx, workspaceID, planID string, keep int, olderThan time.Time) (int, error) {
	removed := 0
	if keep > 0 {
		result, err := tx.ExecContext(ctx, `
			DELETE FROM workspace_plan_draft_snapshots
			WHERE workspace_id = ? AND plan_id = ? AND id NOT IN (
				SELECT id FROM workspace_plan_draft_snapshots
				WHERE workspace_id = ? AND plan_id = ?
				ORDER BY created_at DESC, id DESC
				LIMIT ?
			)
		`, workspaceID, planID, workspaceID, planID, keep)
		if err != nil {
			return 0, fmt.Errorf("prune plan draft snapshots: %w", err)
		}
		if affected, err := result.RowsAffected(); err == nil {
			removed += int(affected)
		}
	}
	if !olderThan.IsZero() {
		result, err := tx.ExecContext(ctx, `
			DELETE FROM workspace_plan_draft_snapshots
			WHERE workspace_id = ? AND plan_id = ? AND created_at < ?
		`, workspaceID, planID, olderThan)
		if err != nil {
			return 0, fmt.Errorf("expire plan draft snapshots: %w", err)
		}
		if affected, err := result.RowsAffected(); err == nil {
			removed += int(affected)
		}
	}
	return removed, nil
}

// requirePlan and requirePlanTx turn "this Plan is not in this workspace" into
// ErrPlanNotFound before a dependent write happens, so a cross-workspace ID
// cannot attach rows to someone else's Plan (FR-163, FR-167).
func (s *SQLiteStore) requirePlan(ctx context.Context, workspaceID, planID string) error {
	var found string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM workspace_plans WHERE workspace_id = ? AND id = ?`, workspaceID, planID).Scan(&found)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	if err != nil {
		return fmt.Errorf("verify workspace plan: %w", err)
	}
	return nil
}

func requirePlanTx(ctx context.Context, tx *sql.Tx, workspaceID, planID string) error {
	var found string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM workspace_plans WHERE workspace_id = ? AND id = ?`, workspaceID, planID).Scan(&found)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	if err != nil {
		return fmt.Errorf("verify workspace plan: %w", err)
	}
	return nil
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func decodeJSON(value string, dest any) error {
	if value == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(value), dest); err != nil {
		return fmt.Errorf("decode workspace plan json: %w", err)
	}
	return nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint")
}

func isForeignKeyConstraint(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint")
}
