package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFileStoreResolveSlugTracksRenameAndDelete(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := NewWorkspace(CreateWorkspaceParams{Name: "Marketing Site"})
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	resolved, err := store.ResolveSlug("marketing-site")
	if err != nil {
		t.Fatalf("ResolveSlug: %v", err)
	}
	if resolved.ID != ws.ID {
		t.Fatalf("resolved ID = %q, want %q", resolved.ID, ws.ID)
	}
	if _, err := store.ResolveSlug(ws.ID); !errors.Is(err, ErrWorkspaceSlugNotFound) {
		t.Fatalf("UUID lookup error = %v, want ErrWorkspaceSlugNotFound", err)
	}

	if _, err := store.RenameWithSlug(ws.ID, "Campaign Site", "campaign-site"); err != nil {
		t.Fatalf("RenameWithSlug: %v", err)
	}
	if _, err := store.ResolveSlug("marketing-site"); !errors.Is(err, ErrWorkspaceSlugNotFound) {
		t.Fatalf("stale slug error = %v, want ErrWorkspaceSlugNotFound", err)
	}
	resolved, err = store.ResolveSlug("campaign-site")
	if err != nil || resolved.ID != ws.ID {
		t.Fatalf("new slug resolved (%#v, %v), want workspace %q", resolved, err, ws.ID)
	}

	if err := store.Delete(ws.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.ResolveSlug("campaign-site"); !errors.Is(err, ErrWorkspaceSlugNotFound) {
		t.Fatalf("deleted slug error = %v, want ErrWorkspaceSlugNotFound", err)
	}
}

func TestSyncStoreResolveSlugUsesPrimaryForDBOnlyWorkspace(t *testing.T) {
	primary := NewInMemoryStore()
	fileStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()

	ws := NewWorkspace(CreateWorkspaceParams{Name: "Database Only"})
	if err := primary.Save(ws); err != nil {
		t.Fatalf("primary Save: %v", err)
	}
	store := NewSyncStore(primary, fileStore)

	resolved, err := store.ResolveSlug("database-only")
	if err != nil {
		t.Fatalf("ResolveSlug: %v", err)
	}
	if resolved.ID != ws.ID {
		t.Fatalf("resolved ID = %q, want %q", resolved.ID, ws.ID)
	}
	if _, err := fileStore.ResolveSlug("database-only"); !errors.Is(err, ErrWorkspaceSlugNotFound) {
		t.Fatalf("file-only resolver unexpectedly found DB-only workspace: %v", err)
	}
}

func TestFileStoreReconcilesDuplicateSlugsDeterministically(t *testing.T) {
	root := t.TempDir()
	seedWorkspaceFolder(t, filepath.Join(root, "a-group"), &Workspace{ID: "group-a", Name: "A Group", FolderSlug: "a-group"})
	seedWorkspaceFolder(t, filepath.Join(root, "a-group", SubWorkspacesDir, "reports"), &Workspace{ID: "reports-a", Name: "Reports", FolderSlug: "reports", ParentID: "group-a"})
	seedWorkspaceFolder(t, filepath.Join(root, "b-group"), &Workspace{ID: "group-b", Name: "B Group", FolderSlug: "b-group"})
	seedWorkspaceFolder(t, filepath.Join(root, "b-group", SubWorkspacesDir, "reports"), &Workspace{ID: "reports-b", Name: "Reports", FolderSlug: "reports", ParentID: "group-b"})
	seedWorkspaceFolder(t, filepath.Join(root, "reports-2"), &Workspace{ID: "reports-existing", Name: "Existing Reports 2", FolderSlug: "reports-2"})

	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	first, err := store.ResolveSlug("reports")
	if err != nil || first.ID != "reports-a" {
		t.Fatalf("reports resolved (%#v, %v), want reports-a", first, err)
	}
	second, err := store.ResolveSlug("reports-3")
	if err != nil || second.ID != "reports-b" {
		t.Fatalf("reports-3 resolved (%#v, %v), want reports-b", second, err)
	}
	if _, err := os.Stat(filepath.Join(root, "b-group", SubWorkspacesDir, "reports-3", WorkspaceConfigFile)); err != nil {
		t.Fatalf("migrated nested folder missing: %v", err)
	}
	if _, err := store.ResolveSlug("reports-2"); err != nil {
		t.Fatalf("pre-existing reports-2 was not reserved: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	restarted, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("restart NewFileStore: %v", err)
	}
	defer func() { _ = restarted.Close() }()
	if _, err := restarted.ResolveSlug("reports-3"); err != nil {
		t.Fatalf("migration was not restart-idempotent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "b-group", SubWorkspacesDir, "reports-4")); !os.IsNotExist(err) {
		t.Fatalf("restart added another suffix, stat error = %v", err)
	}
}

func TestFileStoreImportPreflightsNestedGlobalSlugConflicts(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	existing := NewWorkspace(CreateWorkspaceParams{Name: "Reports"})
	if err := store.Save(existing); err != nil {
		t.Fatalf("Save existing: %v", err)
	}

	sourceRoot := filepath.Join(t.TempDir(), "bundle")
	seedWorkspaceFolder(t, sourceRoot, &Workspace{ID: "bundle-id", Name: "Bundle", FolderSlug: "bundle"})
	seedWorkspaceFolder(t, filepath.Join(sourceRoot, SubWorkspacesDir, "reports"), &Workspace{ID: "nested-reports", Name: "Reports", FolderSlug: "reports"})
	_, _, err = store.Import(sourceRoot)
	var conflict *FolderSlugConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Import error = %v, want nested FolderSlugConflictError", err)
	}
	if _, err := store.Get("bundle-id"); err == nil {
		t.Fatal("failed import registered its root before nested slug validation")
	}
}

func TestFileStoreRootSwitchReplacesSlugIndex(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	store, err := NewFileStore(rootA)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	alpha := NewWorkspace(CreateWorkspaceParams{Name: "Alpha"})
	if err := store.Save(alpha); err != nil {
		t.Fatalf("Save alpha: %v", err)
	}
	seedWorkspaceFolder(t, filepath.Join(rootB, "beta"), &Workspace{ID: "beta-id", Name: "Beta", FolderSlug: "beta"})

	if _, err := store.SetBasePath(rootB); err != nil {
		t.Fatalf("SetBasePath: %v", err)
	}
	if _, err := store.ResolveSlug("alpha"); !errors.Is(err, ErrWorkspaceSlugNotFound) {
		t.Fatalf("old-root slug error = %v, want ErrWorkspaceSlugNotFound", err)
	}
	resolved, err := store.ResolveSlug("beta")
	if err != nil || resolved.ID != "beta-id" {
		t.Fatalf("new-root slug resolved (%#v, %v), want beta-id", resolved, err)
	}
}

func TestFileStoreExternalWorkspaceRestoreReentersGlobalSlugValidation(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	first := NewWorkspace(CreateWorkspaceParams{Name: "External Reports"})
	first.FolderSlug = "reports"
	if err := store.SaveAt(first, t.TempDir()); err != nil {
		t.Fatalf("SaveAt(first): %v", err)
	}
	originalPath, _, err := store.Trash(first.ID)
	if err != nil {
		t.Fatalf("Trash(first): %v", err)
	}

	second := NewWorkspace(CreateWorkspaceParams{Name: "Replacement Reports"})
	second.FolderSlug = "reports"
	if err := store.SaveAt(second, t.TempDir()); err != nil {
		t.Fatalf("SaveAt(second): %v", err)
	}
	_, err = store.RestoreFromTrash(originalPath, "")
	var conflict *FolderSlugConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("RestoreFromTrash error = %v, want FolderSlugConflictError", err)
	}
	if conflict.SuggestedSlug != "reports-2" {
		t.Fatalf("restore suggestion = %q, want reports-2", conflict.SuggestedSlug)
	}
}

func TestFileStoreDuplicateMigrationResumesPartialAssignments(t *testing.T) {
	root := t.TempDir()
	seedWorkspaceFolder(t, filepath.Join(root, "a-group"), &Workspace{ID: "group-a", Name: "A Group", FolderSlug: "a-group"})
	seedWorkspaceFolder(t, filepath.Join(root, "a-group", SubWorkspacesDir, "reports"), &Workspace{ID: "reports-a", Name: "Reports", FolderSlug: "reports"})
	seedWorkspaceFolder(t, filepath.Join(root, "b-group"), &Workspace{ID: "group-b", Name: "B Group", FolderSlug: "b-group"})
	seedWorkspaceFolder(t, filepath.Join(root, "b-group", SubWorkspacesDir, "reports-3"), &Workspace{ID: "reports-b", Name: "Reports", FolderSlug: "reports-3"})
	seedWorkspaceFolder(t, filepath.Join(root, "c-group"), &Workspace{ID: "group-c", Name: "C Group", FolderSlug: "c-group"})
	seedWorkspaceFolder(t, filepath.Join(root, "c-group", SubWorkspacesDir, "reports"), &Workspace{ID: "reports-c", Name: "Reports", FolderSlug: "reports"})
	seedWorkspaceFolder(t, filepath.Join(root, "reports-2"), &Workspace{ID: "reports-existing", Name: "Existing Reports 2", FolderSlug: "reports-2"})

	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	resolved, err := store.ResolveSlug("reports-4")
	if err != nil || resolved.ID != "reports-c" {
		t.Fatalf("resumed reports-4 resolved (%#v, %v), want reports-c", resolved, err)
	}
	if resolved, err := store.ResolveSlug("reports-3"); err != nil || resolved.ID != "reports-b" {
		t.Fatalf("partial reports-3 assignment changed: (%#v, %v)", resolved, err)
	}
}

func TestFileStoreGlobalSlugSuggestionRespectsMaxLength(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	base := strings.Repeat("a", MaxSlugLength)
	first := NewWorkspace(CreateWorkspaceParams{Name: base})
	if err := store.Save(first); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	second := NewWorkspace(CreateWorkspaceParams{Name: base})
	err = store.Save(second)
	var conflict *FolderSlugConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate error = %v, want FolderSlugConflictError", err)
	}
	if len(conflict.SuggestedSlug) > MaxSlugLength || !strings.HasSuffix(conflict.SuggestedSlug, "-2") {
		t.Fatalf("suggested slug = %q, want <= %d chars ending in -2", conflict.SuggestedSlug, MaxSlugLength)
	}
}

