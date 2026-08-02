package workspacerun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

type SQLiteStore struct {
	db *database.DB
}

func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) CreateRun(ctx context.Context, run *Run) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	if run.ID == "" {
		run.ID = uuid.New().String()
	}
	if run.WorkspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}
	if run.Status == "" {
		run.Status = RunStatusPending
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspace_runs (
			id, workspace_id, parent_run_id, profile_id, profile_version,
			profile_snapshot_json, executor_json, scope_json, policy_json, environment_json, context_plan_json,
			reference_url, prompt, status, created_at, started_at, finished_at,
			prepared_context_json, validation_request_json, validation_result_json, task_output_json, cost_json, report_json, toolbox_snapshot_json, toolbox_wrap_up_json, error, updated_at
		)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, run.WorkspaceID, run.ParentRunID, run.ProfileID, run.ProfileVersion,
		mustJSON(run.ProfileSnapshot), mustJSON(run.Executor), mustJSON(run.Scope), mustJSON(run.Policy), mustJSON(run.Environment), mustJSON(run.ContextPlan),
		run.ReferenceURL, run.Prompt, string(run.Status), run.CreatedAt, nullableTime(run.StartedAt), nullableTime(run.FinishedAt),
		nullableJSON(run.PreparedContext), nullableJSON(run.ValidationRequest), nullableJSON(run.ValidationResult), nullableJSON(run.TaskOutput), nullableJSON(run.Cost), nullableJSON(run.Report), nullableJSON(run.ToolboxSnapshot), nullableJSON(run.ToolboxWrapUp), nullableString(run.Error), now)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrRunExists
		}
		return fmt.Errorf("create workspace run: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetRun(ctx context.Context, workspaceID, runID string) (*Run, error) {
	run, err := s.getRun(ctx, workspaceID, runID)
	if err != nil {
		return nil, err
	}
	trace, err := s.ListTrace(ctx, workspaceID, runID, 0, DefaultTracePageLimit)
	if err != nil {
		return nil, err
	}
	run.TraceTail = TraceTail(trace.Events, DefaultTraceTailLimit)
	artifacts, err := s.ListArtifacts(ctx, workspaceID, runID)
	if err != nil {
		return nil, err
	}
	run.Artifacts = artifacts
	return run, nil
}

func (s *SQLiteStore) ListRuns(ctx context.Context, workspaceID string) ([]*Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM workspace_runs
		WHERE workspace_id = ?
		ORDER BY created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list workspace runs: %w", err)
	}

	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, fmt.Errorf("scan workspace run id: %w", err)
		}
		runIDs = append(runIDs, runID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace runs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close workspace run rows: %w", err)
	}

	runs := make([]*Run, 0, len(runIDs))
	for _, runID := range runIDs {
		run, err := s.GetRun(ctx, workspaceID, runID)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *SQLiteStore) UpdateStatus(ctx context.Context, workspaceID, runID string, status RunStatus, message string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_runs
		SET status = ?, error = CASE WHEN ? = '' THEN error ELSE ? END, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, string(status), message, message, time.Now(), workspaceID, runID)
	return checkRunUpdate(result, err, "update workspace run status")
}

func (s *SQLiteStore) UpdateEnvironment(ctx context.Context, workspaceID, runID string, env Environment) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_runs
		SET environment_json = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, mustJSON(env), time.Now(), workspaceID, runID)
	return checkRunUpdate(result, err, "update workspace run environment")
}

