package projectlibrary

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func browserFileVersion(t *testing.T, library *Store, scope Scope, relative string) string {
	t.Helper()
	parent := filepath.ToSlash(filepath.Dir(relative))
	if parent == "." {
		parent = ""
	}
	list, err := library.BrowserList(t.Context(), scope, "browser-000", parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range list.Entries {
		if entry.Path == relative {
			return entry.Version
		}
	}
	t.Fatalf("file not listed: %q", relative)
	return ""
}

func TestBrowserAccess_IndependentReferenceNotDiscoveryOrManagedFiles(t *testing.T) {
	library, scope, fixture := createBrowserFixture(t, t.TempDir())
	for _, entry := range []string{"browser-000", "browser-001"} {
		list, err := library.BrowserList(t.Context(), scope, entry, "")
		if err != nil || len(list.Entries) == 0 {
			t.Fatalf("authorized %s: %+v %v", entry, list, err)
		}
		for _, row := range list.Entries {
			if row.Name == "managed-only.txt" {
				t.Fatal("substituted managed files")
			}
		}
	}
	for _, entry := range []string{"foreign", "browser-002", "browser-003", "browser-104"} {
		if _, err := library.BrowserList(t.Context(), scope, entry, ""); !errors.Is(err, ErrBrowserDenied) {
			t.Fatalf("unauthorized %s: %v", entry, err)
		}
	}
	for _, change := range []func(*Scope){func(s *Scope) { s.OwnerUserID = "other" }, func(s *Scope) { s.HomeID = "other" }} {
		foreign := scope
		change(&foreign)
		if _, err := library.BrowserList(t.Context(), foreign, "browser-000", ""); !errors.Is(err, ErrBrowserDenied) {
			t.Fatalf("foreign scope: %v", err)
		}
	}
	if err := library.workspaces.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.PluginAvailable = false
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := library.BrowserList(t.Context(), scope, "browser-001", ""); err != nil {
		t.Fatalf("independent child lost access on discovery/provider loss: %v", err)
	}
	data, err := library.BrowserPreview(t.Context(), scope, "browser-000", "page.html", browserFileVersion(t, library, scope, "page.html"))
	if err != nil || data.MediaType != "text/plain; charset=utf-8" || !bytes.Contains(data.Bytes, []byte("<script>")) {
		t.Fatalf("HTML must be inert text: %+v %v", data, err)
	}
	if strings.Contains(data.MediaType, fixture.SourceRoot) {
		t.Fatal("leaked source path")
	}
}

func TestBrowserAccess_ReferenceLocatorAndRootChangesRefuse(t *testing.T) {
	for _, mode := range []string{"reference-removed", "purpose", "owner", "reference-owner", "locator", "replacement", "symlink-root", "symlink-parent"} {
		t.Run(mode, func(t *testing.T) {
			library, scope, fixture := createBrowserFixture(t, t.TempDir())
			root := filepath.Join(fixture.SourceRoot, "active", "browser-000")
			if err := library.workspaces.Update("browser-child-0", func(child *workspace.Workspace) error {
				switch mode {
				case "reference-removed":
					child.DirectoryReferences = nil
				case "purpose":
					child.DirectoryReferences[0].Purpose = "internal-future-capability"
				case "owner":
					child.OwnerUserID = "foreign"
				case "reference-owner":
					child.DirectoryReferences[0].WorkspaceID = "foreign"
				case "locator":
					return workspace.SetProjectEntryLocator(child.SharedData, workspace.ProjectEntryLocator{SchemaVersion: 1, Kind: workspace.ProjectEntryDirectoryReference, DirectoryReferenceID: "another-reference", RelativePath: "Project.rpp"})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if mode == "replacement" || mode == "symlink-root" {
				if err := os.Rename(root, root+"-original"); err != nil {
					t.Fatal(err)
				}
				if mode == "symlink-root" {
					if err := os.Symlink(root+"-original", root); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(root, 0o750); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(root, "Project.rpp"), []byte("replacement"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "symlink-parent" {
				parent := filepath.Dir(root)
				if err := os.Rename(parent, parent+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(parent+"-original", parent); err != nil {
					t.Fatal(err)
				}
			}
			if result, err := library.BrowserList(t.Context(), scope, "browser-000", ""); err == nil || len(result.Entries) != 0 {
				t.Fatalf("%s exposed data: %+v %v", mode, result, err)
			}
		})
	}
}

func TestBrowserAccess_BoundsPathsPreviewsAndChangedFiles(t *testing.T) {
	library, scope, fixture := createBrowserFixture(t, t.TempDir())
	root := filepath.Join(fixture.SourceRoot, "active", "browser-000")
	for _, path := range []string{"../", "/etc", "a/../b", "a\\b", "a//b", "./", "a\x00b", strings.Repeat("a/", 33) + "b"} {
		if _, err := library.BrowserList(t.Context(), scope, "browser-000", path); err == nil {
			t.Fatalf("accepted traversal %q", path)
		}
	}
	partial, err := library.BrowserList(t.Context(), scope, "browser-000", "Large")
	if err != nil || !partial.Truncated || len(partial.Entries) != 500 {
		t.Fatalf("large immediate listing: %d %v", len(partial.Entries), err)
	}
	empty, err := library.BrowserList(t.Context(), scope, "browser-000", "Empty")
	if err != nil || empty.Truncated || len(empty.Entries) != 0 {
		t.Fatalf("empty listing: %+v %v", empty, err)
	}
	nested := "Nested space 日本語/read me.txt"
	version := browserFileVersion(t, library, scope, nested)
	preview, err := library.BrowserPreview(t.Context(), scope, "browser-000", nested, version)
	if err != nil || !bytes.Contains(preview.Bytes, []byte("Only the original")) {
		t.Fatalf("nested preview: %+v %v", preview, err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(nested)), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := library.BrowserPreview(t.Context(), scope, "browser-000", nested, version); !errors.Is(err, ErrBrowserChanged) {
		t.Fatalf("replaced bytes: %v", err)
	}
	current := browserFileVersion(t, library, scope, nested)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(nested))); err != nil {
		t.Fatal(err)
	}
	if _, err := library.BrowserPreview(t.Context(), scope, "browser-000", nested, current); !errors.Is(err, ErrBrowserMissing) {
		t.Fatalf("disappeared file: %v", err)
	}
	for file, want := range map[string]error{"oversized.txt": ErrBrowserLarge, "vector.svg": ErrBrowserUnsupported, "Project.rpp": ErrBrowserUnsupported} {
		if _, err := library.BrowserPreview(t.Context(), scope, "browser-000", file, browserFileVersion(t, library, scope, file)); !errors.Is(err, want) {
			t.Fatalf("%s: %v", file, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "at-limit.txt"), bytes.Repeat([]byte("x"), BrowserMaxTextBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err = library.BrowserPreview(t.Context(), scope, "browser-000", "at-limit.txt", browserFileVersion(t, library, scope, "at-limit.txt"))
	if err != nil || len(preview.Bytes) != BrowserMaxTextBytes {
		t.Fatalf("at limit: %d %v", len(preview.Bytes), err)
	}
	if err := os.Symlink(filepath.Join(root, "Empty"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := library.BrowserList(t.Context(), scope, "browser-000", "link"); err == nil {
		t.Fatal("followed symlink")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := library.BrowserList(ctx, scope, "browser-000", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored cancellation: %v", err)
	}
	if _, err := library.BrowserPreview(ctx, scope, "browser-000", "page.html", browserFileVersion(t, library, scope, "page.html")); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored preview cancellation: %v", err)
	}
}

func TestBrowserAccess_LegacyReferenceCannotEnrollAnIdentityOnRead(t *testing.T) {
	library, scope, _ := createBrowserFixture(t, t.TempDir())
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	doc.Entries[0].Observations = nil
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := library.workspaces.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectLibrary = encoded
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := library.BrowserList(t.Context(), scope, "browser-000", ""); !errors.Is(err, ErrBrowserUnsupported) {
		t.Fatalf("unpinned legacy reference read: %v", err)
	}
	after, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != doc.Revision || len(after.Entries[0].Observations) != 0 {
		t.Fatal("GET enrolled an identity witness")
	}
}

func TestBrowserAccess_WireBudgetAndRasterDimensions(t *testing.T) {
	library, scope, fixture := createBrowserFixture(t, t.TempDir())
	root := filepath.Join(fixture.SourceRoot, "active", "browser-000")
	for i := range 400 {
		name := strings.Repeat("<", 230) + string(rune('A'+i/26)) + string(rune('a'+i%26)) + ".txt"
		if err := os.WriteFile(filepath.Join(root, "Empty", name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	list, err := library.BrowserList(t.Context(), scope, "browser-000", "Empty")
	encoded, _ := json.Marshal(list)
	if err != nil || !list.Truncated || len(encoded) > BrowserMaxListBytes {
		t.Fatalf("wire budget: %d %v", len(encoded), err)
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "image.png")
	if err := os.WriteFile(file, pngData.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := library.BrowserPreview(t.Context(), scope, "browser-000", "image.png", browserFileVersion(t, library, scope, "image.png"))
	if err != nil || preview.MediaType != "image/png" {
		t.Fatalf("bounded raster: %+v %v", preview, err)
	}
	data := pngData.Bytes()
	binary.BigEndian.PutUint32(data[16:20], 5000)
	binary.BigEndian.PutUint32(data[20:24], 5000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := library.BrowserPreview(t.Context(), scope, "browser-000", "image.png", browserFileVersion(t, library, scope, "image.png")); !errors.Is(err, ErrBrowserLarge) {
		t.Fatalf("unbounded image dimensions: %v", err)
	}
}

type browserRevokingStore struct {
	workspace.Store
	home   string
	reads  int
	change func() error
}

func (s *browserRevokingStore) Get(id string) (*workspace.Workspace, error) {
	if id == s.home {
		s.reads++
		if s.reads == 2 && s.change != nil {
			if err := s.change(); err != nil {
				return nil, err
			}
		} else if s.reads == 2 {
			if err := s.Update("browser-child-0", func(child *workspace.Workspace) error { child.DirectoryReferences = nil; return nil }); err != nil {
				return nil, err
			}
		}
	}
	return s.Store.Get(id)
}
func TestBrowserAccess_MirrorDisagreementRefuses(t *testing.T) {
	for _, changed := range []string{"reference", "link"} {
		t.Run(changed, func(t *testing.T) {
			library, scope, _ := createBrowserFixture(t, t.TempDir())
			folder := library.workspaces.(*workspace.FileStore)
			primary := workspace.NewInMemoryStore()
			ids, err := folder.List()
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range ids {
				value, err := folder.Get(id)
				if err != nil {
					t.Fatal(err)
				}
				if err := primary.Save(value); err != nil {
					t.Fatal(err)
				}
			}
			library.workspaces = workspace.NewSyncStore(primary, folder)
			if _, err := library.BrowserList(t.Context(), scope, "browser-000", ""); err != nil {
				t.Fatal(err)
			}
			if err := folder.Update("browser-child-0", func(child *workspace.Workspace) error {
				if changed == "reference" {
					child.DirectoryReferences[0].Name = "divergent"
				} else {
					link := child.GetAssistantProjectLink()
					link.StateRevision++
					child.SetAssistantProjectLink(link)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := library.BrowserList(t.Context(), scope, "browser-000", ""); !errors.Is(err, ErrBrowserDenied) {
				t.Fatalf("mirror divergence read: %v", err)
			}
		})
	}
}

func TestBrowserAccess_NestedSwapBeforeExposureDiscardsRead(t *testing.T) {
	for _, preview := range []bool{false, true} {
		t.Run(fmt.Sprint(preview), func(t *testing.T) {
			library, scope, fixture := createBrowserFixture(t, t.TempDir())
			relative := "Nested space 日本語/read me.txt"
			version := browserFileVersion(t, library, scope, relative)
			parent := filepath.Join(fixture.SourceRoot, "active", "browser-000", "Nested space 日本語")
			library.workspaces = &browserRevokingStore{Store: library.workspaces, home: scope.HomeID, change: func() error {
				if err := os.Rename(parent, parent+"-old"); err != nil {
					return err
				}
				if err := os.Mkdir(parent, 0o750); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(parent, "read me.txt"), []byte("replacement"), 0o600)
			}}
			if preview {
				value, err := library.BrowserPreview(t.Context(), scope, "browser-000", relative, version)
				if !errors.Is(err, ErrBrowserChanged) || len(value.Bytes) != 0 {
					t.Fatalf("swapped preview escaped: %+v %v", value, err)
				}
			} else {
				value, err := library.BrowserList(t.Context(), scope, "browser-000", "Nested space 日本語")
				if !errors.Is(err, ErrBrowserChanged) || len(value.Entries) != 0 {
					t.Fatalf("swapped listing escaped: %+v %v", value, err)
				}
			}
		})
	}
}

func TestBrowserAccess_RevocationBeforeExposureDiscardsBufferedBytes(t *testing.T) {
	library, scope, _ := createBrowserFixture(t, t.TempDir())
	version := browserFileVersion(t, library, scope, "page.html")
	library.workspaces = &browserRevokingStore{Store: library.workspaces, home: scope.HomeID}
	preview, err := library.BrowserPreview(t.Context(), scope, "browser-000", "page.html", version)
	if !errors.Is(err, ErrBrowserChanged) || len(preview.Bytes) != 0 {
		t.Fatalf("revocation leaked bytes: %+v %v", preview, err)
	}
}
