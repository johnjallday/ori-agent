package workspacerun

import (
	"context"
	"sync"
	"testing"
)

func TestMemoryStoreCreateGetDefensiveCopiesAndParentRun(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	run := &Run{
		ID:          "run-1",
		WorkspaceID: "workspace-1",
		ParentRunID: "parent-1",
		ProfileID:   ProfileGeneral,
		Status:      RunStatusPending,
		Policy: Policy{
			ToolAllow: []string{"mcp:a"},
		},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	got, err := store.GetRun(ctx, "workspace-1", "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.ParentRunID != "parent-1" {
		t.Fatalf("ParentRunID = %q, want parent-1", got.ParentRunID)
	}
	got.Policy.ToolAllow[0] = "changed"

	again, err := store.GetRun(ctx, "workspace-1", "run-1")
	if err != nil {
		t.Fatalf("get run again: %v", err)
	}
	if again.Policy.ToolAllow[0] != "mcp:a" {
		t.Fatal("store did not return defensive run copy")
	}
}

func TestMemoryStoreSetTaskOutputDefensiveCopy(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.CreateRun(ctx, &Run{ID: "run-1", WorkspaceID: "workspace-1"}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	output := TaskOutputSummary{
		TaskID:           "task-1",
		ValidationStatus: "passed",
		StorageStatus:    "appended",
		Errors: []TaskOutputValidationError{
			{Code: "old"},
		},
	}
	if err := store.SetTaskOutput(ctx, "workspace-1", "run-1", output); err != nil {
		t.Fatalf("set task output: %v", err)
	}
	output.Errors[0].Code = "mutated"

	got, err := store.GetRun(ctx, "workspace-1", "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.TaskOutput == nil || got.TaskOutput.Errors[0].Code != "old" {
		t.Fatalf("TaskOutput = %+v, want defensive copy", got.TaskOutput)
	}
	got.TaskOutput.Errors[0].Code = "changed"

	again, err := store.GetRun(ctx, "workspace-1", "run-1")
	if err != nil {
		t.Fatalf("get run again: %v", err)
	}
	if again.TaskOutput == nil || again.TaskOutput.Errors[0].Code != "old" {
		t.Fatalf("TaskOutput = %+v, want stored copy unchanged", again.TaskOutput)
	}
}

func TestMemoryStoreConcurrentTraceAppendSequences(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.CreateRun(ctx, &Run{ID: "run-1", WorkspaceID: "workspace-1"}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	const count = 25
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			if _, err := store.AppendTrace(ctx, "workspace-1", "run-1", NewTraceEvent("run-1", TraceMessage)); err != nil {
				t.Errorf("append trace: %v", err)
			}
		}()
	}
	wg.Wait()

	page, err := store.ListTrace(ctx, "workspace-1", "run-1", 0, count)
	if err != nil {
		t.Fatalf("list trace: %v", err)
	}
	if len(page.Events) != count {
		t.Fatalf("trace count = %d, want %d", len(page.Events), count)
	}
	for i, event := range page.Events {
		want := int64(i + 1)
		if event.Sequence != want {
			t.Fatalf("event[%d].Sequence = %d, want %d", i, event.Sequence, want)
		}
	}
}

func TestMemoryStoreConcurrentStatusUpdatesSerialize(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.CreateRun(ctx, &Run{ID: "run-1", WorkspaceID: "workspace-1"}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	statuses := []RunStatus{
		RunStatusPreparing,
		RunStatusExecuting,
		RunStatusValidating,
		RunStatusAwaitingApproval,
	}
	var wg sync.WaitGroup
	wg.Add(len(statuses))
	for _, status := range statuses {
		status := status
		go func() {
			defer wg.Done()
			if err := store.UpdateStatus(ctx, "workspace-1", "run-1", status, ""); err != nil {
				t.Errorf("update status: %v", err)
			}
		}()
	}
	wg.Wait()

	run, err := store.GetRun(ctx, "workspace-1", "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	found := false
	for _, status := range statuses {
		if run.Status == status {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Status = %q, want one of %v", run.Status, statuses)
	}
}

func TestMemoryStoreTraceSinceAndArtifacts(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.CreateRun(ctx, &Run{ID: "run-1", WorkspaceID: "workspace-1"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := store.AppendTrace(ctx, "workspace-1", "run-1", NewTraceEvent("run-1", TraceMessage)); err != nil {
		t.Fatalf("append trace 1: %v", err)
	}
	if _, err := store.AppendTrace(ctx, "workspace-1", "run-1", NewTraceEvent("run-1", TraceError)); err != nil {
		t.Fatalf("append trace 2: %v", err)
	}
	page, err := store.ListTrace(ctx, "workspace-1", "run-1", 1, 10)
	if err != nil {
		t.Fatalf("list trace: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].Kind != TraceError {
		t.Fatalf("trace since page = %+v, want only error event", page.Events)
	}

	artifact, err := store.AddArtifact(ctx, "workspace-1", "run-1", NewArtifact("run-1", ArtifactLog, ArtifactInline([]byte("log"))))
	if err != nil {
		t.Fatalf("add artifact: %v", err)
	}
	if artifact.ID == "" || artifact.RunID != "run-1" {
		t.Fatalf("artifact = %+v, want stable id and run id", artifact)
	}
	artifacts, err := store.ListArtifacts(ctx, "workspace-1", "run-1")
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	artifacts[0].Inline[0] = 'x'
	again, err := store.ListArtifacts(ctx, "workspace-1", "run-1")
	if err != nil {
		t.Fatalf("list artifacts again: %v", err)
	}
	if string(again[0].Inline) != "log" {
		t.Fatal("artifacts were not defensively copied")
	}
}

func TestMemoryStorePreparedContextDefensiveCopy(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.CreateRun(ctx, &Run{ID: "run-1", WorkspaceID: "workspace-1"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := store.SetPreparedContext(ctx, "workspace-1", "run-1", PreparedContext{
		Summary:        "prepared",
		Items:          []PreparedContextItem{{Kind: "workspace_snapshot", Access: PreparedContextAccessInjected}},
		AvailableTools: []string{"workspace_notes"},
	}); err != nil {
		t.Fatalf("set prepared context: %v", err)
	}

	got, err := store.GetRun(ctx, "workspace-1", "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	got.PreparedContext.Items[0].Kind = "changed"
	got.PreparedContext.AvailableTools[0] = "changed"

	again, err := store.GetRun(ctx, "workspace-1", "run-1")
	if err != nil {
		t.Fatalf("get run again: %v", err)
	}
	if again.PreparedContext.Items[0].Kind != "workspace_snapshot" || again.PreparedContext.AvailableTools[0] != "workspace_notes" {
		t.Fatalf("PreparedContext = %+v, want defensive copy", again.PreparedContext)
	}
}
