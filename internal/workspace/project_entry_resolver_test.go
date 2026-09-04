package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectEntryLocatorPreservesLegacyManagedCompatibility(t *testing.T) {
	shared := map[string]any{ProjectEntryPathKey: "song/main.rpp"}
	locator, err := GetProjectEntryLocator(shared)
	if err != nil || locator == nil || locator.Kind != ProjectEntryManagedWorkspace || locator.RelativePath != "song/main.rpp" {
		t.Fatalf("legacy locator = %#v err=%v", locator, err)
	}
	if _, exists := shared[ProjectEntryLocatorKey]; exists {
		t.Fatal("legacy read mutated persisted data")
	}
	if err := SetProjectEntryPath(shared, "main.rpp"); err != nil {
		t.Fatal(err)
	}
	if shared[ProjectEntryPathKey] != "main.rpp" {
		t.Fatalf("legacy projection missing: %#v", shared)
	}
	wantTyped := map[string]any{
		"schema_version": ProjectEntryLocatorSchemaVersion,
		"kind":           string(ProjectEntryManagedWorkspace), "relative_path": "main.rpp",
	}
	if !reflect.DeepEqual(shared[ProjectEntryLocatorKey], wantTyped) {
		t.Fatalf("typed projection = %#v", shared[ProjectEntryLocatorKey])
	}
	shared[ProjectEntryPathKey] = "different.rpp"
	if _, err := GetProjectEntryLocator(shared); !errors.Is(err, ErrInvalidProjectEntryPath) {
		t.Fatalf("typed/legacy confusion error = %v", err)
	}
}

func TestDirectoryReferenceLocatorNeverPersistsAbsolutePathOrLegacyFallback(t *testing.T) {
	shared := map[string]any{ProjectEntryPathKey: "old.rpp"}
	if err := SetProjectEntryLocator(shared, ProjectEntryLocator{
		SchemaVersion: ProjectEntryLocatorSchemaVersion,
		Kind:          ProjectEntryDirectoryReference, DirectoryReferenceID: "reference-1",
		RelativePath: "Songs/main.rpp",
	}); err != nil {
		t.Fatal(err)
	}
	if _, exists := shared[ProjectEntryPathKey]; exists {
		t.Fatal("directory reference retained ambiguous legacy path")
	}
	locator, err := GetProjectEntryLocator(shared)
	if err != nil || locator.DirectoryReferenceID != "reference-1" {
		t.Fatalf("typed external locator = %#v err=%v", locator, err)
	}
	if _, err := GetProjectEntryPath(shared); !errors.Is(err, ErrInvalidProjectEntryPath) {
		t.Fatalf("legacy-only reader accepted external locator: %v", err)
	}
	encoded, err := json.Marshal(shared)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("absolute_path")) || bytes.Contains(encoded, []byte("source_path")) {
		t.Fatalf("locator persistence exposed an absolute-path field: %s", encoded)
	}
}

