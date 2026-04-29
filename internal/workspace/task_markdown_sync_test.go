package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

func TestRenderWorkspaceTasksMarkdown_NestedMultiAgentTasks(t *testing.T) {
	ws := &Workspace{
		ID:   "workspace-1",
		Name: "Markdown Workspace",
		Tasks: []Task{
			{
				ID:                "parent-1",
				WorkspaceID:       "workspace-1",
				Description:       "Prepare launch report",
				To:                "Coordinator",
				Status:            TaskStatusPending,
				OrchestrationMode: TaskOrchestrationModeGraph,
				CreatedAt:         time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC),
			},
			{
				ID:           "child-1",
				WorkspaceID:  "workspace-1",
				Description:  "Collect market data",
				To:           "Researcher",
				Status:       TaskStatusPending,
				ParentTaskID: "parent-1",
				SubtaskIndex: 1,
				CreatedAt:    time.Date(2026, 4, 28, 10, 1, 0, 0, time.UTC),
			},
			{
				ID:           "child-2",
				WorkspaceID:  "workspace-1",
				Description:  "Draft summary",
				To:           "Writer",
				Status:       TaskStatusCompleted,
				ParentTaskID: "parent-1",
				SubtaskIndex: 2,
				InputTaskIDs: []string{"child-1"},
				CreatedAt:    time.Date(2026, 4, 28, 10, 2, 0, 0, time.UTC),
			},
		},
	}

	rendered := RenderWorkspaceTasksMarkdown(ws)
	for _, want := range []string{
		"type: ori_workspace_tasks",
		"workspace_id: workspace-1",
		"- [ ] Prepare launch report @coordinator <!-- ori:id=parent-1 mode=graph to=Coordinator",
		"  - [ ] Collect market data @researcher <!-- ori:id=child-1 parent=parent-1 index=1 to=Researcher",
		"  - [x] Draft summary @writer <!-- ori:id=child-2 parent=parent-1 index=2 depends=child-1 to=Writer",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered markdown to contain %q:\n%s", want, rendered)
		}
	}
}

func TestParseWorkspaceTasksMarkdown_GeneratedFileDoesNotWarn(t *testing.T) {
	ws := &Workspace{
		ID:   "workspace-1",
		Name: "Markdown Workspace",
		Tasks: []Task{
			{
				ID:          "task-1",
				WorkspaceID: "workspace-1",
				Description: "Prepare launch report",
				To:          "Coordinator",
				Status:      TaskStatusPending,
				CreatedAt:   time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC),
			},
		},
	}

	items, warnings, err := ParseWorkspaceTasksMarkdown(RenderWorkspaceTasksMarkdown(ws), ws.ID)
	if err != nil {
		t.Fatalf("ParseWorkspaceTasksMarkdown: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected generated markdown to parse without warnings, got %#v", warnings)
	}
	if len(items) != 1 || items[0].ID != "task-1" {
		t.Fatalf("expected generated task item, got %#v", items)
	}
}

func TestParseWorkspaceTasksMarkdown_ParsesNestedMetadata(t *testing.T) {
	content := `---
type: ori_workspace_tasks
schema_version: 1
workspace_id: workspace-1
---

# Tasks

## Active

- [ ] Prepare launch report @coordinator <!-- ori:id=parent-1 mode=graph to=Coordinator -->
  - [x] Collect market data @researcher <!-- ori:id=child-1 parent=parent-1 index=1 depends=input-1 to=Researcher assigned_node_id=researcher-node-1 -->
- [ ] Reassign review @Writer <!-- ori:id=task-2 to=Researcher -->
`

	items, warnings, err := ParseWorkspaceTasksMarkdown(content, "workspace-1")
	if err != nil {
		t.Fatalf("ParseWorkspaceTasksMarkdown: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", warnings)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %#v", items)
	}
	if items[1].ID != "child-1" || !items[1].Checked {
		t.Fatalf("expected checked child-1, got %#v", items[1])
	}
	if items[1].ParentTaskID != "parent-1" || items[1].SubtaskIndex != 1 {
		t.Fatalf("expected parent metadata, got %#v", items[1])
	}
	if items[1].To != "Researcher" || items[1].AssignedNodeID != "researcher-node-1" {
		t.Fatalf("expected assignment metadata, got %#v", items[1])
	}
	if len(items[1].InputTaskIDs) != 1 || items[1].InputTaskIDs[0] != "input-1" {
		t.Fatalf("expected dependency metadata, got %#v", items[1].InputTaskIDs)
	}
	if items[2].To != "Writer" {
		t.Fatalf("expected visible assignee to override stale metadata, got %#v", items[2])
	}
}

