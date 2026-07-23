package workspace

import (
	"testing"
)

func newBacklogTestWorkspace(t *testing.T, store Store, name string) *Workspace {
	t.Helper()
	ws := NewWorkspace(CreateWorkspaceParams{Name: name})
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return ws
}

// newBacklogTestStore returns a real FileStore (not InMemoryStore): FileStore
// deserializes a fresh copy from disk on every Get, which is what makes
// store.Update atomic (a failed mutation closure never reaches Save).
// InMemoryStore.Get returns the live cached pointer with no such guarantee,
// so it cannot be used to test atomicity.
func newBacklogTestStore(t *testing.T) Store {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestBacklogService_Create(t *testing.T) {
	store := newBacklogTestStore(t)
	svc := NewBacklogService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")

	t.Run("requires title", func(t *testing.T) {
		_, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID})
		if err == nil {
			t.Fatalf("expected error for missing title")
		}
	})

	t.Run("requires workspace_id", func(t *testing.T) {
		_, err := svc.Create(BacklogCreateInput{Description: "idea"})
		if err == nil {
			t.Fatalf("expected error for missing workspace_id")
		}
	})

	t.Run("captures a minimal item in Backlog", func(t *testing.T) {
		task, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "  investigate flaky test  "})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if task.Status != TaskStatusBacklog {
			t.Fatalf("Status = %q, want Backlog", task.Status)
		}
		if task.Description != "investigate flaky test" {
			t.Fatalf("Description = %q, want trimmed", task.Description)
		}
		if task.To != "" {
			t.Fatalf("To = %q, want unassigned", task.To)
		}
		if task.SourceType != BacklogSourceManual {
			t.Fatalf("SourceType = %q, want manual default", task.SourceType)
		}
		if task.Priority != 3 {
			t.Fatalf("Priority = %d, want default 3", task.Priority)
		}
	})

	t.Run("respects explicit source provenance", func(t *testing.T) {
		task, err := svc.Create(BacklogCreateInput{
			WorkspaceID: ws.ID, Description: "from action center",
			SourceType: BacklogSourceActionCenter, SourceID: "opp-1",
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if task.SourceType != BacklogSourceActionCenter || task.SourceID != "opp-1" {
			t.Fatalf("provenance not preserved: %+v", task)
		}
	})

	// Every capture surface (manual UI, workspace/Home assistant, Action
	// Center, BACKLOG.md import) funnels through this single Create() with
	// its own BacklogSource* constant as SourceType — proving here that every
	// source produces an equally safe, equally-shaped record is what makes
	// that funneling meaningful rather than incidental (PRD workspace-backlog
	// FR5, 20, 23-29; task 6.13).
	t.Run("every capture source produces an equivalent, safe Backlog record", func(t *testing.T) {
		sources := []string{
			BacklogSourceManual,
			BacklogSourceAssistant,
			BacklogSourceActionCenter,
			BacklogSourceBacklogFile,
		}
		for _, source := range sources {
			task, err := svc.Create(BacklogCreateInput{
				WorkspaceID: ws.ID,
				Description: "idea via " + source,
				SourceType:  source,
				SourceID:    "src-" + source,
			})
			if err != nil {
				t.Fatalf("Create() for source %q error = %v", source, err)
			}
			if task.Status != TaskStatusBacklog {
				t.Errorf("source %q: Status = %q, want Backlog", source, task.Status)
			}
			if task.SourceType != source {
				t.Errorf("source %q: SourceType not preserved, got %q", source, task.SourceType)
			}
			if task.SourceID != "src-"+source {
				t.Errorf("source %q: SourceID not preserved, got %q", source, task.SourceID)
			}
			if task.To != "" {
				t.Errorf("source %q: To = %q, want unassigned regardless of capture path", source, task.To)
			}
			if task.AwaitingExecutionIntent {
				t.Errorf("source %q: AwaitingExecutionIntent must not be set by capture alone", source)
			}
			if err := ValidateBacklogTaskInvariants(task); err != nil {
				t.Errorf("source %q: ValidateBacklogTaskInvariants failed: %v", source, err)
			}
		}
	})

	t.Run("deterministic increasing rank", func(t *testing.T) {
		a, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "first"})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		b, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "second"})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if b.BacklogRank <= a.BacklogRank {
			t.Fatalf("expected increasing rank: a=%d b=%d", a.BacklogRank, b.BacklogRank)
		}
	})
}

