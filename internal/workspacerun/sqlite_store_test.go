package workspacerun

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func TestSQLiteStoreRunArtifactTraceRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, ctx)
	store := NewSQLiteStore(db)
	createTestWorkspace(t, ctx, db, "workspace-1")

	run := &Run{
		ID:             "run-1",
		WorkspaceID:    "workspace-1",
		ParentRunID:    "parent-1",
		ProfileID:      ProfileGeneral,
		ProfileVersion: "1",
		ProfileSnapshot: Profile{
			ID:      ProfileGeneral,
			Version: "1",
			Name:    "General",
		},
		Executor: Executor{Kind: ExecutorKindNativeCLI, Ref: "codex"},
		Scope:    Scope{TargetNoteID: "note-1"},
		Policy:   Policy{Mutation: PolicyMutationDenied, Approval: PolicyApprovalNone},
		ContextPlan: ContextPlan{
			Strategy:                 "task_default",
			IncludeWorkspaceSnapshot: true,
		},
		Prompt: "do work",
		Status: RunStatusPending,
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := store.AppendTrace(ctx, "workspace-1", "run-1", NewTraceEvent("run-1", TraceMessage, TraceMessageText("hello"))); err != nil {
		t.Fatalf("append trace 1: %v", err)
	}
	if _, err := store.AppendTrace(ctx, "workspace-1", "run-1", NewTraceEvent("run-1", TraceError, TraceMessageText("boom"))); err != nil {
		t.Fatalf("append trace 2: %v", err)
	}
	if _, err := store.AddArtifact(ctx, "workspace-1", "run-1", NewArtifact("run-1", ArtifactLog, ArtifactInline([]byte("log")))); err != nil {
		t.Fatalf("add artifact: %v", err)
	}
	if err := store.SetCost(ctx, "workspace-1", "run-1", CostSummary{InputTokens: 2, OutputTokens: 3}); err != nil {
		t.Fatalf("set cost: %v", err)
	}
	if err := store.SetPreparedContext(ctx, "workspace-1", "run-1", PreparedContext{
		Strategy:       "task_default",
		Summary:        "prepared",
		AvailableTools: []string{"workspace_notes"},
		PreparedAt:     time.Now(),
		Items: []PreparedContextItem{
			{Kind: "workspace_snapshot", Access: PreparedContextAccessInjected},
		},
	}); err != nil {
		t.Fatalf("set prepared context: %v", err)
	}
	if err := store.SetReport(ctx, "workspace-1", "run-1", Report{Summary: "done", ValidationStatus: ValidationStatusPassed}); err != nil {
		t.Fatalf("set report: %v", err)
	}

	got, err := store.GetRun(ctx, "workspace-1", "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.ParentRunID != "parent-1" {
		t.Fatalf("ParentRunID = %q, want parent-1", got.ParentRunID)
	}
	if got.Cost == nil || got.Cost.TotalTokens != 5 {
		t.Fatalf("Cost = %+v, want total tokens 5", got.Cost)
	}
	if got.ContextPlan.Strategy != "task_default" || got.PreparedContext == nil || got.PreparedContext.Summary != "prepared" {
		t.Fatalf("context = plan %+v prepared %+v, want persisted context data", got.ContextPlan, got.PreparedContext)
	}
	if got.Report == nil || got.Report.Summary != "done" {
		t.Fatalf("Report = %+v, want summary done", got.Report)
	}
	if len(got.Artifacts) != 1 || string(got.Artifacts[0].Inline) != "log" {
		t.Fatalf("Artifacts = %+v, want persisted log artifact", got.Artifacts)
	}
	if len(got.TraceTail) != 2 {
		t.Fatalf("TraceTail length = %d, want 2", len(got.TraceTail))
	}

	page, err := store.ListTrace(ctx, "workspace-1", "run-1", 1, 10)
	if err != nil {
		t.Fatalf("list trace: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].Kind != TraceError {
		t.Fatalf("trace page = %+v, want only error event", page.Events)
	}
}

func TestSQLiteStoreTracePagingCapsResults(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, ctx)
	store := NewSQLiteStore(db)
	createTestWorkspace(t, ctx, db, "workspace-1")

	if err := store.CreateRun(ctx, &Run{
		ID:             "run-1",
		WorkspaceID:    "workspace-1",
		ProfileID:      ProfileGeneral,
		ProfileVersion: "1",
		ProfileSnapshot: Profile{
			ID:      ProfileGeneral,
			Version: "1",
			Name:    "General",
		},
		Executor: Executor{Kind: ExecutorKindNativeCLI, Ref: "codex"},
		Policy:   Policy{Mutation: PolicyMutationDenied, Approval: PolicyApprovalNone},
		Prompt:   "do work",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.AppendTrace(ctx, "workspace-1", "run-1", NewTraceEvent("run-1", TraceMessage)); err != nil {
			t.Fatalf("append trace %d: %v", i, err)
		}
	}

	page, err := store.ListTrace(ctx, "workspace-1", "run-1", 0, 2)
	if err != nil {
		t.Fatalf("list trace: %v", err)
	}
	if len(page.Events) != 2 || page.NextSince != 2 || !page.HasMore {
		t.Fatalf("page = %+v, want 2 events, next_since 2, has_more true", page)
	}
}

func TestSQLiteStoreListRunsDoesNotDeadlockWithSingleConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	db := openTestDB(t, ctx)
	store := NewSQLiteStore(db)
	createTestWorkspace(t, ctx, db, "workspace-1")

	for _, runID := range []string{"run-1", "run-2"} {
		if err := store.CreateRun(ctx, &Run{
			ID:             runID,
			WorkspaceID:    "workspace-1",
			ProfileID:      ProfileGeneral,
			ProfileVersion: "1",
			ProfileSnapshot: Profile{
				ID:      ProfileGeneral,
				Version: "1",
				Name:    "General",
			},
			Executor: Executor{Kind: ExecutorKindNativeCLI, Ref: "codex"},
			Policy:   Policy{Mutation: PolicyMutationDenied, Approval: PolicyApprovalNone},
			Prompt:   "do work",
		}); err != nil {
			t.Fatalf("create run %s: %v", runID, err)
		}
	}

	runs, err := store.ListRuns(ctx, "workspace-1")
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs length = %d, want 2", len(runs))
	}
}

func openTestDB(t *testing.T, ctx context.Context) *database.DB {
	t.Helper()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func createTestWorkspace(t *testing.T, ctx context.Context, db *database.DB, id string) {
	t.Helper()
	now := time.Now()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, id, "Test Workspace", now, now); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
}
