package folderdigest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
)

func softwareSet(t *testing.T) softwarefixture.Set {
	t.Helper()
	set, err := softwarefixture.Build(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { softwarefixture.Restore(set.Unreadable) })
	return set
}

func scanRoot(t *testing.T, dir string) Candidate {
	t.Helper()
	result, err := Scan(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return result.RootCandidate()
}

func TestScan_SoftwareMarkersAndRepositoryKinds(t *testing.T) {
	set := softwareSet(t)
	for _, tc := range []struct {
		name       string
		dir        string
		marker     string
		repository RepositoryMarker
	}{
		{"go and node in one checkout", set.GoNode, "go.mod", RepositoryDirectory},
		{"rust and python", set.RustPython, "Cargo.toml", RepositoryDirectory},
		{"linked worktree", set.Worktree, "go.mod", RepositoryFile},
		{"clone", set.Clone, "go.mod", RepositoryDirectory},
		{"no git", set.NonGit, "package.json", RepositoryNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := scanRoot(t, tc.dir)
			if root.Marker == nil || root.Marker.Name != tc.marker {
				t.Fatalf("marker = %+v, want %s", root.Marker, tc.marker)
			}
			if root.Repository != tc.repository {
				t.Errorf("repository = %q, want %q", root.Repository, tc.repository)
			}
			if ShapeFor(root) != ShapeCode {
				t.Errorf("shape = %q, want code", ShapeFor(root))
			}
			if _, ok := BuiltinCapabilityFor(ShapeFor(root), root.Marker.Name); !ok {
				t.Errorf("a %s project has no built-in setup", tc.marker)
			}
		})
	}
}

// A worktree with no manifest is still a repository: the .git file's name is
// the marker.
func TestScan_GitFileAloneMarksARepository(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nowhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := scanRoot(t, dir)
	if root.Marker == nil || root.Marker.Name != ".git" || root.Repository != RepositoryFile {
		t.Fatalf("marker = %+v repository = %q, want the .git file", root.Marker, root.Repository)
	}
	if !HasMarker(dir, ".git") {
		t.Fatal("revalidation does not see the .git file")
	}
}

// The scan never follows a link, so a link named .git is not a marker.
func TestScan_LinkedGitEntryIsNotAMarker(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	root := scanRoot(t, dir)
	if root.Marker != nil || root.Repository != RepositoryNone {
		t.Fatalf("a symbolic link became a marker: %+v %q", root.Marker, root.Repository)
	}
	if HasMarker(dir, ".git") {
		t.Fatal("revalidation followed a link named .git")
	}
}

// One checkout with several manifests is one project; a folder that only holds
// separate projects offers each of them and is not itself one.
func TestDecide_OneRepositoryVersusAFolderOfProjects(t *testing.T) {
	set := softwareSet(t)
	now := fixtureNow()

	result, err := Scan(set.GoNode, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Subfolders()) == 0 {
		t.Fatal("the checkout's subfolders were not scanned")
	}
	one := Decide(result, now)
	if one.Kind != KindProject || len(one.Projects) != 1 || !one.Projects[0].IsRoot {
		t.Fatalf("a checkout with two manifests = %s with %d projects, want one root project", one.Kind, len(one.Projects))
	}

	result, err = Scan(set.Container, Options{})
	if err != nil {
		t.Fatal(err)
	}
	many := Decide(result, now)
	if many.Kind != KindMixed || len(many.Projects) != 2 {
		t.Fatalf("a folder of projects = %s with %d projects, want mixed with two", many.Kind, len(many.Projects))
	}
	for _, project := range many.Projects {
		if project.IsRoot || ShapeFor(project) != ShapeCode || project.Repository != RepositoryDirectory {
			t.Errorf("candidate %q: root=%t shape=%q repository=%q", project.Name, project.IsRoot, ShapeFor(project), project.Repository)
		}
	}
}

