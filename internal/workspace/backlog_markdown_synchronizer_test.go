package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newBacklogSyncTestStore(t *testing.T) (*FileStore, *Workspace) {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Alpha"})
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return store, ws
}

func backlogPathFor(t *testing.T, store *FileStore, ws *Workspace) string {
	t.Helper()
	folder, err := store.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	return BacklogMarkdownPath(folder, false)
}

func TestFileBacklogSynchronizer_EnsureAndRoundTrip(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)
	svc := NewBacklogService(store)
	svc.SetSynchronizer(sync)

	t.Run("EnsureBacklogMarkdownFile creates the file", func(t *testing.T) {
		collision, err := sync.EnsureBacklogMarkdownFile(ws.ID)
		if err != nil {
			t.Fatalf("EnsureBacklogMarkdownFile() error = %v", err)
		}
		if collision != nil {
			t.Fatalf("unexpected collision: %+v", collision)
		}
		path := backlogPathFor(t, store, ws)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("BACKLOG.md not created: %v", err)
		}
	})

	t.Run("idempotent: re-running does not duplicate or error", func(t *testing.T) {
		if _, err := sync.EnsureBacklogMarkdownFile(ws.ID); err != nil {
			t.Fatalf("second EnsureBacklogMarkdownFile() error = %v", err)
		}
	})

	t.Run("capture through the service renders the file", func(t *testing.T) {
		item, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "investigate flaky test", Priority: 1})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		path := backlogPathFor(t, store, ws)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read BACKLOG.md: %v", err)
		}
		if !strings.Contains(string(data), "investigate flaky test") {
			t.Fatalf("rendered file missing captured item:\n%s", data)
		}
		if !strings.Contains(string(data), "ori:id="+item.ID) {
			t.Fatalf("rendered file missing stable id:\n%s", data)
		}
	})
}

func TestFileBacklogSynchronizer_ImportNewRowsAndEdits(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)
	svc := NewBacklogService(store)
	svc.SetSynchronizer(sync)

	if _, err := sync.EnsureBacklogMarkdownFile(ws.ID); err != nil {
		t.Fatalf("EnsureBacklogMarkdownFile() error = %v", err)
	}
	path := backlogPathFor(t, store, ws)

	t.Run("a new file-authored row becomes a Backlog item", func(t *testing.T) {
		content := "---\ntype: ori_workspace_backlog\nworkspace_id: " + ws.ID + "\n---\n\n" +
			"## Backlog\n\n- a fresh idea from the file <!-- ori:priority=high tags=x,y -->\n\n" +
			"## Promote to Ready\n\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		result, err := sync.Import(ws.ID)
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}
		if !result.Changed {
			t.Fatalf("expected Changed=true for a new row")
		}
		items, err := svc.List(ws.ID, false)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(items) != 1 || items[0].Task.Description != "a fresh idea from the file" {
			t.Fatalf("new item not imported: %+v", items)
		}
		if items[0].Task.Priority != 1 {
			t.Fatalf("priority not imported: %+v", items[0].Task)
		}
		if !stringSlicesEqualUnordered(items[0].Task.Tags, []string{"x", "y"}) {
			t.Fatalf("tags not imported: %+v", items[0].Task.Tags)
		}
	})

	t.Run("re-importing the same unchanged file is a no-op", func(t *testing.T) {
		result, err := sync.Import(ws.ID)
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}
		if result.Changed {
			t.Fatalf("expected no-op re-import, got Changed=true")
		}
	})

	t.Run("editing a row's title in the file updates the task", func(t *testing.T) {
		items, err := svc.List(ws.ID, false)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		id := items[0].Task.ID
		content := "---\ntype: ori_workspace_backlog\nworkspace_id: " + ws.ID + "\n---\n\n" +
			"## Backlog\n\n- a RENAMED idea <!-- ori:id=" + id + " priority=high tags=x,y -->\n\n" +
			"## Promote to Ready\n\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		result, err := sync.Import(ws.ID)
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}
		if !result.Changed {
			t.Fatalf("expected Changed=true for an edited row")
		}
		updated, err := svc.Get(ws.ID, id)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if updated.Task.Description != "a RENAMED idea" {
			t.Fatalf("title not updated: %+v", updated.Task)
		}
	})
}