func seedWorkspaceFolder(t *testing.T, folder string, ws *Workspace) {
	t.Helper()
	if ws.CreatedAt.IsZero() {
		ws.CreatedAt = time.Unix(1, 0).UTC()
	}
	if ws.UpdatedAt.IsZero() {
		ws.UpdatedAt = ws.CreatedAt
	}
	if err := os.MkdirAll(folder, 0o750); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Base(folder), err)
	}
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON(%s): %v", ws.ID, err)
	}
	if err := os.WriteFile(filepath.Join(folder, WorkspaceConfigFile), data, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", ws.ID, err)
	}
}

func TestFileStoreResolveSlugIsSafeDuringConcurrentWrites(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	const count = 24
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := "Concurrent " + WorkspaceSlugWithSuffix("workspace", i+2)
			ws := NewWorkspace(CreateWorkspaceParams{Name: name})
			if err := store.Save(ws); err != nil {
				errs <- err
				return
			}
			resolved, err := store.ResolveSlug(ws.FolderSlug)
			if err != nil {
				errs <- err
				return
			}
			if resolved.ID != ws.ID {
				errs <- errors.New("resolver returned a different workspace")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent save/resolve: %v", err)
	}
}

func TestInMemoryStoreResolveSlugRejectsMalformedAndInactive(t *testing.T) {
	store := NewInMemoryStore()
	active := NewWorkspace(CreateWorkspaceParams{Name: "Road Map"})
	if err := store.Save(active); err != nil {
		t.Fatalf("Save active: %v", err)
	}
	if _, err := store.ResolveSlug("Road Map"); !errors.Is(err, ErrWorkspaceSlugNotFound) {
		t.Fatalf("malformed slug error = %v, want ErrWorkspaceSlugNotFound", err)
	}

	active.Status = StatusTrashed
	if err := store.Save(active); err != nil {
		t.Fatalf("Save trashed: %v", err)
	}
	if _, err := store.ResolveSlug("road-map"); !errors.Is(err, ErrWorkspaceSlugNotFound) {
		t.Fatalf("trashed slug error = %v, want ErrWorkspaceSlugNotFound", err)
	}
}
