package workspace_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/testutil/testdb"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Exercise the same contract against the test double and actual persistence
// compositions. No application HOME, provider credentials, or server is used.
func contractStores() map[string]func(*testing.T) workspace.Store {
	return map[string]func(*testing.T) workspace.Store{
		"memory": func(*testing.T) workspace.Store { return workspace.NewInMemoryStore() },
		"file":   func(t *testing.T) workspace.Store { return contractFileStore(t) },
		"sqlite": func(t *testing.T) workspace.Store { return contractSQLiteStore(t) },
		"sqlite_with_folder_mirror": func(t *testing.T) workspace.Store {
			return workspace.NewSyncStore(contractSQLiteStore(t), contractFileStore(t))
		},
	}
}

func contractFileStore(t *testing.T) *workspace.FileStore {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func contractSQLiteStore(t *testing.T) workspace.Store {
	t.Helper()
	owner := session.NewHybridStoreWithDB(testdb.Open(t), 10)
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	return session.NewWorkspaceStoreAdapter(owner)
}

func contractSeed(t *testing.T, store workspace.Store) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Store Contract"})
	ws.FolderSlug = "store-contract"
	ws.Tags = []string{"original"}
	ws.SharedData = map[string]any{"label": "original"}
	ws.Tasks = []workspace.Task{{
		ID: "task-1", WorkspaceID: ws.ID, Status: workspace.TaskStatusPending,
		Context: map[string]any{"label": "original"},
	}}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return ws
}