// TestFileBacklogSynchronizer_ReorderViaFile covers task-list 3.6 (PRD FR76):
// changing only the row order in BACKLOG.md (no field edits) must update
// each item's persistent BacklogRank.
func TestFileBacklogSynchronizer_ReorderViaFile(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)
	svc := NewBacklogService(store)
	svc.SetSynchronizer(sync)

	a, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	b, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "b"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	c, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "c"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	items, err := svc.List(ws.ID, false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if items[0].Task.ID != a.ID || items[1].Task.ID != b.ID || items[2].Task.ID != c.ID {
		t.Fatalf("unexpected initial order: %+v", items)
	}

	path := backlogPathFor(t, store, ws)
	content := "---\ntype: ori_workspace_backlog\nworkspace_id: " + ws.ID + "\n---\n\n" +
		"## Backlog\n\n" +
		"- c <!-- ori:id=" + c.ID + " -->\n" +
		"- a <!-- ori:id=" + a.ID + " -->\n" +
		"- b <!-- ori:id=" + b.ID + " -->\n\n" +
		"## Promote to Ready\n\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := sync.Import(ws.ID)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("expected Changed=true for a pure reorder")
	}

	reordered, err := svc.List(ws.ID, false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reordered) != 3 || reordered[0].Task.ID != c.ID || reordered[1].Task.ID != a.ID || reordered[2].Task.ID != b.ID {
		t.Fatalf("file order not applied to BacklogRank: %+v", reordered)
	}
}

func TestFileBacklogSynchronizer_PromoteViaFile(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)
	svc := NewBacklogService(store)
	svc.SetSynchronizer(sync)

	item, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "promote me via file"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path := backlogPathFor(t, store, ws)

	t.Run("moving an existing row to Promote to Ready promotes it", func(t *testing.T) {
		content := "---\ntype: ori_workspace_backlog\nworkspace_id: " + ws.ID + "\n---\n\n" +
			"## Backlog\n\n_Nothing saved for later._\n\n" +
			"## Promote to Ready\n\n- promote me via file <!-- ori:id=" + item.ID + " -->\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		result, err := sync.Import(ws.ID)
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}
		if !result.Changed {
			t.Fatalf("expected Changed=true for a promotion")
		}
		promoted, err := store.Get(ws.ID)
		if err != nil {
			t.Fatalf("Get workspace: %v", err)
		}
		task, err := promoted.GetTask(item.ID)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if task.Status != TaskStatusPending {
			t.Fatalf("Status = %q, want Pending (Ready)", task.Status)
		}
		if task.To != "" {
			t.Fatalf("To = %q, want unassigned", task.To)
		}
	})

	t.Run("promoted row leaves the file on next render", func(t *testing.T) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if strings.Contains(string(data), item.ID) {
			t.Fatalf("promoted item's row should have left BACKLOG.md:\n%s", data)
		}
	})

	t.Run("new ID-less row under Promote to Ready creates a direct Ready item", func(t *testing.T) {
		content := "---\ntype: ori_workspace_backlog\nworkspace_id: " + ws.ID + "\n---\n\n" +
			"## Backlog\n\n_Nothing saved for later._\n\n" +
			"## Promote to Ready\n\n- brand new ready item\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if _, err := sync.Import(ws.ID); err != nil {
			t.Fatalf("Import() error = %v", err)
		}
		reloaded, err := store.Get(ws.ID)
		if err != nil {
			t.Fatalf("Get workspace: %v", err)
		}
		found := false
		for _, task := range reloaded.Tasks {
			if task.Description == "brand new ready item" {
				found = true
				if task.Status != TaskStatusPending {
					t.Fatalf("Status = %q, want Pending", task.Status)
				}
				if task.To != "" {
					t.Fatalf("To = %q, want unassigned", task.To)
				}
			}
		}
		if !found {
			t.Fatalf("direct Ready item not created")
		}
	})
}

func TestFileBacklogSynchronizer_RemovedRowIsRestoredNotDeleted(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)
	svc := NewBacklogService(store)
	svc.SetSynchronizer(sync)

	item, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "do not delete me"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path := backlogPathFor(t, store, ws)

	// Simulate the user deleting the line entirely.
	content := "---\ntype: ori_workspace_backlog\nworkspace_id: " + ws.ID + "\n---\n\n" +
		"## Backlog\n\n_Nothing saved for later._\n\n## Promote to Ready\n\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := sync.Import(ws.ID)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected a restore-guidance warning")
	}

	still, err := svc.Get(ws.ID, item.ID)
	if err != nil {
		t.Fatalf("item was deleted through a file-side removal, want restore: %v", err)
	}
	if still.Task.Status != TaskStatusBacklog {
		t.Fatalf("Status = %q, want unchanged Backlog", still.Task.Status)
	}

	// The next render must bring the row back.
	if err := sync.RenderAfterMutation(ws.ID); err != nil {
		t.Fatalf("RenderAfterMutation() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(data), "do not delete me") {
		t.Fatalf("row was not restored on render:\n%s", data)
	}
}