func (s *SQLiteStore) AppendTrace(ctx context.Context, workspaceID, runID string, event TraceEvent) (TraceEvent, error) {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	event.RunID = runID
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var sequence int64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(sequence), 0) + 1
			FROM workspace_run_trace
			WHERE run_id = ?
		`, runID).Scan(&sequence); err != nil {
			return err
		}
		event.Sequence = sequence
		_, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_run_trace (
				id, workspace_id, run_id, sequence, kind, source, message, status,
				tool_name, artifact_id, data_json, created_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, event.ID, workspaceID, runID, event.Sequence, string(event.Kind), event.Source, event.Message, event.Status,
			event.ToolName, event.ArtifactID, nullableJSON(event.Data), event.CreatedAt)
		return err
	})
	if err != nil {
		return TraceEvent{}, fmt.Errorf("append workspace run trace: %w", err)
	}
	return CloneTraceEvent(event), nil
}

func (s *SQLiteStore) ListTrace(ctx context.Context, workspaceID, runID string, since int64, limit int) (TracePage, error) {
	if limit <= 0 || limit > MaxTracePageLimit {
		limit = DefaultTracePageLimit
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, sequence, kind, source, message, status, tool_name, artifact_id, data_json, created_at
		FROM workspace_run_trace
		WHERE workspace_id = ? AND run_id = ? AND sequence > ?
		ORDER BY sequence ASC
		LIMIT ?
	`, workspaceID, runID, since, limit+1)
	if err != nil {
		return TracePage{}, fmt.Errorf("list workspace run trace: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var events []TraceEvent
	for rows.Next() {
		event, err := scanTraceEvent(rows)
		if err != nil {
			return TracePage{}, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return TracePage{}, fmt.Errorf("iterate workspace run trace: %w", err)
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	nextSince := since
	if len(events) > 0 {
		nextSince = events[len(events)-1].Sequence
	}
	return TracePage{Events: CloneTraceEvents(events), NextSince: nextSince, HasMore: hasMore}, nil
}

func (s *SQLiteStore) AddArtifact(ctx context.Context, workspaceID, runID string, artifact Artifact) (Artifact, error) {
	if artifact.ID == "" {
		artifact.ID = uuid.New().String()
	}
	artifact.RunID = runID
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspace_run_artifacts (id, workspace_id, run_id, kind, path, inline, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, artifact.ID, workspaceID, runID, string(artifact.Kind), artifact.Path, artifact.Inline, nullableJSON(artifact.Metadata), artifact.CreatedAt)
	if err != nil {
		return Artifact{}, fmt.Errorf("add workspace run artifact: %w", err)
	}
	return CloneArtifact(artifact), nil
}

func (s *SQLiteStore) ListArtifacts(ctx context.Context, workspaceID, runID string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, kind, path, inline, metadata_json, created_at
		FROM workspace_run_artifacts
		WHERE workspace_id = ? AND run_id = ?
		ORDER BY created_at ASC
	`, workspaceID, runID)
	if err != nil {
		return nil, fmt.Errorf("list workspace run artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var artifacts []Artifact
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace run artifacts: %w", err)
	}
	return CloneArtifacts(artifacts), nil
}

func (s *SQLiteStore) SetValidationResult(ctx context.Context, workspaceID, runID string, result ValidationResult) error {
	sqlResult, err := s.db.ExecContext(ctx, `
		UPDATE workspace_runs
		SET validation_result_json = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, mustJSON(result), time.Now(), workspaceID, runID)
	return checkRunUpdate(sqlResult, err, "set workspace run validation result")
}

func (s *SQLiteStore) SetTaskOutput(ctx context.Context, workspaceID, runID string, output TaskOutputSummary) error {
	sqlResult, err := s.db.ExecContext(ctx, `
		UPDATE workspace_runs
		SET task_output_json = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, mustJSON(output), time.Now(), workspaceID, runID)
	return checkRunUpdate(sqlResult, err, "set workspace run task output")
}

func (s *SQLiteStore) SetPreparedContext(ctx context.Context, workspaceID, runID string, prepared PreparedContext) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_runs
		SET prepared_context_json = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, mustJSON(prepared), time.Now(), workspaceID, runID)
	return checkRunUpdate(result, err, "set workspace run prepared context")
}

func (s *SQLiteStore) SetReport(ctx context.Context, workspaceID, runID string, report Report) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_runs
		SET report_json = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, mustJSON(report), time.Now(), workspaceID, runID)
	return checkRunUpdate(result, err, "set workspace run report")
}

func (s *SQLiteStore) SetCost(ctx context.Context, workspaceID, runID string, cost CostSummary) error {
	cost = NormalizeCost(cost)
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_runs
		SET cost_json = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, mustJSON(cost), time.Now(), workspaceID, runID)
	return checkRunUpdate(result, err, "set workspace run cost")
}

// SetToolboxSnapshot freezes the run's capabilities.
//
// Called BEFORE the model is invoked, which is the whole point: a snapshot
// written afterwards would describe what the run ended up with rather than what
// it was given, and could not prove that a mid-run toolbox edit changed nothing
// (PRD FR-107).
func (s *SQLiteStore) SetToolboxSnapshot(ctx context.Context, workspaceID, runID string, snapshot RunToolboxSnapshot) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_runs
		SET toolbox_snapshot_json = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, mustJSON(snapshot), time.Now(), workspaceID, runID)
	return checkRunUpdate(result, err, "set workspace run toolbox snapshot")
}

// SetToolboxWrapUp records the post-run measurement against that snapshot.
func (s *SQLiteStore) SetToolboxWrapUp(ctx context.Context, workspaceID, runID string, wrapUp ToolboxWrapUp) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_runs
		SET toolbox_wrap_up_json = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, mustJSON(wrapUp), time.Now(), workspaceID, runID)
	return checkRunUpdate(result, err, "set workspace run toolbox wrap-up")
}

func (s *SQLiteStore) SetError(ctx context.Context, workspaceID, runID, errMessage string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_runs
		SET error = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, errMessage, time.Now(), workspaceID, runID)
	return checkRunUpdate(result, err, "set workspace run error")
}

func (s *SQLiteStore) getRun(ctx context.Context, workspaceID, runID string) (*Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, parent_run_id, profile_id, profile_version,
			profile_snapshot_json, executor_json, scope_json, policy_json, environment_json, context_plan_json,
			reference_url, prompt, status, created_at, started_at, finished_at,
			prepared_context_json, validation_request_json, validation_result_json, task_output_json, cost_json, report_json, toolbox_snapshot_json, toolbox_wrap_up_json, error
		FROM workspace_runs
		WHERE workspace_id = ? AND id = ?
	`, workspaceID, runID)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, err
	}
	return run, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRun(row scanner) (*Run, error) {
	var run Run
	var parent sql.NullString
	var profileJSON, executorJSON, scopeJSON, policyJSON, envJSON, contextPlanJSON string
	var status string
	var startedAt, finishedAt sql.NullTime
	var preparedContextJSON, validationReqJSON, validationResultJSON, taskOutputJSON, costJSON, reportJSON, toolboxSnapshotJSON, toolboxWrapUpJSON, errText sql.NullString
	if err := row.Scan(
		&run.ID, &run.WorkspaceID, &parent, &run.ProfileID, &run.ProfileVersion,
		&profileJSON, &executorJSON, &scopeJSON, &policyJSON, &envJSON, &contextPlanJSON,
		&run.ReferenceURL, &run.Prompt, &status, &run.CreatedAt, &startedAt, &finishedAt,
		&preparedContextJSON, &validationReqJSON, &validationResultJSON, &taskOutputJSON, &costJSON, &reportJSON, &toolboxSnapshotJSON, &toolboxWrapUpJSON, &errText,
	); err != nil {
		return nil, err
	}
	run.ParentRunID = parent.String
	run.Status = RunStatus(status)
	run.StartedAt = nullTimePtr(startedAt)
	run.FinishedAt = nullTimePtr(finishedAt)
	run.Error = errText.String
	if err := decodeJSON(profileJSON, &run.ProfileSnapshot); err != nil {
		return nil, err
	}
	if err := decodeJSON(executorJSON, &run.Executor); err != nil {
		return nil, err
	}
	if err := decodeJSON(scopeJSON, &run.Scope); err != nil {
		return nil, err
	}
	if err := decodeJSON(policyJSON, &run.Policy); err != nil {
		return nil, err
	}
	if err := decodeJSON(envJSON, &run.Environment); err != nil {
		return nil, err
	}
	if err := decodeJSON(contextPlanJSON, &run.ContextPlan); err != nil {
		return nil, err
	}
	if preparedContextJSON.Valid {
		var prepared PreparedContext
		if err := decodeJSON(preparedContextJSON.String, &prepared); err != nil {
			return nil, err
		}
		run.PreparedContext = &prepared
	}
	if validationReqJSON.Valid {
		var req ValidationRequest
		if err := decodeJSON(validationReqJSON.String, &req); err != nil {
			return nil, err
		}
		run.ValidationRequest = &req
	}
	if validationResultJSON.Valid {
		var result ValidationResult
		if err := decodeJSON(validationResultJSON.String, &result); err != nil {
			return nil, err
		}
		run.ValidationResult = &result
	}
	if taskOutputJSON.Valid {
		var output TaskOutputSummary
		if err := decodeJSON(taskOutputJSON.String, &output); err != nil {
			return nil, err
		}
		run.TaskOutput = &output
	}
	if costJSON.Valid {
		var cost CostSummary
		if err := decodeJSON(costJSON.String, &cost); err != nil {
			return nil, err
		}
		run.Cost = &cost
	}
	if reportJSON.Valid {
		var report Report
		if err := decodeJSON(reportJSON.String, &report); err != nil {
			return nil, err
		}
		run.Report = &report
	}
	// NULL means this run predates snapshots, which is different from having
	// had no capabilities — leaving these nil keeps that distinction readable
	// instead of reporting a historical run as unrestricted or empty.
	if toolboxSnapshotJSON.Valid {
		var snapshot RunToolboxSnapshot
		if err := decodeJSON(toolboxSnapshotJSON.String, &snapshot); err != nil {
			return nil, err
		}
		run.ToolboxSnapshot = &snapshot
	}
	if toolboxWrapUpJSON.Valid {
		var wrapUp ToolboxWrapUp
		if err := decodeJSON(toolboxWrapUpJSON.String, &wrapUp); err != nil {
			return nil, err
		}
		run.ToolboxWrapUp = &wrapUp
	}
	return CloneRun(&run), nil
}

func scanTraceEvent(row scanner) (TraceEvent, error) {
	var event TraceEvent
	var kind string
	var dataJSON sql.NullString
	if err := row.Scan(&event.ID, &event.RunID, &event.Sequence, &kind, &event.Source, &event.Message, &event.Status, &event.ToolName, &event.ArtifactID, &dataJSON, &event.CreatedAt); err != nil {
		return TraceEvent{}, fmt.Errorf("scan workspace run trace: %w", err)
	}
	event.Kind = TraceEventKind(kind)
	if dataJSON.Valid {
		if err := decodeJSON(dataJSON.String, &event.Data); err != nil {
			return TraceEvent{}, err
		}
	}
	return CloneTraceEvent(event), nil
}

func scanArtifact(row scanner) (Artifact, error) {
	var artifact Artifact
	var kind string
	var inline []byte
	var metadataJSON sql.NullString
	if err := row.Scan(&artifact.ID, &artifact.RunID, &kind, &artifact.Path, &inline, &metadataJSON, &artifact.CreatedAt); err != nil {
		return Artifact{}, fmt.Errorf("scan workspace run artifact: %w", err)
	}
	artifact.Kind = ArtifactKind(kind)
	artifact.Inline = append([]byte(nil), inline...)
	if metadataJSON.Valid {
		if err := decodeJSON(metadataJSON.String, &artifact.Metadata); err != nil {
			return Artifact{}, err
		}
	}
	return CloneArtifact(artifact), nil
}

func mustJSON(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// nullableJSON stores a missing optional value as SQL NULL rather than the
// JSON literal "null".
//
// The reflect check is doing real work. Every caller passes a TYPED pointer —
// (*CostSummary)(nil), (*Report)(nil) — and a typed nil in an `any` is not
// equal to nil, so the plain `value == nil` test never fired. Those fields were
// being stored as the four bytes "null", read back as Valid, decoded into a
// zero struct, and handed out as a non-nil pointer to an empty value. A run
// with no cost reported a cost of zero rather than no cost at all, which is a
// different claim.
func nullableJSON(value any) any {
	if value == nil {
		return nil
	}
	if reflected := reflect.ValueOf(value); reflected.Kind() == reflect.Ptr && reflected.IsNil() {
		return nil
	}
	return mustJSON(value)
}

func decodeJSON(value string, dest any) error {
	if value == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(value), dest); err != nil {
		return fmt.Errorf("decode workspace run json: %w", err)
	}
	return nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func checkRunUpdate(result sql.Result, err error, op string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return database.CheckRowsAffectedWithError(result, "workspace_run", ErrRunNotFound)
}

func isUniqueConstraint(err error) bool {
	return err != nil && (errors.Is(err, ErrRunExists) || strings.Contains(err.Error(), "UNIQUE constraint"))
}