func TestBacklogService_List_LocalSortAndExclusions(t *testing.T) {
	store := newBacklogTestStore(t)
	svc := NewBacklogService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")

	// Two Backlog items out of rank order, one Ready item, one subtask.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("setup error: %v", err)
		}
	}
	must(ws.AddTask(Task{ID: "second", Status: TaskStatusBacklog, Description: "second", BacklogRank: 5}))
	must(ws.AddTask(Task{ID: "first", Status: TaskStatusBacklog, Description: "first", BacklogRank: 1}))
	must(ws.AddTask(Task{ID: "ready", Status: TaskStatusPending, Description: "ready item"}))
	must(ws.AddTask(Task{ID: "child", Status: TaskStatusBacklog, Description: "child", ParentTaskID: "first"}))
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	items, err := svc.List(ws.ID, false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 local Backlog items (excluding Ready + subtask), got %d: %+v", len(items), items)
	}
	if items[0].Task.ID != "first" || items[1].Task.ID != "second" {
		t.Fatalf("expected rank-ordered [first, second], got [%s, %s]", items[0].Task.ID, items[1].Task.ID)
	}
	for _, it := range items {
		if it.OwningWorkspaceID != ws.ID || it.OwningWorkspaceName != ws.Name {
			t.Fatalf("owning workspace identity missing: %+v", it)
		}
	}
}

func TestBacklogService_List_DescendantRollup(t *testing.T) {
	store := newBacklogTestStore(t)
	svc := NewBacklogService(store)
	parent := newBacklogTestWorkspace(t, store, "Parent")
	child := NewWorkspace(CreateWorkspaceParams{Name: "Child"})
	child.ParentID = parent.ID
	if err := store.Save(child); err != nil {
		t.Fatalf("save child: %v", err)
	}
	grandchild := NewWorkspace(CreateWorkspaceParams{Name: "Grandchild"})
	grandchild.ParentID = child.ID
	if err := store.Save(grandchild); err != nil {
		t.Fatalf("save grandchild: %v", err)
	}

	if err := parent.AddTask(Task{Status: TaskStatusBacklog, Description: "parent item"}); err != nil {
		t.Fatalf("add parent task: %v", err)
	}
	if err := store.Save(parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}
	if err := child.AddTask(Task{Status: TaskStatusBacklog, Description: "child item"}); err != nil {
		t.Fatalf("add child task: %v", err)
	}
	if err := store.Save(child); err != nil {
		t.Fatalf("save child: %v", err)
	}
	if err := grandchild.AddTask(Task{Status: TaskStatusBacklog, Description: "grandchild item"}); err != nil {
		t.Fatalf("add grandchild task: %v", err)
	}
	if err := store.Save(grandchild); err != nil {
		t.Fatalf("save grandchild: %v", err)
	}

	t.Run("without opt-in, only local items", func(t *testing.T) {
		items, err := svc.List(parent.ID, false)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(items) != 1 || items[0].Task.Description != "parent item" {
			t.Fatalf("expected only local item, got %+v", items)
		}
	})

	t.Run("with opt-in, includes all descendants and preserves ownership", func(t *testing.T) {
		items, err := svc.List(parent.ID, true)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(items) != 3 {
			t.Fatalf("expected 3 rolled-up items, got %d: %+v", len(items), items)
		}
		owners := map[string]string{}
		for _, it := range items {
			owners[it.Task.Description] = it.OwningWorkspaceID
		}
		if owners["parent item"] != parent.ID || owners["child item"] != child.ID || owners["grandchild item"] != grandchild.ID {
			t.Fatalf("ownership not preserved across roll-up: %+v", owners)
		}
	})
}

