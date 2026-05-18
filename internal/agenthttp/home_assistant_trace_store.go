package agenthttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

type HomeAssistantIntakeTraceStore interface {
	RecordTrace(ctx context.Context, trace *HomeAssistantIntakeTrace) error
	Summary(ctx context.Context) (HomeAssistantIntakeTraceSummary, error)
	PreferredWorkspaceForPrompt(ctx context.Context, prompt string) (string, bool, error)
	RecentWorkspaceCorrections(ctx context.Context, limit int) ([]HomeAssistantWorkspaceCorrection, error)
}

type SQLiteHomeAssistantIntakeTraceStore struct {
	db *database.DB
}

type HomeAssistantIntakeTraceSummary struct {
	TotalCount       int `json:"total_count"`
	DirectCount      int `json:"direct_count"`
	WorkspaceCount   int `json:"workspace_count"`
	ConfidentCount   int `json:"confident_count"`
	AmbiguousCount   int `json:"ambiguous_count"`
	NoFitCount       int `json:"no_fit_count"`
	NeedsRepairCount int `json:"needs_repair_count"`
	OverrideCount    int `json:"override_count"`
	CorrectionCount  int `json:"correction_count"`
}

type HomeAssistantWorkspaceCorrection struct {
	Prompt      string
	WorkspaceID string
}

func NewSQLiteHomeAssistantIntakeTraceStore(db *database.DB) *SQLiteHomeAssistantIntakeTraceStore {
	return &SQLiteHomeAssistantIntakeTraceStore{db: db}
}

func (s *SQLiteHomeAssistantIntakeTraceStore) RecordTrace(ctx context.Context, trace *HomeAssistantIntakeTrace) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("home assistant intake trace store is unavailable")
	}
	if trace == nil {
		return fmt.Errorf("trace is nil")
	}
	if trace.ID == "" {
		trace.ID = uuid.New().String()
	}
	if trace.CreatedAt.IsZero() {
		trace.CreatedAt = time.Now()
	}

	reasons := trace.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	candidates := trace.Candidates
	if candidates == nil {
		candidates = []HomeAssistantWorkspaceCandidate{}
	}

	reasonsJSON, err := json.Marshal(reasons)
	if err != nil {
		return fmt.Errorf("encode trace reasons: %w", err)
	}
	candidatesJSON, err := json.Marshal(candidates)
	if err != nil {
		return fmt.Errorf("encode trace candidates: %w", err)
	}
	routeContextJSON, err := json.Marshal(trace.RouteContext)
	if err != nil {
		return fmt.Errorf("encode trace route context: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO home_assistant_intake_traces (
			id, prompt, intent, intent_variant, routing_policy, context_mode, handoff_policy,
			route_mode, target_surface, matched_agent, workspace_state, selected_workspace_id,
			selected_workspace_name, final_workspace_id, confidence, reasons_json, candidates_json,
			user_override, final_handoff_target, route_context_json, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, trace.ID, trace.Prompt, trace.Intent, trace.IntentVariant, trace.RoutingPolicy, trace.ContextMode,
		trace.HandoffPolicy, trace.RouteMode, trace.TargetSurface, trace.MatchedAgent, trace.WorkspaceState,
		trace.SelectedWorkspaceID, trace.SelectedWorkspaceName, trace.FinalWorkspaceID, trace.Confidence,
		string(reasonsJSON), string(candidatesJSON), boolToSQLiteInt(trace.UserOverride),
		trace.FinalHandoffTarget, nullableTraceJSON(routeContextJSON, trace.RouteContext != nil), trace.CreatedAt)
	if err != nil {
		return fmt.Errorf("record home assistant intake trace: %w", err)
	}
	return nil
}

