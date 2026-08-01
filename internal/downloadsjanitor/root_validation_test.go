package downloadsjanitor

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}

func setupErrorFor(t *testing.T, err error) *SetupError {
	t.Helper()
	var setupError *SetupError
	if !errors.As(err, &setupError) {
		t.Fatalf("expected a SetupError, got %v", err)
	}
	return setupError
}

// --- Canonicalization (FR-47) ------------------------------------------------

// TestCanonicalizeRoot_ResolvesSymlinksToTheRealFolder is the guarantee that
// makes ownership and containment checks meaningful. A link and its target are
// the same folder; if setup stored the link path, two workspaces could each
// "own" the same directory and every later containment check would compare
// against a path the filesystem does not actually use.
func TestCanonicalizeRoot_ResolvesSymlinksToTheRealFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	base := tempDirCanonical(t)
	real := mkdir(t, filepath.Join(base, "Downloads"))
	link := filepath.Join(base, "Inbox")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := canonicalizeRoot(link)
	if err != nil {
		t.Fatalf("canonicalizeRoot: %v", err)
	}
	if got != real {
		t.Fatalf("canonical root = %q, want the link target %q", got, real)
	}
}

func TestCanonicalizeRoot_NormalizesEquivalentSpellings(t *testing.T) {
	base := tempDirCanonical(t)
	real := mkdir(t, filepath.Join(base, "Downloads"))

	spellings := []string{
		real,
		real + string(filepath.Separator),
		filepath.Join(base, ".", "Downloads"),
		filepath.Join(base, "Downloads", "..", "Downloads"),
	}
	for _, spelling := range spellings {
		got, err := canonicalizeRoot(spelling)
		if err != nil {
			t.Fatalf("canonicalizeRoot(%q): %v", spelling, err)
		}
		if got != real {
			t.Fatalf("spelling %q resolved to %q, want %q", spelling, got, real)
		}
	}
}

func TestCanonicalizeRoot_RejectsUnusableInput(t *testing.T) {
	base := tempDirCanonical(t)

	tests := []struct {
		name string
		path string
		code string
	}{
		{"empty", "   ", CodeInvalidPath},
		{"NUL byte", "/tmp/a\x00b", CodeInvalidPath},
		{"missing", filepath.Join(base, "does-not-exist"), CodeRootMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := canonicalizeRoot(tc.path)
			if got := setupErrorFor(t, err); got.Code != tc.code {
				t.Fatalf("code = %q, want %q", got.Code, tc.code)
			}
		})
	}
}

// TestCanonicalizeRoot_DanglingSymlinkReportsMissing covers a link whose target
// was deleted: resolving before stat is what makes this report "no longer
// exists" rather than appearing valid because the link itself is there.
func TestCanonicalizeRoot_DanglingSymlinkReportsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	base := tempDirCanonical(t)
	target := mkdir(t, filepath.Join(base, "gone"))
	link := filepath.Join(base, "Inbox")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}

	_, err := canonicalizeRoot(link)
	if got := setupErrorFor(t, err); got.Code != CodeRootMissing {
		t.Fatalf("code = %q, want root_missing", got.Code)
	}
}

// --- Unsafe roots (FR-48) ----------------------------------------------------

// TestRejectUnsafeRoot_RefusesEveryTooBroadSelection walks each denied root.
// Every guard is a temp directory, so this exercises the real rules without
// touching the developer's home folder or Ori's real data directory.
func TestRejectUnsafeRoot_RefusesEveryTooBroadSelection(t *testing.T) {
	base := tempDirCanonical(t)
	home := mkdir(t, filepath.Join(base, "home"))
	data := mkdir(t, filepath.Join(base, "home", "Library", "Application Support", "OriAgent"))
	workspaces := mkdir(t, filepath.Join(base, "home", "Ori Workspaces"))
	project := mkdir(t, filepath.Join(base, "home", "code", "project"))

	guards := RootGuards{
		HomeDir:        home,
		DataDir:        data,
		WorkspaceRoot:  workspaces,
		ExtraForbidden: []string{project},
	}

	denied := []struct {
		name string
		root string
	}{
		{"filesystem root", string(filepath.Separator)},
		{"home directory itself", home},
		{"Ori data directory", data},
		{"a parent of the data directory", filepath.Join(base, "home", "Library")},
		{"workspace storage root", workspaces},
		{"a parent of the workspace root", home},
		{"a declared project root", project},
		{"a parent of the project root", filepath.Join(base, "home", "code")},
	}

	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectUnsafeRoot(tc.root, guards)
			if err == nil {
				t.Fatalf("%q was accepted; it is too broad to manage", tc.root)
			}
			setupError := setupErrorFor(t, err)
			if setupError.Code != CodeInvalidPath {
				t.Fatalf("code = %q, want invalid_path", setupError.Code)
			}
			if setupError.Repair != RepairChooseFolder {
				t.Fatalf("a refused folder must offer to choose another, got %q", setupError.Repair)
			}
			// The message must be actionable, not just a refusal.
			if !strings.Contains(setupError.Message, "Choose") {
				t.Fatalf("message is not actionable: %q", setupError.Message)
			}
		})
	}
}

