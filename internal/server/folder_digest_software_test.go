package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// softwareLookupFixture is a real workspace store beside the software
// fixtures: the existing linked workspaces the lookup must and must not find.
type softwareLookupFixture struct {
	files *workspace.FileStore
	set   softwarefixture.Set
}

func newSoftwareLookupFixture(t *testing.T) *softwareLookupFixture {
	t.Helper()
	root := t.TempDir()
	files, err := workspace.NewFileStore(filepath.Join(root, "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(root, "projects")
	if err := os.MkdirAll(projects, 0o750); err != nil {
		t.Fatal(err)
	}
	set, err := softwarefixture.Build(projects)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { softwarefixture.Restore(set.Unreadable) })
	return &softwareLookupFixture{files: files, set: set}
}

func directoryWitness(t *testing.T, dir string) string {
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

type softwareLinked struct {
	id, name, owner, kind string
	status                workspace.WorkspaceStatus
	// primary is the folder linked as the workspace's project; supporting is a
	// folder linked beside it. bound records this feature's binding to primary.
	primary, supporting string
	bound               bool
}

// link saves one workspace with its linked folders, as the folder linker and
// the software setup leave it.
func (f *softwareLookupFixture) link(t *testing.T, spec softwareLinked) {
	t.Helper()
	if spec.owner == "" {
		spec.owner = "local"
	}
	if spec.status == "" {
		spec.status = workspace.StatusActive
	}
	ws := &workspace.Workspace{
		ID: spec.id, Name: spec.name, Kind: spec.kind, FolderSlug: spec.id, OwnerUserID: spec.owner,
		SharedData: map[string]any{}, Status: spec.status,
	}
	if spec.supporting != "" {
		if err := ws.AddDirectoryReference(workspace.DirectoryReference{Name: filepath.Base(spec.supporting), Path: spec.supporting}); err != nil {
			t.Fatal(err)
		}
	}
	if spec.primary != "" {
		directoryID, err := projecttemplates.AttachLinkedDirectory(ws, filepath.Base(spec.primary), spec.primary)
		if err != nil {
			t.Fatal(err)
		}
		if spec.bound {
			if err := workspace.SetSoftwareProjectBinding(ws.SharedData, workspace.SoftwareProjectBinding{
				DirectoryID: directoryID, DirectoryIdentity: directoryWitness(t, spec.primary),
				Recipe: "software_project", RecipeVersion: 1, BoundAt: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC),
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.files.Save(ws); err != nil {
		t.Fatal(err)
	}
}

func (f *softwareLookupFixture) find(t *testing.T, userID, dir string) []personalassistant.FolderExistingProject {
	t.Helper()
	found, err := softwareExistingProjects(f.files, userID, dir, directoryWitness(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestSoftwareExistingProjects_MatchesOnlyTheExactCheckoutOfTheOwner(t *testing.T) {
	f := newSoftwareLookupFixture(t)
	f.link(t, softwareLinked{id: "widget", name: "Widget", primary: f.set.GoNode, bound: true})

	found := f.find(t, "local", f.set.GoNode)
	if len(found) != 1 || found[0].WorkspaceID != "widget" || found[0].Name != "Widget" ||
		found[0].Route != "/workspaces/widget" || !found[0].Software || found[0].Replaced {
		t.Fatalf("found = %+v", found)
	}

	// Sibling worktrees and a clone share the repository, not the directory.
	for _, other := range []string{f.set.Worktree, f.set.SiblingWorktree, f.set.Clone, f.set.RustPython, f.set.NonGit} {
		if got := f.find(t, "local", other); len(got) != 0 {
			t.Errorf("%s matched the workspace of another checkout: %+v", filepath.Base(other), got)
		}
	}
	// A folder inside the checkout, and the folder that holds it, are other folders.
	for _, other := range []string{filepath.Join(f.set.GoNode, "web"), f.set.Root} {
		if got := f.find(t, "local", other); len(got) != 0 {
			t.Errorf("%s matched: %+v", filepath.Base(other), got)
		}
	}
	// Another owner's lookup never sees this user's workspace.
	if got := f.find(t, "someone-else", f.set.GoNode); len(got) != 0 {
		t.Fatalf("another owner found the workspace: %+v", got)
	}
}

func TestSoftwareExistingProjects_IgnoresWorkspacesThatAreNotThisProject(t *testing.T) {
	f := newSoftwareLookupFixture(t)
	dir := f.set.RustPython
	f.link(t, softwareLinked{id: "foreign", name: "Foreign", owner: "someone-else", primary: dir, bound: true})
	f.link(t, softwareLinked{id: "trashed", name: "Trashed", status: workspace.StatusTrashed, primary: dir, bound: true})
	f.link(t, softwareLinked{id: "missing", name: "Missing", status: workspace.StatusMissing, primary: dir, bound: true})
	f.link(t, softwareLinked{id: "group", name: "Group", kind: "group", primary: dir})
	// The folder is only a supporting source of this one; its project is elsewhere.
	f.link(t, softwareLinked{id: "supporting", name: "Supporting", primary: f.set.NonGit, supporting: dir})
	f.link(t, softwareLinked{id: "unlinked", name: "Unlinked"})

	if got := f.find(t, "local", dir); len(got) != 0 {
		t.Fatalf("workspaces that are not this user's project for the folder matched: %+v", got)
	}
}

func TestSoftwareExistingProjects_LegacyLinksAliasesAndSeveralMatches(t *testing.T) {
	f := newSoftwareLookupFixture(t)
	dir := f.set.Clone
	// Linked before this feature: a primary folder, no binding, no witness.
	f.link(t, softwareLinked{id: "legacy", name: "Legacy", primary: dir})
	found := f.find(t, "local", dir)
	if len(found) != 1 || found[0].WorkspaceID != "legacy" || found[0].Software || found[0].Replaced {
		t.Fatalf("legacy link = %+v", found)
	}

	// A workspace saved through a link to the folder is the same folder.
	alias := filepath.Join(f.set.Root, "clone-alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	f.link(t, softwareLinked{id: "aliased", name: "Aliased", primary: alias})
	found = f.find(t, "local", dir)
	if len(found) != 2 || found[0].WorkspaceID != "aliased" || found[1].WorkspaceID != "legacy" {
		t.Fatalf("several matches must all be returned, in a stable order: %+v", found)
	}
	// The alias path itself is not a canonical selection and matches nothing new.
	if _, err := softwareExistingProjects(f.files, "local", "relative/path", "x"); err == nil {
		t.Fatal("a relative path was accepted")
	}
	if _, err := softwareExistingProjects(nil, "local", dir, "x"); err == nil {
		t.Fatal("a missing store answered")
	}
}

func TestSoftwareExistingProjects_AReplacedDirectoryIsReportedNotReused(t *testing.T) {
	f := newSoftwareLookupFixture(t)
	dir := f.set.Clone
	f.link(t, softwareLinked{id: "bound", name: "Bound", primary: dir, bound: true})
	if found := f.find(t, "local", dir); len(found) != 1 || found[0].Replaced {
		t.Fatalf("before replacement = %+v", found)
	}
	if err := softwarefixture.Replace(dir); err != nil {
		t.Fatal(err)
	}
	found := f.find(t, "local", dir)
	if len(found) != 1 || !found[0].Software || !found[0].Replaced {
		t.Fatalf("a different directory at the same path = %+v, want it flagged as replaced", found)
	}
}