func TestImportTaskMarkdownFromStore_UpdatesStatusAndPreservesRuntimeFields(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer store.Close()

	ws := &Workspace{
		ID:         "workspace-1",
		Name:       "Markdown Import",
		FolderSlug: "markdown-import",
		SharedData: workspacesettings.Store(nil, workspacesettings.Settings{
			TaskMarkdown: workspacesettings.TaskMarkdownSettings{
				Enabled:            true,
				Path:               "tasks.md",
				GenerateAgentViews: false,
			},
		}),
		Tasks: []Task{
			{
				ID:          "task-1",
				WorkspaceID: "workspace-1",
				Description: "Old title",
				To:          "Researcher",
				Status:      TaskStatusPending,
				Result:      "keep this result",
				CreatedAt:   time.Now(),
			},
		},
		Status:    StatusActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, "markdown-import", "tasks.md")
	content := `---
type: ori_workspace_tasks
schema_version: 1
workspace_id: workspace-1
---

# Tasks

## Active

- [x] New title @writer <!-- ori:id=task-1 to=Writer -->
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write tasks.md: %v", err)
	}

	loaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	result, err := ImportTaskMarkdownFromStore(store, loaded)
	if err != nil {
		t.Fatalf("ImportTaskMarkdownFromStore: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected import to change workspace")
	}
	task, err := loaded.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Description != "New title" || task.To != "Writer" {
		t.Fatalf("expected imported title and assignee, got %#v", task)
	}
	if task.Status != TaskStatusCompleted {
		t.Fatalf("expected completed status, got %q", task.Status)
	}
	if task.Result != "keep this result" {
		t.Fatalf("expected runtime result preserved, got %q", task.Result)
	}
}

func TestImportTaskMarkdownFromStore_WarnsWhenWorkspaceAndMarkdownChanged(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer store.Close()

	ws := &Workspace{
		ID:         "workspace-1",
		Name:       "Conflict Import",
		FolderSlug: "conflict-import",
		SharedData: workspacesettings.Store(nil, workspacesettings.Settings{
			TaskMarkdown: workspacesettings.TaskMarkdownSettings{
				Enabled:            true,
				Path:               "tasks.md",
				GenerateAgentViews: false,
			},
		}),
		Tasks: []Task{
			{
				ID:          "task-1",
				WorkspaceID: "workspace-1",
				Description: "Original title",
				Status:      TaskStatusPending,
				CreatedAt:   time.Now(),
			},
		},
		Status:    StatusActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	loaded.UpdatedAt = time.Now().Add(2 * time.Minute)
	path := filepath.Join(dir, "conflict-import", "tasks.md")
	content := `---
type: ori_workspace_tasks
schema_version: 1
workspace_id: workspace-1
updated_at: "2026-04-28T10:00:00Z"
markdown_sync:
  last_synced_at: "2026-04-28T10:00:00Z"
  content_hash: "sha256:0000"
---

# Tasks

## Active

- [ ] Edited title <!-- ori:id=task-1 -->
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write tasks.md: %v", err)
	}

	result, err := ImportTaskMarkdownFromStore(store, loaded)
	if err != nil {
		t.Fatalf("ImportTaskMarkdownFromStore: %v", err)
	}
	if len(result.Warnings) < 2 {
		t.Fatalf("expected hash and conflict warnings, got %#v", result.Warnings)
	}
}

func TestSyncTaskMarkdownFilesToFolder_WritesCanonicalAndAgentViews(t *testing.T) {
	folder := t.TempDir()
	ws := &Workspace{
		ID:             "workspace-1",
		Name:           "Markdown Sync",
		AgentInstances: []AgentInstance{{Name: "Researcher", NodeID: "researcher-node-1"}},
		Tasks: []Task{
			{
				ID:          "task-1",
				WorkspaceID: "workspace-1",
				Description: "Collect market data",
				To:          "Researcher",
				Status:      TaskStatusPending,
				CreatedAt:   time.Now(),
			},
		},
	}

	err := SyncTaskMarkdownFilesToFolder(folder, ws, workspacesettings.TaskMarkdownSettings{
		Enabled:            true,
		Path:               "tasks.md",
		GenerateAgentViews: true,
	})
	if err != nil {
		t.Fatalf("SyncTaskMarkdownFilesToFolder: %v", err)
	}

	canonical, err := os.ReadFile(filepath.Join(folder, "tasks.md"))
	if err != nil {
		t.Fatalf("read canonical tasks.md: %v", err)
	}
	if !strings.Contains(string(canonical), "Collect market data") {
		t.Fatalf("expected canonical task content, got:\n%s", string(canonical))
	}

	agentView, err := os.ReadFile(filepath.Join(folder, "agents", "researcher", "tasks.md"))
	if err != nil {
		t.Fatalf("read agent tasks.md: %v", err)
	}
	if !strings.Contains(string(agentView), "Generated by Ori") || !strings.Contains(string(agentView), "Collect market data") {
		t.Fatalf("expected generated agent task content, got:\n%s", string(agentView))
	}
}

func TestFileStoreSave_SyncsTaskMarkdownAcrossTaskLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer store.Close()

	ws := &Workspace{
		ID:         "workspace-1",
		Name:       "Lifecycle Sync",
		FolderSlug: "lifecycle-sync",
		SharedData: workspacesettings.Store(nil, workspacesettings.Settings{
			TaskMarkdown: workspacesettings.TaskMarkdownSettings{
				Enabled:            true,
				Path:               "tasks.md",
				GenerateAgentViews: false,
			},
		}),
		Status:    StatusActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := ws.AddTask(Task{
		ID:          "task-1",
		WorkspaceID: ws.ID,
		Description: "Create draft",
		Status:      TaskStatusPending,
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save create: %v", err)
	}
	taskPath := filepath.Join(dir, "lifecycle-sync", "tasks.md")
	assertFileContains(t, taskPath, "- [ ] Create draft")

	task, err := ws.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.Description = "Create final draft"
	task.Status = TaskStatusCompleted
	now := time.Now()
	task.CompletedAt = &now
	if err := ws.UpdateTask(*task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	assertFileContains(t, taskPath, "- [x] Create final draft")

	if err := ws.DeleteTask("task-1"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save delete: %v", err)
	}
	content, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatalf("read tasks.md: %v", err)
	}
	if strings.Contains(string(content), "Create final draft") {
		t.Fatalf("expected deleted task removed from tasks.md, got:\n%s", string(content))
	}
}

func assertFileContains(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(content), expected) {
		t.Fatalf("expected %s to contain %q, got:\n%s", path, expected, string(content))
	}
}
