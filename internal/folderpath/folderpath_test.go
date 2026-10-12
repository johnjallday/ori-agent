package folderpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

type fixture struct {
	home, data, workspaces, elsewhere string
	guards                            filejanitor.RootGuards
}

// newFixture lays out a home with a music folder, Ori's data directory and
// workspace root inside it, and a second tree outside the home.
func newFixture(t *testing.T) fixture {
	t.Helper()
	// macOS temp dirs sit behind a symlink; Check returns canonical paths.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{
		home:       filepath.Join(base, "home"),
		elsewhere:  filepath.Join(base, "volumes", "Sessions"),
		data:       filepath.Join(base, "home", "Library", "Application Support", "OriAgent"),
		workspaces: filepath.Join(base, "home", "Ori Workspaces"),
	}
	for _, dir := range []string{
		filepath.Join(f.home, "Music", "REAPER Projects", "Night Drive"),
		filepath.Join(f.home, ".ssh", "keys"),
		filepath.Join(f.home, ".config", "app"),
		filepath.Join(f.home, "Library", "Keychains"),
		filepath.Join(f.data, "vaults"),
		filepath.Join(f.workspaces, "Night Drive"),
		f.elsewhere,
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.home, "Music", "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.guards = filejanitor.RootGuards{HomeDir: f.home, DataDir: f.data, WorkspaceRoot: f.workspaces}
	return f
}

func TestCheck_AcceptsAFolderUnderHome(t *testing.T) {
	f := newFixture(t)
	want := filepath.Join(f.home, "Music", "REAPER Projects")
	for name, raw := range map[string]string{
		"absolute":           want,
		"tilde":              "~/Music/REAPER Projects",
		"padded":             "  ~/Music/REAPER Projects  ",
		"trailing separator": want + string(filepath.Separator),
		"unclean":            filepath.Join(f.home, "Music", "..", "Music", "REAPER Projects"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Check(raw, f.guards)
			if err != nil {
				t.Fatalf("Check(%q) = %v", raw, err)
			}
			if got.Path != want || got.Name != "REAPER Projects" || got.Warning != "" {
				t.Fatalf("Check(%q) = %+v, want path %q with no warning", raw, got, want)
			}
		})
	}
}

func TestCheck_ResolvesASymlinkToTheRealFolder(t *testing.T) {
	f := newFixture(t)
	link := filepath.Join(f.home, "Songs")
	if err := os.Symlink(filepath.Join(f.home, "Music", "REAPER Projects"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := Check(link, f.guards)
	if err != nil || got.Path != filepath.Join(f.home, "Music", "REAPER Projects") {
		t.Fatalf("Check(link) = %+v, %v", got, err)
	}

	// A link is judged by where it points, never by its own name.
	keys := filepath.Join(f.home, "Music", "keys")
	if err := os.Symlink(filepath.Join(f.home, ".ssh"), keys); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(keys, f.guards); err == nil {
		t.Fatal("a link to a credential folder was accepted")
	}
}

func TestCheck_Refuses(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, raw, want string
	}{
		{"nothing typed", "   ", "Type or paste a folder path first."},
		{"a relative path", "Music/REAPER Projects", "Use the full path"},
		{"a dot path", "./Music", "Use the full path"},
		{"a NUL byte", "/tmp/\x00x", "not valid"},
		{"a folder that is not there", filepath.Join(f.home, "Music", "Gone"), "no longer exists"},
		{"a file", filepath.Join(f.home, "Music", "notes.txt"), "a file, not a folder"},
		{"the whole drive", string(filepath.Separator), "whole drive"},
		{"the home folder", f.home, "whole home folder"},
		{"the home folder as a tilde", "~", "whole home folder"},
		{"Ori's data directory", f.data, "Ori's own data"},
		{"a folder that contains Ori's data", filepath.Join(f.home, "Library"), "Ori's own data"},
		{"the vaults inside Ori's data", filepath.Join(f.data, "vaults"), "Ori's own data"},
		{"the workspace root", f.workspaces, "your Ori workspaces"},
		{"a credential folder", filepath.Join(f.home, ".ssh"), "passwords or keys"},
		{"a credential folder as a tilde", "~/.ssh", "passwords or keys"},
		{"a folder inside a credential folder", filepath.Join(f.home, ".ssh", "keys"), "passwords or keys"},
		{"a folder that contains a credential folder", filepath.Join(f.home, ".config"), "passwords or keys"},
		{"the keychains", filepath.Join(f.home, "Library", "Keychains"), "passwords or keys"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Check(tc.raw, f.guards)
			var refusal *Error
			if !errors.As(err, &refusal) {
				t.Fatalf("Check(%q) = %+v, %v; want a refusal", tc.raw, got, err)
			}
			if !strings.Contains(refusal.Message, tc.want) {
				t.Fatalf("Check(%q) message = %q, want it to contain %q", tc.raw, refusal.Message, tc.want)
			}
			if got != (Checked{}) {
				t.Fatalf("a refusal still returned %+v", got)
			}
			if Message(err) != refusal.Message {
				t.Fatalf("Message = %q, want the refusal's own sentence", Message(err))
			}
		})
	}
}