func TestBacklogService_Update(t *testing.T) {
	store := newBacklogTestStore(t)
	svc := NewBacklogService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")
	created, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "original"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Run("updates supported fields", func(t *testing.T) {
		newTitle := "revised title"
		newPriority := 1
		updated, err := svc.Update(ws.ID, created.ID, BacklogUpdateInput{
			Description: &newTitle,
			Priority:    &newPriority,
		})
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if updated.Description != newTitle || updated.Priority != newPriority {
			t.Fatalf("fields not updated: %+v", updated)
		}
	})

	t.Run("rejects empty title", func(t *testing.T) {
		empty := "   "
		if _, err := svc.Update(ws.ID, created.ID, BacklogUpdateInput{Description: &empty}); err == nil {
			t.Fatalf("expected error for empty title")
		}
	})

	t.Run("rejects update on a promoted item", func(t *testing.T) {
		promoted, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "will promote"})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := svc.Promote(ws.ID, promoted.ID); err != nil {
			t.Fatalf("Promote() error = %v", err)
		}
		newTitle := "should fail"
		if _, err := svc.Update(ws.ID, promoted.ID, BacklogUpdateInput{Description: &newTitle}); err == nil {
			t.Fatalf("expected error updating a promoted item through the Backlog service")
		}
	})
}

func TestBacklogService_Reorder(t *testing.T) {
	store := newBacklogTestStore(t)
	svc := NewBacklogService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")

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

	t.Run("reorders atomically and returns full ordering", func(t *testing.T) {
		result, err := svc.Reorder(ws.ID, []string{c.ID, a.ID, b.ID})
		if err != nil {
			t.Fatalf("Reorder() error = %v", err)
		}
		if len(result) != 3 || result[0].ID != c.ID || result[1].ID != a.ID || result[2].ID != b.ID {
			t.Fatalf("unexpected order: %+v", result)
		}
	})

	t.Run("invalid id fails the whole operation without partial writes", func(t *testing.T) {
		before, err := svc.List(ws.ID, false)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		_, err = svc.Reorder(ws.ID, []string{a.ID, "does-not-exist", b.ID})
		if err == nil {
			t.Fatalf("expected error for unknown id")
		}
		after, err := svc.List(ws.ID, false)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		for i := range before {
			if before[i].Task.ID != after[i].Task.ID || before[i].Task.BacklogRank != after[i].Task.BacklogRank {
				t.Fatalf("partial write occurred: before=%+v after=%+v", before, after)
			}
		}
	})
}