// A folder that holds one large checkout and nothing of its own is not the
// project: its file counts are the checkout's. Found by the browser demo, where
// a Documents folder with one repository in it was offered as the project.
func TestDecide_AFolderHoldingOneCheckoutIsNotTheProject(t *testing.T) {
	holder := t.TempDir()
	checkout := filepath.Join(holder, "service")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Enough Go files, edited today, for the holder to pass the density rule.
	for i := range 40 {
		dir := filepath.Join(checkout, "internal", "pkg")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go"), []byte("package pkg\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := fixtureNow()
	result, err := Scan(holder, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if root := result.RootCandidate(); root.HasMarker() || !projectSignal(root, now) {
		t.Fatalf("the fixture no longer exercises the density rule on the holder: %+v", root)
	}
	v := Decide(result, now)
	if v.Kind != KindProject || len(v.Projects) != 1 || v.Project == nil || v.Project.IsRoot || v.Project.Name != "service" {
		t.Fatalf("verdict = %s about %+v, want the one checkout inside", v.Kind, v.Project)
	}

	// A folder that holds a body of work directly is still a project itself,
	// whatever marked folders sit beside that work.
	direct := t.TempDir()
	for i := range 12 {
		if err := os.WriteFile(filepath.Join(direct, "script"+string(rune('a'+i))+".py"), []byte("pass\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vendored := filepath.Join(direct, "vendored")
	if err := os.MkdirAll(vendored, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendored, "package.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = Scan(direct, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if v := Decide(result, now); v.Project == nil || !v.Project.IsRoot {
		t.Fatalf("a folder of scripts lost its own offer to a marked subfolder: %s %+v", v.Kind, v.Project)
	}
}

// A manuscript or a notes vault kept under Git is still a manuscript or a
// vault: the more specific marker names it, and it gets no software setup.
func TestScan_MoreSpecificShapesKeepPrecedenceUnderGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tex"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := scanRoot(t, dir)
	if ShapeFor(root) != ShapeManuscript || root.Repository != RepositoryDirectory {
		t.Fatalf("shape = %q repository = %q, want a manuscript in a repository", ShapeFor(root), root.Repository)
	}
	if _, ok := BuiltinCapabilityFor(ShapeFor(root), root.Marker.Name); ok {
		t.Fatal("a manuscript under Git was offered the software setup")
	}
}

func TestBuiltinCapabilityFor_NeedsAMarkerOfItsOwnRow(t *testing.T) {
	if row, ok := BuiltinCapabilityFor(ShapeCode, "go.mod"); !ok || row.Builtin == nil || row.Builtin.Recipe != BuiltinSoftwareProject || row.Builtin.Version < 1 {
		t.Fatalf("code row = %+v ok=%t", row.Builtin, ok)
	}
	for _, tc := range []struct {
		shape  Shape
		marker string
	}{
		{ShapeCode, ""},         // a picked file lent the folder its shape
		{ShapeCode, "main.tex"}, // another row's marker
		{ShapeAudio, "*.rpp"},   // an integration row
		{ShapeManuscript, "main.tex"},
		{"", "go.mod"},
	} {
		if _, ok := BuiltinCapabilityFor(tc.shape, tc.marker); ok {
			t.Errorf("shape %q marker %q was given a built-in setup", tc.shape, tc.marker)
		}
	}
	// Callers cannot change the host's recipe through a returned row.
	row, _ := BuiltinCapabilityFor(ShapeCode, "go.mod")
	row.Builtin.Recipe = "forged"
	if again, _ := BuiltinCapabilityFor(ShapeCode, "go.mod"); again.Builtin.Recipe != BuiltinSoftwareProject {
		t.Fatal("a caller mutated the host table")
	}
}

// With every file unreadable, including a worktree's .git file, the markers,
// repository kinds and verdicts are unchanged: the scan read names only.
func TestScan_SoftwareFixturesNeverOpenFiles(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores file modes")
	}
	set := softwareSet(t)
	now := fixtureNow()
	for _, dir := range []string{set.GoNode, set.Worktree, set.NonGit, set.Container, set.Hostile} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			before, err := Scan(dir, Options{})
			if err != nil {
				t.Fatal(err)
			}
			want := Decide(before, now)
			makeUnreadable(t, dir)
			after, err := Scan(dir, Options{})
			if err != nil {
				t.Fatal(err)
			}
			got := Decide(after, now)
			if got.Kind != want.Kind || got.Reason != want.Reason || len(got.Projects) != len(want.Projects) {
				t.Errorf("unreadable files changed the verdict: %s %q -> %s %q", want.Kind, want.Reason, got.Kind, got.Reason)
			}
			a, b := before.RootCandidate(), after.RootCandidate()
			if a.Repository != b.Repository || (a.Marker == nil) != (b.Marker == nil) || (a.Marker != nil && a.Marker.Name != b.Marker.Name) {
				t.Errorf("unreadable files changed the markers: %+v/%q -> %+v/%q", a.Marker, a.Repository, b.Marker, b.Repository)
			}
		})
	}
}