func TestFileBacklogSynchronizer_SameItemConflict(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)
	svc := NewBacklogService(store)
	svc.SetSynchronizer(sync)

	item, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "original title"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path := backlogPathFor(t, store, ws)

	// Establish a synced baseline (render already happened via Create, but do
	// an explicit import to be sure the snapshot is recorded).
	if _, err := sync.Import(ws.ID); err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	// Ori-side edit made through a synchronizer-less service instance, so it
	// does NOT re-render/advance the last-synced snapshot — simulating a
	// mutation that has not yet been reconciled with the file (e.g. two
	// requests racing, or a render that hasn't landed yet). This is what
	// makes the scenario a genuine simultaneous divergence from the same
	// "original title" baseline, rather than the file trivially becoming the
	// newest known state.
	unsynced := NewBacklogService(store)
	newTitle := "ori-side edit"
	if _, err := unsynced.Update(ws.ID, item.ID, BacklogUpdateInput{Description: &newTitle}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Independently, the file also diverged from the same "original title"
	// baseline.
	content := "---\ntype: ori_workspace_backlog\nworkspace_id: " + ws.ID + "\n---\n\n" +
		"## Backlog\n\n- file-side edit <!-- ori:id=" + item.ID + " -->\n\n## Promote to Ready\n\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := sync.Import(ws.ID)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Changed {
		t.Fatalf("a conflicted item must not be silently applied either way")
	}

	conflicts := svc.Conflicts(ws.ID)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %+v", len(conflicts), conflicts)
	}
	c := conflicts[0]
	if c.ItemID != item.ID {
		t.Fatalf("conflict item id = %q, want %q", c.ItemID, item.ID)
	}
	if c.OriValue.Title != "ori-side edit" || c.FileValue.Title != "file-side edit" {
		t.Fatalf("both versions must be retained: %+v", c)
	}

	// The task itself must still hold the Ori-side value (no silent
	// last-write-wins toward the file).
	current, err := svc.Get(ws.ID, item.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if current.Task.Description != "ori-side edit" {
		t.Fatalf("task was mutated during conflict detection: %+v", current.Task)
	}

	t.Run("resolve with Use File applies the file version", func(t *testing.T) {
		if err := svc.ResolveConflict(ws.ID, item.ID, true); err != nil {
			t.Fatalf("ResolveConflict() error = %v", err)
		}
		resolved, err := svc.Get(ws.ID, item.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if resolved.Task.Description != "file-side edit" {
			t.Fatalf("Use File resolution not applied: %+v", resolved.Task)
		}
		if len(svc.Conflicts(ws.ID)) != 0 {
			t.Fatalf("conflict should be cleared after resolution")
		}
	})
}

func TestFileBacklogSynchronizer_ResolveConflictUseOri(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)
	svc := NewBacklogService(store)
	svc.SetSynchronizer(sync)

	item, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "keep me"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Manually seed a conflict record without going through the full
	// detection path, to test resolution in isolation.
	err = sync.persistSyncState(ws.ID, func(state *backlogMarkdownSyncState) {
		state.Conflicts = []BacklogSyncConflict{{
			ItemID:    item.ID,
			Title:     "keep me",
			OriValue:  backlogSyncItemSnapshot{Title: "keep me"},
			FileValue: backlogSyncItemSnapshot{Title: "file wanted this instead"},
		}}
	})
	if err != nil {
		t.Fatalf("seed conflict: %v", err)
	}

	if err := svc.ResolveConflict(ws.ID, item.ID, false); err != nil {
		t.Fatalf("ResolveConflict(useFile=false) error = %v", err)
	}
	current, err := svc.Get(ws.ID, item.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if current.Task.Description != "keep me" {
		t.Fatalf("Use Ori resolution should not change the task: %+v", current.Task)
	}
	if len(svc.Conflicts(ws.ID)) != 0 {
		t.Fatalf("conflict should be cleared after resolution")
	}
}

// TestFileBacklogSynchronizer_WriteFailureDoesNotLoseStructuredData covers
// FR88: a render failure must not roll back or lose an already-persisted
// structured mutation, and must persist an observable repair-needed status.
func TestFileBacklogSynchronizer_WriteFailureDoesNotLoseStructuredData(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)
	svc := NewBacklogService(store)
	svc.SetSynchronizer(sync)

	path := backlogPathFor(t, store, ws)
	// Force every future write to this path to fail by replacing it with a
	// directory: atomicWriteFile's rename onto path will fail.
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir to force write failure: %v", err)
	}

	item, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "survives the failed render"})
	if err != nil {
		t.Fatalf("Create() should succeed even though the render fails: %v", err)
	}

	// The structured mutation must be intact regardless of the render failure.
	current, err := svc.Get(ws.ID, item.ID)
	if err != nil {
		t.Fatalf("structured item lost after a render failure: %v", err)
	}
	if current.Task.Description != "survives the failed render" {
		t.Fatalf("unexpected task state: %+v", current.Task)
	}

	status := svc.SyncStatus(ws.ID)
	if status.Warning == "" {
		t.Fatalf("expected a repair-needed warning to be surfaced in sync status")
	}
}