func TestBacklogService_Delete(t *testing.T) {
	store := newBacklogTestStore(t)
	svc := NewBacklogService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")

	item, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "to delete"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := svc.Delete(ws.ID, item.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	items, err := svc.List(ws.ID, false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected item removed, got %+v", items)
	}

	t.Run("rejects deleting a non-Backlog item", func(t *testing.T) {
		ready, err := svc.CreateReadyUnassigned(BacklogCreateInput{WorkspaceID: ws.ID, Description: "ready"})
		if err != nil {
			t.Fatalf("CreateReadyUnassigned() error = %v", err)
		}
		if err := svc.Delete(ws.ID, ready.ID); err == nil {
			t.Fatalf("expected error deleting a non-Backlog item through the Backlog service")
		}
	})
}

func TestBacklogService_Promote(t *testing.T) {
	store := newBacklogTestStore(t)
	svc := NewBacklogService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")

	item, err := svc.Create(BacklogCreateInput{
		WorkspaceID: ws.ID, Description: "promote me", Tags: []string{"x"}, Priority: 1,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	promoted, err := svc.Promote(ws.ID, item.ID)
	if err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	if promoted.Status != TaskStatusPending {
		t.Fatalf("Status = %q, want Pending (Ready)", promoted.Status)
	}
	if promoted.ID != item.ID || promoted.Description != item.Description || promoted.Priority != item.Priority {
		t.Fatalf("identity/metadata not preserved: %+v", promoted)
	}
	if promoted.BacklogRank != 0 {
		t.Fatalf("BacklogRank = %d, want cleared", promoted.BacklogRank)
	}
	if promoted.To != "" {
		t.Fatalf("To = %q, want unassigned (no automatic assignment)", promoted.To)
	}
	if !promoted.AwaitingExecutionIntent {
		t.Fatalf("expected AwaitingExecutionIntent to remain true (quiescent) after promotion")
	}

	t.Run("idempotent: promoting again returns the current item without erroring", func(t *testing.T) {
		again, err := svc.Promote(ws.ID, item.ID)
		if err != nil {
			t.Fatalf("Promote() second call error = %v", err)
		}
		if again.Status != TaskStatusPending {
			t.Fatalf("Status = %q, want unchanged Pending", again.Status)
		}
	})
}

func TestBacklogService_CreateReadyUnassigned(t *testing.T) {
	store := newBacklogTestStore(t)
	svc := NewBacklogService(store)
	// Give the workspace a resolvable single-agent coordinator to prove this
	// path truly bypasses entry-agent defaulting (FR2.10) rather than merely
	// having no coordinator to default to.
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Alpha", Agents: []string{"Solo"}})
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	task, err := svc.CreateReadyUnassigned(BacklogCreateInput{WorkspaceID: ws.ID, Description: "direct ready"})
	if err != nil {
		t.Fatalf("CreateReadyUnassigned() error = %v", err)
	}
	if task.Status != TaskStatusPending {
		t.Fatalf("Status = %q, want Pending", task.Status)
	}
	if task.To != "" {
		t.Fatalf("To = %q, want unassigned — entry-agent default must be bypassed", task.To)
	}
	if !task.AwaitingExecutionIntent {
		t.Fatalf("expected AwaitingExecutionIntent true (quiescent) for direct Ready creation")
	}
	if task.ScheduleEnabled {
		t.Fatalf("expected no schedule for direct Ready creation")
	}
}

// TestBacklogService_MutationsRouteToOwningWorkspaceOnly covers task-list
// 2.5: a roll-up view or a caller naming any other workspace ID must never
// be able to mutate an item — every mutation targets the item's actual
// owning workspace, never a parent's context (FR48-50, 60, 63-65).
func TestBacklogService_MutationsRouteToOwningWorkspaceOnly(t *testing.T) {
	store := newBacklogTestStore(t)
	svc := NewBacklogService(store)
	owner := newBacklogTestWorkspace(t, store, "Owner")
	other := newBacklogTestWorkspace(t, store, "Other")

	item, err := svc.Create(BacklogCreateInput{WorkspaceID: owner.ID, Description: "owned item"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	newTitle := "hijacked"
	if _, err := svc.Update(other.ID, item.ID, BacklogUpdateInput{Description: &newTitle}); err == nil {
		t.Fatalf("expected error updating an item through a non-owning workspace")
	}
	if err := svc.Delete(other.ID, item.ID); err == nil {
		t.Fatalf("expected error deleting an item through a non-owning workspace")
	}
	if _, err := svc.Promote(other.ID, item.ID); err == nil {
		t.Fatalf("expected error promoting an item through a non-owning workspace")
	}
	if _, err := svc.Get(other.ID, item.ID); err == nil {
		t.Fatalf("expected error reading an item through a non-owning workspace")
	}

	// The item must be entirely untouched by the rejected attempts.
	current, err := svc.Get(owner.ID, item.ID)
	if err != nil {
		t.Fatalf("Get() through the real owner failed: %v", err)
	}
	if current.Task.Description != "owned item" || current.Task.Status != TaskStatusBacklog {
		t.Fatalf("item was mutated despite rejected cross-workspace calls: %+v", current)
	}
}