// Every sandboxed server (wt demo, e2e, the persona run) points HOME and
// ORI_DATA_DIR at one folder. "Nothing inside the data directory" would then
// refuse every folder the user has; the home-folder and credential rules are
// what still apply there.
func TestCheck_ASandboxWhoseHomeIsItsDataDirectoryStillTakesFolders(t *testing.T) {
	f := newFixture(t)
	sandbox := f.guards
	sandbox.DataDir = f.home

	got, err := Check("~/Music/REAPER Projects", sandbox)
	if err != nil || got.Path != filepath.Join(f.home, "Music", "REAPER Projects") || got.Warning != "" {
		t.Fatalf("Check = %+v, %v", got, err)
	}
	for raw, want := range map[string]string{
		"~":                           "whole home folder",
		f.home:                        "whole home folder",
		filepath.Dir(f.home):          "Ori's own data",
		"~/.ssh":                      "passwords or keys",
		filepath.Join(f.home, ".ssh"): "passwords or keys",
		f.workspaces:                  "your Ori workspaces",
	} {
		if _, err := Check(raw, sandbox); err == nil || !strings.Contains(Message(err), want) {
			t.Errorf("Check(%q) = %v, want a refusal containing %q", raw, err, want)
		}
	}

	// A data directory that merely contains the home (a relocated ORI_DATA_DIR
	// above it) is the same situation.
	sandbox.DataDir = filepath.Dir(f.home)
	if _, err := Check("~/Music/REAPER Projects", sandbox); err != nil {
		t.Fatalf("Check with the data directory above the home = %v", err)
	}
}

// A project inside the workspace root is an ordinary folder; only the root
// itself (and anything above it) is refused.
func TestCheck_AllowsAFolderInsideTheWorkspaceRoot(t *testing.T) {
	f := newFixture(t)
	got, err := Check(filepath.Join(f.workspaces, "Night Drive"), f.guards)
	if err != nil || got.Warning != "" {
		t.Fatalf("Check = %+v, %v", got, err)
	}
}

func TestCheck_WarnsOutsideTheHomeFolder(t *testing.T) {
	f := newFixture(t)
	got, err := Check(f.elsewhere, f.guards)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != f.elsewhere || got.Name != "Sessions" || got.Warning != WarningOutsideHome {
		t.Fatalf("Check = %+v, want the folder with the outside-home warning", got)
	}

	// Without a known home nothing can be called inside it, and a tilde has
	// nothing to expand against.
	homeless := f.guards
	homeless.HomeDir = ""
	if got, err := Check(f.elsewhere, homeless); err != nil || got.Warning != WarningOutsideHome {
		t.Fatalf("Check without a home = %+v, %v", got, err)
	}
	if _, err := Check("~/Music", homeless); err == nil || !strings.Contains(Message(err), "home folder") {
		t.Fatalf("tilde without a home = %v", err)
	}
}

func TestMessage_FallsBackForAnUnknownError(t *testing.T) {
	if got := Message(errors.New("lstat /x: input/output error")); strings.Contains(got, "lstat") || got == "" {
		t.Fatalf("Message leaked or lost the error: %q", got)
	}
}
