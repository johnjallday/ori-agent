package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestAnchorDeletionAndTrashOnlyRemoveSidecar(t *testing.T) {
	// Keep even the native Trash implementation inside a disposable HOME.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
	for _, operation := range []string{"delete", "trash"} {
		for _, missing := range []bool{false, true} {
			t.Run(operation+map[bool]string{true: "-missing", false: "-present"}[missing], func(t *testing.T) {
				if operation == "trash" && runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
					t.Skip("isolated path-based Trash only")
				}
				root := t.TempDir()
				anchor := filepath.Join(root, "external")
				if err := os.Mkdir(anchor, 0750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(anchor, "source.txt"), []byte("untouched"), 0600); err != nil {
					t.Fatal(err)
				}
				store, err := NewFileStore(filepath.Join(root, "workspaces"))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = store.Close() }()
				ws := newTestWorkspace("anchored", "Client")
				ws.SharedData = map[string]any{AnchorDirectoryReferenceIDKey: "anchor", PrimaryDirectoryIDKey: "anchor"}
				ws.DirectoryReferences = []DirectoryReference{{ID: "anchor", Path: anchor}}
				if err := store.Save(ws); err != nil {
					t.Fatal(err)
				}
				sidecar, _ := store.GetFolderPath(ws.ID)
				actual := anchor
				if missing {
					actual = anchor + "-moved"
					if err := os.Rename(anchor, actual); err != nil {
						t.Fatal(err)
					}
				}
				if operation == "delete" {
					if err := store.Delete(ws.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					original, trashed, err := store.Trash(ws.ID)
					if err != nil {
						t.Fatal(err)
					}
					if original != sidecar || trashed == "" {
						t.Fatal("did not trash sidecar")
					}
					if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
						t.Fatal("sidecar not moved")
					}
					restored, err := store.RestoreFromTrash(original, trashed)
					if err != nil || !restored.IsAnchored() || restored.AnchorDirectoryReferenceID() != "anchor" {
						t.Fatalf("restore lost anchor: %+v %v", restored, err)
					}
					if err := store.Delete(ws.ID); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
					t.Fatal("sidecar still exists")
				}
				if data, err := os.ReadFile(filepath.Join(actual, "source.txt")); err != nil || string(data) != "untouched" {
					t.Fatal("external folder changed")
				}
				if missing {
					if _, err := os.Stat(anchor); !os.IsNotExist(err) {
						t.Fatal("absent anchor recreated")
					}
				}
			})
		}
	}
}

func TestLegacyJanitorWorkspaceReloadDoesNotConvertShapeOrRemoveGrants(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ws := newTestWorkspace("legacy", "Existing Downloads")
	ws.SharedData = map[string]any{PrimaryDirectoryIDKey: "sidecar"}
	ws.DirectoryReferences = []DirectoryReference{{ID: "sidecar", Path: "/legacy/ori/downloads"}, {ID: "downloads", Path: "/legacy/Downloads"}}
	ws.MCPBindings = []MCPBinding{{ID: "managed", ServerName: "filesystem", Alias: "workspace-files", Enabled: true, Config: map[string]any{"roots": []string{"/legacy/ori/downloads"}}},
		{ID: "janitor", ServerName: "filesystem", Alias: "downloads_janitor_root", Enabled: true, Config: map[string]any{"roots": []string{"/legacy/Downloads"}}, AllowedTools: []string{"list_directory", "list_directory_with_sizes", "search_files", "get_file_info"}}}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Get(ws.ID)
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	after, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Shape() != "managed" || after.IsAnchored() || !reflect.DeepEqual(before.DirectoryReferences, after.DirectoryReferences) || !reflect.DeepEqual(before.MCPBindings, after.MCPBindings) {
		t.Fatal("legacy workspace migrated")
	}
}