func (s *SQLiteHomeAssistantIntakeTraceStore) Summary(ctx context.Context) (HomeAssistantIntakeTraceSummary, error) {
	if s == nil || s.db == nil {
		return HomeAssistantIntakeTraceSummary{}, fmt.Errorf("home assistant intake trace store is unavailable")
	}

	var summary HomeAssistantIntakeTraceSummary
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total_count,
			COALESCE(SUM(CASE WHEN context_mode = 'direct' THEN 1 ELSE 0 END), 0) AS direct_count,
			COALESCE(SUM(CASE WHEN context_mode = 'workspace' THEN 1 ELSE 0 END), 0) AS workspace_count,
			COALESCE(SUM(CASE WHEN workspace_state = 'confident' THEN 1 ELSE 0 END), 0) AS confident_count,
			COALESCE(SUM(CASE WHEN workspace_state = 'ambiguous' THEN 1 ELSE 0 END), 0) AS ambiguous_count,
			COALESCE(SUM(CASE WHEN workspace_state = 'no_fit' THEN 1 ELSE 0 END), 0) AS no_fit_count,
			COALESCE(SUM(CASE WHEN workspace_state = 'needs_repair' THEN 1 ELSE 0 END), 0) AS needs_repair_count,
			COALESCE(SUM(CASE WHEN user_override = 1 THEN 1 ELSE 0 END), 0) AS override_count,
			COALESCE(SUM(CASE
				WHEN user_override = 1
					AND selected_workspace_id <> ''
					AND final_workspace_id <> ''
					AND final_workspace_id <> selected_workspace_id
				THEN 1 ELSE 0 END), 0) AS correction_count
		FROM home_assistant_intake_traces
	`).Scan(
		&summary.TotalCount,
		&summary.DirectCount,
		&summary.WorkspaceCount,
		&summary.ConfidentCount,
		&summary.AmbiguousCount,
		&summary.NoFitCount,
		&summary.NeedsRepairCount,
		&summary.OverrideCount,
		&summary.CorrectionCount,
	)
	if err != nil {
		return HomeAssistantIntakeTraceSummary{}, fmt.Errorf("summarize home assistant intake traces: %w", err)
	}
	return summary, nil
}

func (s *SQLiteHomeAssistantIntakeTraceStore) PreferredWorkspaceForPrompt(ctx context.Context, prompt string) (string, bool, error) {
	if s == nil || s.db == nil {
		return "", false, fmt.Errorf("home assistant intake trace store is unavailable")
	}
	var workspaceID string
	err := s.db.QueryRowContext(ctx, `
		SELECT final_workspace_id
		FROM home_assistant_intake_traces
		WHERE LOWER(prompt) = LOWER(?)
			AND user_override = 1
			AND final_workspace_id <> ''
			AND final_workspace_id <> selected_workspace_id
		ORDER BY created_at DESC
		LIMIT 1
	`, prompt).Scan(&workspaceID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load preferred workspace for prompt: %w", err)
	}
	workspaceID = strings.TrimSpace(workspaceID)
	return workspaceID, workspaceID != "", nil
}

func (s *SQLiteHomeAssistantIntakeTraceStore) RecentWorkspaceCorrections(ctx context.Context, limit int) ([]HomeAssistantWorkspaceCorrection, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("home assistant intake trace store is unavailable")
	}
	if limit <= 0 {
		return []HomeAssistantWorkspaceCorrection{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT prompt, final_workspace_id
		FROM home_assistant_intake_traces
		WHERE user_override = 1
			AND final_workspace_id <> ''
			AND final_workspace_id <> selected_workspace_id
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("load recent workspace corrections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	corrections := make([]HomeAssistantWorkspaceCorrection, 0)
	for rows.Next() {
		var correction HomeAssistantWorkspaceCorrection
		if err := rows.Scan(&correction.Prompt, &correction.WorkspaceID); err != nil {
			return nil, fmt.Errorf("scan recent workspace correction: %w", err)
		}
		correction.Prompt = strings.TrimSpace(correction.Prompt)
		correction.WorkspaceID = strings.TrimSpace(correction.WorkspaceID)
		if correction.Prompt == "" || correction.WorkspaceID == "" {
			continue
		}
		corrections = append(corrections, correction)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent workspace corrections: %w", err)
	}
	return corrections, nil
}

func boolToSQLiteInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableTraceJSON(raw []byte, present bool) interface{} {
	if !present {
		return nil
	}
	return string(raw)
}
