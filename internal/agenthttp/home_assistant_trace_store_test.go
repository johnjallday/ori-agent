package agenthttp

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

type homeAssistantIntakeTraceStoreStub struct {
	traces  []HomeAssistantIntakeTrace
	err     error
	summary HomeAssistantIntakeTraceSummary
}

func (s *homeAssistantIntakeTraceStoreStub) RecordTrace(_ context.Context, trace *HomeAssistantIntakeTrace) error {
	if s.err != nil {
		return s.err
	}
	if trace == nil {
		return errors.New("trace is nil")
	}
	s.traces = append(s.traces, *trace)
	return nil
}

func (s *homeAssistantIntakeTraceStoreStub) Summary(_ context.Context) (HomeAssistantIntakeTraceSummary, error) {
	if s.err != nil {
		return HomeAssistantIntakeTraceSummary{}, s.err
	}
	return s.summary, nil
}

func (s *homeAssistantIntakeTraceStoreStub) PreferredWorkspaceForPrompt(_ context.Context, _ string) (string, bool, error) {
	return "", false, s.err
}

func (s *homeAssistantIntakeTraceStoreStub) RecentWorkspaceCorrections(_ context.Context, _ int) ([]HomeAssistantWorkspaceCorrection, error) {
	return nil, s.err
}

func TestSQLiteHomeAssistantIntakeTraceStore_RecordTrace(t *testing.T) {
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewSQLiteHomeAssistantIntakeTraceStore(db)
	trace := &HomeAssistantIntakeTrace{
		Prompt:                "ship launch tasks",
		Intent:                "general_task",
		ContextMode:           homeAssistantContextWorkspace,
		WorkspaceState:        homeAssistantWorkspaceStateConfident,
		SelectedWorkspaceID:   "ws-launch",
		SelectedWorkspaceName: "Launch Ops",
		FinalWorkspaceID:      "ws-launch",
		Confidence:            0.91,
		Reasons:               []string{"matched workspace goal"},
		Candidates: []HomeAssistantWorkspaceCandidate{
			{ID: "ws-launch", Name: "Launch Ops", Score: 9, Reasons: []string{"matched workspace goal"}},
		},
		UserOverride:       true,
		FinalHandoffTarget: "workspace_assistant",
		RouteContext: &HomeAssistantRouteContext{
			WorkspaceID: "ws-launch",
			Surface:     "dashboard",
		},
	}

	if err := store.RecordTrace(context.Background(), trace); err != nil {
		t.Fatalf("record trace: %v", err)
	}
	if trace.ID == "" || trace.CreatedAt.IsZero() {
		t.Fatalf("expected generated id and timestamp, got %#v", trace)
	}

	var (
		prompt, selectedWorkspaceID, reasonsJSON, candidatesJSON, routeContextJSON string
		userOverride                                                               int
	)
	err = db.QueryRowContext(context.Background(), `
		SELECT prompt, selected_workspace_id, reasons_json, candidates_json, user_override, route_context_json
		FROM home_assistant_intake_traces
		WHERE id = ?
	`, trace.ID).Scan(&prompt, &selectedWorkspaceID, &reasonsJSON, &candidatesJSON, &userOverride, &routeContextJSON)
	if err != nil {
		t.Fatalf("load trace row: %v", err)
	}
	if prompt != trace.Prompt || selectedWorkspaceID != trace.SelectedWorkspaceID {
		t.Fatalf("unexpected stored trace fields: prompt=%q workspace=%q", prompt, selectedWorkspaceID)
	}
	if reasonsJSON != `["matched workspace goal"]` {
		t.Fatalf("unexpected reasons json: %s", reasonsJSON)
	}
	if candidatesJSON == "[]" {
		t.Fatalf("expected candidate payload to persist")
	}
	if userOverride != 1 {
		t.Fatalf("expected user override to persist as 1, got %d", userOverride)
	}
	if routeContextJSON == "" {
		t.Fatalf("expected route context json")
	}
}