func TestResolveProjectEntryManagedAndDirectoryReference(t *testing.T) {
	workspaceRoot := t.TempDir()
	managedRoot := filepath.Join(workspaceRoot, "song")
	if err := os.Mkdir(managedRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	managedEntry := filepath.Join(managedRoot, "main.rpp")
	if err := os.WriteFile(managedEntry, []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := NewWorkspace(CreateWorkspaceParams{Name: "Managed"})
	managed.ID = "managed-workspace"
	managed.ProjectPath = "song"
	managed.SharedData = map[string]any{}
	if err := SetProjectEntryPath(managed.SharedData, "main.rpp"); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProjectEntry(managed, workspaceRoot)
	if err != nil || resolved.AbsolutePath != managedEntry || resolved.Locator.Kind != ProjectEntryManagedWorkspace {
		t.Fatalf("managed resolve = %#v err=%v", resolved, err)
	}

	externalRoot := t.TempDir()
	externalEntry := filepath.Join(externalRoot, "Existing.RPP")
	if err := os.WriteFile(externalEntry, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := NewWorkspace(CreateWorkspaceParams{Name: "External"})
	external.ID = "external-workspace"
	external.SharedData = map[string]any{}
	if err := external.AddDirectoryReference(DirectoryReference{ID: "reference-1", Name: "Existing", Path: externalRoot}); err != nil {
		t.Fatal(err)
	}
	if err := SetProjectEntryLocator(external.SharedData, ProjectEntryLocator{
		SchemaVersion: ProjectEntryLocatorSchemaVersion, Kind: ProjectEntryDirectoryReference,
		DirectoryReferenceID: "reference-1", RelativePath: "Existing.RPP",
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err = ResolveProjectEntry(external, workspaceRoot)
	if err != nil || resolved.AbsolutePath != externalEntry || resolved.Locator.Kind != ProjectEntryDirectoryReference {
		t.Fatalf("external resolve = %#v err=%v", resolved, err)
	}
}

func TestResolveProjectEntryRejectsRevocationSymlinksAndChangedOwnership(t *testing.T) {
	externalRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.rpp")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(externalRoot, "inside.rpp")
	if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(CreateWorkspaceParams{Name: "External"})
	ws.ID = "workspace-1"
	ws.SharedData = map[string]any{}
	if err := ws.AddDirectoryReference(DirectoryReference{ID: "reference-1", Name: "External", Path: externalRoot}); err != nil {
		t.Fatal(err)
	}
	set := func(relative string) {
		t.Helper()
		if err := SetProjectEntryLocator(ws.SharedData, ProjectEntryLocator{
			SchemaVersion: ProjectEntryLocatorSchemaVersion, Kind: ProjectEntryDirectoryReference,
			DirectoryReferenceID: "reference-1", RelativePath: relative,
		}); err != nil {
			t.Fatal(err)
		}
	}
	set("inside.rpp")
	ws.DirectoryReferences[0].WorkspaceID = "another-workspace"
	if _, err := ResolveProjectEntry(ws, t.TempDir()); !errors.Is(err, ErrProjectEntryUnavailable) {
		t.Fatalf("changed reference ownership error = %v", err)
	}
	ws.DirectoryReferences[0].WorkspaceID = ws.ID
	if err := os.Symlink(outside, filepath.Join(externalRoot, "linked.rpp")); err != nil {
		t.Fatal(err)
	}
	set("linked.rpp")
	if _, err := ResolveProjectEntry(ws, t.TempDir()); !errors.Is(err, ErrProjectEntryUnsafe) {
		t.Fatalf("symlink entry error = %v", err)
	}
	set("missing.rpp")
	if _, err := ResolveProjectEntry(ws, t.TempDir()); !errors.Is(err, ErrProjectEntryUnavailable) {
		t.Fatalf("missing entry error = %v", err)
	}
	set("inside.rpp")
	ws.DirectoryReferences = nil
	if _, err := ResolveProjectEntry(ws, t.TempDir()); !errors.Is(err, ErrProjectEntryUnavailable) {
		t.Fatalf("revoked reference error = %v", err)
	}
}

func TestProjectEntryLocatorRejectsUnknownFieldsTraversalAndWrongTypes(t *testing.T) {
	cases := []map[string]any{
		{ProjectEntryLocatorKey: map[string]any{"schema_version": 1, "kind": "managed_workspace", "relative_path": "ok.rpp", "command": "run"}},
		{ProjectEntryLocatorKey: map[string]any{"schema_version": 1, "kind": "managed_workspace", "relative_path": "../escape.rpp"}},
		{ProjectEntryLocatorKey: map[string]any{"schema_version": 1, "kind": "directory_reference", "directory_reference_id": 42, "relative_path": "ok.rpp"}},
		{ProjectEntryLocatorKey: map[string]any{"schema_version": 2, "kind": "managed_workspace", "relative_path": "ok.rpp"}},
	}
	for index, shared := range cases {
		if _, err := GetProjectEntryLocator(shared); !errors.Is(err, ErrInvalidProjectEntryPath) {
			t.Errorf("case %d error = %v", index, err)
		}
	}
}
