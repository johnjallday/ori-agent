package workspace

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

func TestComputeMapSummaryFields(t *testing.T) {
	settings := workspacesettings.DefaultSettings()
	settings.Workflow.Mode = "direct"

	ws := &Workspace{
		Kind: "group",
		AgentInstances: []AgentInstance{
			{Name: "Research Lead", EntryPoint: true},
			{Name: "Source Scout"},
		},
		MCPBindings:   []MCPBinding{{}, {}},
		SkillBindings: []SkillBinding{{}},
		Tasks: []Task{
			{Status: TaskStatusPending},
			{Status: TaskStatusInProgress},
			{Status: TaskStatusCompleted},
			{Status: TaskStatusFailed},
			{Status: TaskStatusTimeout},
			{Status: TaskStatusBacklog},
			{Status: TaskStatusBacklog},
		},
		SharedData: workspacesettings.Store(map[string]any{}, settings),
	}

	fields := ComputeMapSummaryFields(ws)

	if fields.EntryAgentName != "Research Lead" {
		t.Errorf("EntryAgentName = %q, want %q", fields.EntryAgentName, "Research Lead")
	}
	if fields.AgentCount != 2 {
		t.Errorf("AgentCount = %d, want 2", fields.AgentCount)
	}
	if fields.MCPCount != 2 {
		t.Errorf("MCPCount = %d, want 2", fields.MCPCount)
	}
	if fields.SkillCount != 1 {
		t.Errorf("SkillCount = %d, want 1", fields.SkillCount)
	}
	if fields.OpsMode != "direct" {
		t.Errorf("OpsMode = %q, want %q", fields.OpsMode, "direct")
	}
	if fields.OpenTaskCount != 2 {
		t.Errorf("OpenTaskCount = %d, want 2 (pending + in_progress, excludes completed and Backlog)", fields.OpenTaskCount)
	}
	if fields.BacklogCount != 2 {
		t.Errorf("BacklogCount = %d, want 2, tracked separately from OpenTaskCount (PRD workspace-backlog FR7, 40, 49)", fields.BacklogCount)
	}
	if fields.NeedsAttentionCount != 2 {
		t.Errorf("NeedsAttentionCount = %d, want 2 (failed + timeout)", fields.NeedsAttentionCount)
	}
	if !fields.Active {
		t.Error("Active = false, want true (workspace has an in-progress task)")
	}
}

func TestComputeMapSummaryFieldsNilWorkspace(t *testing.T) {
	fields := ComputeMapSummaryFields(nil)
	if fields.AgentNames != nil || fields.EntryAgentName != "" || fields.AgentCount != 0 ||
		fields.OpenTaskCount != 0 || fields.NeedsAttentionCount != 0 || fields.MCPCount != 0 ||
		fields.SkillCount != 0 || fields.OpsMode != "" || fields.Active {
		t.Errorf("ComputeMapSummaryFields(nil) = %+v, want zero value", fields)
	}
}

// Regression test for a gap found via a Group 7 cross-surface audit:
// GetWorkspaceProgress counted Backlog items in TotalTasks but the status
// switch had no matching bucket for them, silently skewing Percentage low
// for any workspace with Backlog items.
func TestGetWorkspaceProgress_ExcludesBacklogFromTotals(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Progress Test"})
	tasks := []Task{
		{Status: TaskStatusBacklog, Description: "idea 1"},
		{Status: TaskStatusBacklog, Description: "idea 2"},
		{Status: TaskStatusCompleted, Description: "done"},
		{Status: TaskStatusPending, Description: "todo"},
	}
	for _, task := range tasks {
		if err := ws.AddTask(task); err != nil {
			t.Fatalf("add task: %v", err)
		}
	}

	progress := ws.GetWorkspaceProgress()
	if progress.TotalTasks != 2 {
		t.Fatalf("TotalTasks = %d, want 2 (Backlog excluded)", progress.TotalTasks)
	}
	if progress.CompletedTasks != 1 {
		t.Fatalf("CompletedTasks = %d, want 1", progress.CompletedTasks)
	}
	if progress.PendingTasks != 1 {
		t.Fatalf("PendingTasks = %d, want 1", progress.PendingTasks)
	}
	if progress.Percentage != 50 {
		t.Fatalf("Percentage = %d, want 50 (1 of 2 non-Backlog tasks complete, not 1 of 4)", progress.Percentage)
	}
}