func TestSQLiteHomeAssistantIntakeTraceStore_Summary(t *testing.T) {
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewSQLiteHomeAssistantIntakeTraceStore(db)
	traces := []*HomeAssistantIntakeTrace{
		{
			Prompt:             "what time is it",
			ContextMode:        homeAssistantContextDirect,
			FinalHandoffTarget: "utility_tool",
		},
		{
			Prompt:              "ship launch tasks",
			ContextMode:         homeAssistantContextWorkspace,
			WorkspaceState:      homeAssistantWorkspaceStateConfident,
			SelectedWorkspaceID: "ws-launch",
			FinalWorkspaceID:    "ws-launch",
			FinalHandoffTarget:  "workspace_assistant",
		},
		{
			Prompt:              "ship the roadmap",
			ContextMode:         homeAssistantContextWorkspace,
			WorkspaceState:      homeAssistantWorkspaceStateConfident,
			SelectedWorkspaceID: "ws-launch",
			FinalWorkspaceID:    "ws-ops",
			UserOverride:        true,
			FinalHandoffTarget:  "workspace_assistant",
		},
		{
			Prompt:             "build the dashboard",
			ContextMode:        homeAssistantContextWorkspace,
			WorkspaceState:     homeAssistantWorkspaceStateAmbiguous,
			UserOverride:       true,
			FinalWorkspaceID:   "ws-dashboard",
			FinalHandoffTarget: "workspace_assistant",
		},
		{
			Prompt:             "start a payroll app",
			ContextMode:        homeAssistantContextWorkspace,
			WorkspaceState:     homeAssistantWorkspaceStateNoFit,
			FinalHandoffTarget: "workspace_create",
		},
		{
			Prompt:             "fix broken ops",
			ContextMode:        homeAssistantContextWorkspace,
			WorkspaceState:     homeAssistantWorkspaceStateNeedsRepair,
			FinalHandoffTarget: "workspace_repair",
		},
	}
	for _, trace := range traces {
		if err := store.RecordTrace(context.Background(), trace); err != nil {
			t.Fatalf("record trace %q: %v", trace.Prompt, err)
		}
	}

	summary, err := store.Summary(context.Background())
	if err != nil {
		t.Fatalf("summarize traces: %v", err)
	}
	want := HomeAssistantIntakeTraceSummary{
		TotalCount:       6,
		DirectCount:      1,
		WorkspaceCount:   5,
		ConfidentCount:   2,
		AmbiguousCount:   1,
		NoFitCount:       1,
		NeedsRepairCount: 1,
		OverrideCount:    2,
		CorrectionCount:  1,
	}
	if summary != want {
		t.Fatalf("expected summary %#v, got %#v", want, summary)
	}
}

func TestSQLiteHomeAssistantIntakeTraceStore_PreferredWorkspaceForPrompt(t *testing.T) {
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewSQLiteHomeAssistantIntakeTraceStore(db)
	for _, trace := range []*HomeAssistantIntakeTrace{
		{
			Prompt:              "build the cabinet roadmap",
			SelectedWorkspaceID: "ws-cabinet",
			FinalWorkspaceID:    "ws-cabinet",
			FinalHandoffTarget:  "workspace_assistant",
		},
		{
			Prompt:              "build the cabinet roadmap",
			SelectedWorkspaceID: "ws-cabinet",
			FinalWorkspaceID:    "ws-ops",
			UserOverride:        true,
			FinalHandoffTarget:  "workspace_assistant",
		},
	} {
		if err := store.RecordTrace(context.Background(), trace); err != nil {
			t.Fatalf("record trace: %v", err)
		}
	}

	workspaceID, ok, err := store.PreferredWorkspaceForPrompt(context.Background(), "BUILD THE CABINET ROADMAP")
	if err != nil {
		t.Fatalf("load preferred workspace: %v", err)
	}
	if !ok || workspaceID != "ws-ops" {
		t.Fatalf("expected ws-ops preference, got id=%q ok=%v", workspaceID, ok)
	}
}

func TestSQLiteHomeAssistantIntakeTraceStore_RecentWorkspaceCorrections(t *testing.T) {
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewSQLiteHomeAssistantIntakeTraceStore(db)
	for _, trace := range []*HomeAssistantIntakeTrace{
		{
			Prompt:              "build the cabinet roadmap",
			SelectedWorkspaceID: "ws-cabinet",
			FinalWorkspaceID:    "ws-cabinet",
			FinalHandoffTarget:  "workspace_assistant",
		},
		{
			Prompt:              "build the cabinet roadmap",
			SelectedWorkspaceID: "ws-cabinet",
			FinalWorkspaceID:    "ws-ops",
			UserOverride:        true,
			FinalHandoffTarget:  "workspace_assistant",
		},
		{
			Prompt:              "review the payroll roadmap",
			SelectedWorkspaceID: "ws-payroll",
			FinalWorkspaceID:    "ws-finance",
			UserOverride:        true,
			FinalHandoffTarget:  "workspace_assistant",
		},
	} {
		if err := store.RecordTrace(context.Background(), trace); err != nil {
			t.Fatalf("record trace: %v", err)
		}
	}

	corrections, err := store.RecentWorkspaceCorrections(context.Background(), 10)
	if err != nil {
		t.Fatalf("load recent corrections: %v", err)
	}
	if len(corrections) != 2 {
		t.Fatalf("expected 2 corrections, got %#v", corrections)
	}
	if corrections[0].Prompt != "review the payroll roadmap" || corrections[0].WorkspaceID != "ws-finance" {
		t.Fatalf("expected newest correction first, got %#v", corrections)
	}
	if corrections[1].Prompt != "build the cabinet roadmap" || corrections[1].WorkspaceID != "ws-ops" {
		t.Fatalf("expected cabinet correction second, got %#v", corrections)
	}
}