// TestRejectUnsafeRoot_AllowsOrdinaryInboxFolders is the other half: the guards
// must not be so broad that the normal case is refused. A folder inside home is
// exactly what this capability is for.
func TestRejectUnsafeRoot_AllowsOrdinaryInboxFolders(t *testing.T) {
	base := tempDirCanonical(t)
	home := mkdir(t, filepath.Join(base, "home"))
	guards := RootGuards{
		HomeDir:       home,
		DataDir:       mkdir(t, filepath.Join(base, "home", ".ori-agent")),
		WorkspaceRoot: mkdir(t, filepath.Join(base, "home", "Ori Workspaces")),
	}

	allowed := []string{
		mkdir(t, filepath.Join(home, "Downloads")),
		mkdir(t, filepath.Join(home, "Desktop")),
		mkdir(t, filepath.Join(home, "Scans")),
		mkdir(t, filepath.Join(base, "elsewhere", "drop")),
		// A sibling of a guarded folder, not the folder itself.
		mkdir(t, filepath.Join(home, "Ori Workspaces Archive")),
	}
	for _, root := range allowed {
		if err := rejectUnsafeRoot(root, guards); err != nil {
			t.Fatalf("%q should be allowed: %v", root, err)
		}
	}
}

// TestRejectUnsafeRoot_ResolvesGuardsBeforeComparing proves a symlinked guard
// cannot be slipped past: if the data directory is reached through a link, the
// real location is still protected.
func TestRejectUnsafeRoot_ResolvesGuardsBeforeComparing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	base := tempDirCanonical(t)
	realData := mkdir(t, filepath.Join(base, "real-data"))
	linkedData := filepath.Join(base, "linked-data")
	if err := os.Symlink(realData, linkedData); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// The guard names the LINK; the user selects the REAL directory.
	guards := RootGuards{DataDir: linkedData}
	if err := rejectUnsafeRoot(realData, guards); err == nil {
		t.Fatal("selecting the real data directory was accepted when the guard named a link to it")
	}
}

func TestRejectUnsafeRoot_IgnoresBlankGuards(t *testing.T) {
	base := tempDirCanonical(t)
	root := mkdir(t, filepath.Join(base, "Inbox"))
	if err := rejectUnsafeRoot(root, RootGuards{HomeDir: "  ", DataDir: "", WorkspaceRoot: ""}); err != nil {
		t.Fatalf("blank guards should protect nothing: %v", err)
	}
}

// --- Overlap (FR-49) ---------------------------------------------------------

func TestRootsOverlap_CoversExactAncestorAndDescendant(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"identical", "/home/me/Downloads", "/home/me/Downloads", true},
		{"trailing separator", "/home/me/Downloads/", "/home/me/Downloads", true},
		{"a contains b", "/home/me", "/home/me/Downloads", true},
		{"b contains a", "/home/me/Downloads/Sub", "/home/me/Downloads", true},
		{"siblings", "/home/me/Downloads", "/home/me/Desktop", false},
		{"prefix but not ancestor", "/home/me/Downloads", "/home/me/Downloads2", false},
		{"unrelated", "/var/tmp/a", "/home/me/b", false},
		{"empty left", "", "/home/me", false},
		{"empty right", "/home/me", "  ", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RootsOverlap(tc.a, tc.b); got != tc.want {
				t.Fatalf("RootsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// "Downloads2" is the case a naive strings.HasPrefix check gets wrong: it shares
// a prefix with "Downloads" but is a different folder entirely.
func TestRootsOverlap_PrefixIsNotContainment(t *testing.T) {
	if RootsOverlap("/home/me/Downloads", "/home/me/DownloadsX") {
		t.Fatal("a shared name prefix must not count as containment")
	}
}