func contractGet(t *testing.T, store workspace.Store, id string) *workspace.Workspace {
	t.Helper()
	ws, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func assertContractUnchanged(t *testing.T, store workspace.Store, id string) {
	t.Helper()
	got := contractGet(t, store, id)
	if got.Name != "Store Contract" || got.FolderSlug != "store-contract" || got.Tags[0] != "original" ||
		got.SharedData["label"] != "original" || got.Tasks[0].Context["label"] != "original" {
		t.Errorf("an uncommitted mutation reached storage: name=%q, slug=%q, tags=%v, shared=%v, task context=%v",
			got.Name, got.FolderSlug, got.Tags, got.SharedData, got.Tasks[0].Context)
	}
}

func mutateContractSnapshot(ws *workspace.Workspace) {
	ws.Name = "Changed"
	ws.Tags[0] = "changed"
	ws.SharedData["label"] = "changed"
	ws.Tasks[0].Context["label"] = "changed"
}

func TestWorkspaceStoreContract(t *testing.T) {
	for name, create := range contractStores() {
		t.Run(name, func(t *testing.T) {
			t.Run("save_detaches_caller", func(t *testing.T) {
				store := create(t)
				seed := contractSeed(t, store)
				mutateContractSnapshot(seed)
				assertContractUnchanged(t, store, seed.ID)
			})
			t.Run("get_returns_independent_snapshots", func(t *testing.T) {
				store := create(t)
				seed := contractSeed(t, store)
				first := contractGet(t, store, seed.ID)
				second := contractGet(t, store, seed.ID)
				mutateContractSnapshot(first)
				if second.Name != "Store Contract" || second.Tasks[0].Context["label"] != "original" {
					t.Error("two reads share mutable workspace data")
				}
				assertContractUnchanged(t, store, seed.ID)
			})
			t.Run("slug_lookup_returns_snapshot", func(t *testing.T) {
				store := create(t)
				seed := contractSeed(t, store)
				resolver, ok := store.(workspace.SlugResolver)
				if !ok {
					t.Fatal("fixture does not support slug lookup")
				}
				got, err := resolver.ResolveSlug(seed.FolderSlug)
				if err != nil {
					t.Fatal(err)
				}
				mutateContractSnapshot(got)
				assertContractUnchanged(t, store, seed.ID)
			})
			t.Run("active_listing_detaches_metadata", func(t *testing.T) {
				store := create(t)
				seed := contractSeed(t, store)
				listed, err := store.ListActive()
				if err != nil || len(listed) != 1 {
					t.Fatalf("ListActive: count=%d, err=%v", len(listed), err)
				}
				// SQLite's listing intentionally omits tasks and shared data.
				listed[0].Name = "Changed"
				listed[0].Tags[0] = "changed"
				assertContractUnchanged(t, store, seed.ID)
			})
			t.Run("failed_callback_leaves_storage_unchanged", func(t *testing.T) {
				store := create(t)
				seed := contractSeed(t, store)
				failure := errors.New("mutation rejected")
				err := store.Update(seed.ID, func(ws *workspace.Workspace) error {
					mutateContractSnapshot(ws)
					return failure
				})
				if !errors.Is(err, failure) {
					t.Fatalf("Update returned %v, want callback error", err)
				}
				assertContractUnchanged(t, store, seed.ID)
			})
			t.Run("failed_save_leaves_storage_unchanged", func(t *testing.T) {
				store := create(t)
				seed := contractSeed(t, store)
				other := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Reserved"})
				other.FolderSlug = "reserved"
				if err := store.Save(other); err != nil {
					t.Fatal(err)
				}
				err := store.Update(seed.ID, func(ws *workspace.Workspace) error {
					mutateContractSnapshot(ws)
					ws.FolderSlug = other.FolderSlug
					return nil
				})
				if err == nil {
					t.Fatal("expected conflicting slug to reject Save")
				}
				assertContractUnchanged(t, store, seed.ID)
			})
			t.Run("successful_update_commits_and_detaches_callback", func(t *testing.T) {
				store := create(t)
				seed := contractSeed(t, store)
				before := contractGet(t, store, seed.ID)
				var edited *workspace.Workspace
				if err := store.Update(seed.ID, func(ws *workspace.Workspace) error {
					mutateContractSnapshot(ws)
					edited = ws
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				edited.Name = "Mutation after commit"
				if got := contractGet(t, store, seed.ID); got.Name != "Changed" || got.Tasks[0].Context["label"] != "changed" {
					t.Errorf("successful update was not committed independently: %q, %v", got.Name, got.Tasks[0].Context)
				}
				if before.Name != "Store Contract" || before.Tasks[0].Context["label"] != "original" {
					t.Error("committed update changed an earlier read")
				}
			})
			t.Run("concurrent_reads_are_detached_from_updates", func(t *testing.T) {
				store := create(t)
				seed := contractSeed(t, store)
				const writers = 8
				var wg sync.WaitGroup
				for range writers {
					wg.Go(func() {
						if err := store.Update(seed.ID, func(ws *workspace.Workspace) error {
							ws.Tasks[0].FailureCount++
							return nil
						}); err != nil {
							t.Error(err)
						}
					})
					wg.Go(func() {
						read, err := store.Get(seed.ID)
						if err != nil {
							t.Error(err)
							return
						}
						mutateContractSnapshot(read)
					})
				}
				wg.Wait()
				assertContractUnchanged(t, store, seed.ID)
				if got := contractGet(t, store, seed.ID).Tasks[0].FailureCount; got != writers {
					t.Errorf("committed %d increments, want %d", got, writers)
				}
			})
			t.Run("concurrent_updates_do_not_lose_changes", func(t *testing.T) {
				store := create(t)
				seed := contractSeed(t, store)
				const writers = 8
				var wg sync.WaitGroup
				for range writers {
					wg.Go(func() {
						if err := store.Update(seed.ID, func(ws *workspace.Workspace) error {
							ws.Tasks[0].FailureCount++
							return nil
						}); err != nil {
							t.Error(err)
						}
					})
				}
				wg.Wait()
				if got := contractGet(t, store, seed.ID).Tasks[0].FailureCount; got != writers {
					t.Errorf("committed %d increments, want %d", got, writers)
				}
			})
		})
	}
}