func TestFileBacklogSynchronizer_UnmanagedFileCollision(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)

	path := backlogPathFor(t, store, ws)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	unmanagedContent := "# My own notes\n\nThis file is mine, not Ori's.\n"
	if err := os.WriteFile(path, []byte(unmanagedContent), 0644); err != nil {
		t.Fatalf("write unmanaged file: %v", err)
	}

	t.Run("EnsureBacklogMarkdownFile reports a collision instead of overwriting", func(t *testing.T) {
		collision, err := sync.EnsureBacklogMarkdownFile(ws.ID)
		if err != nil {
			t.Fatalf("EnsureBacklogMarkdownFile() error = %v", err)
		}
		if collision == nil {
			t.Fatalf("expected a collision result")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if string(data) != unmanagedContent {
			t.Fatalf("unmanaged file was modified: %s", data)
		}
	})

	t.Run("PreviewCollision returns the same result without writing", func(t *testing.T) {
		collision, err := sync.PreviewCollision(ws.ID)
		if err != nil {
			t.Fatalf("PreviewCollision() error = %v", err)
		}
		if collision == nil || !strings.Contains(collision.Preview, "This file is mine") {
			t.Fatalf("unexpected preview: %+v", collision)
		}
	})

	t.Run("ReplaceCollision explicitly overwrites", func(t *testing.T) {
		if err := sync.ReplaceCollision(ws.ID); err != nil {
			t.Fatalf("ReplaceCollision() error = %v", err)
		}
		collision, err := sync.PreviewCollision(ws.ID)
		if err != nil {
			t.Fatalf("PreviewCollision() error = %v", err)
		}
		if collision != nil {
			t.Fatalf("expected no more collision after replace, got %+v", collision)
		}
	})
}

func TestFileBacklogSynchronizer_AdoptCollision(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)
	svc := NewBacklogService(store)

	path := backlogPathFor(t, store, ws)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// An unmanaged file that happens to already look like a Backlog doc,
	// minus Ori's frontmatter (e.g. hand-authored before this feature).
	unmanagedContent := "## Backlog\n\n- an idea I wrote by hand\n"
	if err := os.WriteFile(path, []byte(unmanagedContent), 0644); err != nil {
		t.Fatalf("write unmanaged file: %v", err)
	}

	result, err := sync.AdoptCollision(ws.ID)
	if err != nil {
		t.Fatalf("AdoptCollision() error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("expected the hand-authored row to be adopted as a new item")
	}

	items, err := svc.List(ws.ID, false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].Task.Description != "an idea I wrote by hand" {
		t.Fatalf("adopted row not imported: %+v", items)
	}

	collision, err := sync.PreviewCollision(ws.ID)
	if err != nil {
		t.Fatalf("PreviewCollision() error = %v", err)
	}
	if collision != nil {
		t.Fatalf("file should now be Ori-managed after adoption, got collision %+v", collision)
	}
}

func TestFileBacklogSynchronizer_GroupWorkspacePlacement(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := NewWorkspace(CreateWorkspaceParams{Name: "Group HQ"})
	ws.Kind = groupWorkspaceKind
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	sync := NewFileBacklogSynchronizer(store)
	if _, err := sync.EnsureBacklogMarkdownFile(ws.ID); err != nil {
		t.Fatalf("EnsureBacklogMarkdownFile() error = %v", err)
	}

	folder, err := store.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	groupPath := filepath.Join(folder, FilesDir, BacklogMarkdownFileName)
	if _, err := os.Stat(groupPath); err != nil {
		t.Fatalf("expected BACKLOG.md under group files/ root at %s: %v", groupPath, err)
	}
	rootPath := filepath.Join(folder, BacklogMarkdownFileName)
	if _, err := os.Stat(rootPath); err == nil {
		t.Fatalf("BACKLOG.md must not also be written at the group folder root")
	}
}

func TestBackfillBacklogMarkdownForAllWorkspaces(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	wsA := NewWorkspace(CreateWorkspaceParams{Name: "A"})
	if err := store.Save(wsA); err != nil {
		t.Fatalf("save A: %v", err)
	}
	wsB := NewWorkspace(CreateWorkspaceParams{Name: "B"})
	if err := store.Save(wsB); err != nil {
		t.Fatalf("save B: %v", err)
	}

	written, errs := BackfillBacklogMarkdownForAllWorkspaces(store)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}

	t.Run("idempotent: rerunning writes the same files without error", func(t *testing.T) {
		written2, errs2 := BackfillBacklogMarkdownForAllWorkspaces(store)
		if len(errs2) != 0 {
			t.Fatalf("unexpected errors on rerun: %v", errs2)
		}
		if written2 != 2 {
			t.Fatalf("rerun written = %d, want 2", written2)
		}
	})
}
