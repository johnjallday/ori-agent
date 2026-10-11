package workspacepolicy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type resolverFixture struct {
	files    *workspace.FileStore
	resolver *Resolver
	checkout string
}

// newResolverFixture saves one workspace bound to an outside checkout on a
// working branch, the way the software-project setup leaves it.
func newResolverFixture(t *testing.T) *resolverFixture {
	t.Helper()
	root := t.TempDir()
	files, err := workspace.NewFileStore(filepath.Join(root, "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	return &resolverFixture{files: files, resolver: NewResolver(files), checkout: repoDir(t, "feature/login")}
}

func witness(t *testing.T, dir string) string {
	t.Helper()
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := projectlibrary.DirectoryIdentity(info)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func (f *resolverFixture) bind(t *testing.T, id, dir string) {
	t.Helper()
	ws := &workspace.Workspace{ID: id, Name: id, FolderSlug: id, SharedData: map[string]any{}, Status: workspace.StatusActive}
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{ID: "dir-" + id, Name: filepath.Base(dir), Path: dir}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SetSoftwareProjectBinding(ws.SharedData, workspace.SoftwareProjectBinding{
		DirectoryID: "dir-" + id, DirectoryIdentity: witness(t, dir),
		Recipe: "software_project", RecipeVersion: 1, BoundAt: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.files.Save(ws); err != nil {
		t.Fatal(err)
	}
}

// The checkout the user confirmed is the code root: its branch is the one a
// branch precondition is about, and it is where planning documents would go.
func TestResolverUsesTheBoundCheckoutAsTheCodeRoot(t *testing.T) {
	f := newResolverFixture(t)
	f.bind(t, "widget", f.checkout)

	if got := f.resolver.CodeFolder("widget"); got != f.checkout {
		t.Fatalf("code folder = %q, want the bound checkout %q", got, f.checkout)
	}
	if folder, bound := f.resolver.BoundCodeFolder("widget"); !bound || folder != f.checkout {
		t.Fatalf("bound code folder = %q, %v", folder, bound)
	}
	_, caps := f.resolver.Policy(t.Context(), "widget")
	if !caps.HasFolder || !caps.IsRepository || caps.CurrentBranch != "feature/login" {
		t.Fatalf("capabilities = %+v, want the bound checkout's repository and branch", caps)
	}
	// Ori's own storage for the workspace is never the code root.
	if own := f.files.GetFilesPath("widget"); own == "" || own == f.resolver.CodeFolder("widget") {
		t.Fatalf("the workspace's private files root %q was used as the code root", own)
	}
}

// A binding that cannot be honoured has no code root. Nothing is substituted:
// not the workspace's own files, and not the directory now at that path.
func TestResolverReportsNoCodeRootWhenTheBoundFolderIsNotTheConfirmedOne(t *testing.T) {
	for name, damage := range map[string]func(t *testing.T, dir string){
		"the folder is missing": func(t *testing.T, dir string) {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		},
		"a different directory is at that path": func(t *testing.T, dir string) {
			// The old checkout outlives the making of the new one: a
			// filesystem that reuses a deleted directory's inode number would
			// otherwise hand the replacement the same identity.
			discard, err := softwarefixture.Vacate(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = discard() })
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newResolverFixture(t)
			f.bind(t, "widget", f.checkout)
			damage(t, f.checkout)

			if got := f.resolver.CodeFolder("widget"); got != "" {
				t.Fatalf("code folder = %q, want none", got)
			}
			if folder, bound := f.resolver.BoundCodeFolder("widget"); !bound || folder != "" {
				t.Fatalf("bound code folder = %q, %v; want bound and unavailable", folder, bound)
			}
			if _, caps := f.resolver.Policy(t.Context(), "widget"); caps.HasFolder || caps.IsRepository {
				t.Fatalf("capabilities = %+v, want nothing enforceable", caps)
			}
		})
	}
}

// A malformed binding is not ignored either: the workspace is bound, and its
// folder is unavailable.
func TestResolverTreatsAMalformedBindingAsUnavailable(t *testing.T) {
	f := newResolverFixture(t)
	ws := &workspace.Workspace{
		ID: "broken", Name: "broken", FolderSlug: "broken", Status: workspace.StatusActive,
		SharedData: map[string]any{workspace.SoftwareProjectKey: map[string]any{"directory_id": "x", "surprise": true}},
	}
	if err := f.files.Save(ws); err != nil {
		t.Fatal(err)
	}
	if folder, bound := f.resolver.BoundCodeFolder("broken"); !bound || folder != "" {
		t.Fatalf("bound code folder = %q, %v", folder, bound)
	}
	if got := f.resolver.CodeFolder("broken"); got != "" {
		t.Fatalf("code folder = %q, want none", got)
	}
}

// Workspaces made before this binding existed resolve exactly as they did.
func TestResolverKeepsTheLegacyFallbackForUnboundWorkspaces(t *testing.T) {
	f := newResolverFixture(t)
	ws := &workspace.Workspace{ID: "plain", Name: "plain", FolderSlug: "plain", SharedData: map[string]any{}, Status: workspace.StatusActive}
	// A linked directory alone is not a binding.
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{ID: "dir-plain", Name: "checkout", Path: f.checkout}); err != nil {
		t.Fatal(err)
	}
	if err := f.files.Save(ws); err != nil {
		t.Fatal(err)
	}
	if folder, bound := f.resolver.BoundCodeFolder("plain"); bound || folder != "" {
		t.Fatalf("an unbound workspace reported a binding: %q, %v", folder, bound)
	}
	if got, want := f.resolver.CodeFolder("plain"), f.files.GetFilesPath("plain"); got != want || got == f.checkout {
		t.Fatalf("code folder = %q, want the legacy files root %q", got, want)
	}
	if folder, bound := f.resolver.BoundCodeFolder("no-such-workspace"); bound || folder != "" {
		t.Fatalf("a missing workspace reported a binding: %q, %v", folder, bound)
	}
}
