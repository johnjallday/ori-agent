package projecttemplates

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func pinnedDate(t *testing.T) string {
	t.Helper()
	pinned := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	previous := nowFunc
	nowFunc = func() time.Time { return pinned }
	t.Cleanup(func() { nowFunc = previous })
	return "2026-06-10"
}

func TestSanitizeProjectName(t *testing.T) {
	slug, err := SanitizeProjectName("My Söng X!")
	if err != nil {
		t.Fatalf("SanitizeProjectName: %v", err)
	}
	if slug != "my-song-x" {
		t.Errorf("slug = %q, want my-song-x", slug)
	}

	if _, err := SanitizeProjectName("   "); err == nil {
		t.Error("expected error for blank name")
	}
	for _, reserved := range []string{"files", "Notes", "sub-workspaces", "tasks", "outputs", "agents", "sessions"} {
		if _, err := SanitizeProjectName(reserved); !errors.Is(err, ErrReservedName) {
			t.Errorf("SanitizeProjectName(%q): expected ErrReservedName, got %v", reserved, err)
		}
	}
}

func TestValidateTarget(t *testing.T) {
	if err := ValidateTarget(false, ""); err != nil {
		t.Errorf("clean target: %v", err)
	}
	if err := ValidateTarget(true, ""); !errors.Is(err, ErrGroupWorkspace) {
		t.Errorf("group: expected ErrGroupWorkspace, got %v", err)
	}
	if err := ValidateTarget(false, "existing-project"); !errors.Is(err, ErrProjectExists) {
		t.Errorf("existing project: expected ErrProjectExists, got %v", err)
	}
}

func TestInstantiateSubstitutesNamesAndCopiesBytes(t *testing.T) {
	date := pinnedDate(t)
	tplDir := t.TempDir()
	wsDir := t.TempDir()

	rppContent := "<REAPER_PROJECT 0.1\n  NAME {{name}} stays-literal-in-content\n>\n"
	writeFile(t, filepath.Join(tplDir, "{{name}}.rpp"), rppContent)
	writeFile(t, filepath.Join(tplDir, "bounces-{{date}}", "readme.txt"), "hello")
	writeFile(t, filepath.Join(tplDir, ManifestFileName), `{"name":"X"}`)

	rel, err := Instantiate(tplDir, wsDir, "Song X")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if rel != "song-x" {
		t.Fatalf("rel = %q, want song-x", rel)
	}

	// Filename substituted, contents byte-identical (tokens in contents untouched).
	data, err := os.ReadFile(filepath.Join(wsDir, "song-x", "song-x.rpp"))
	if err != nil {
		t.Fatalf("read instantiated rpp: %v", err)
	}
	if string(data) != rppContent {
		t.Errorf("contents modified:\n%q\nwant\n%q", data, rppContent)
	}

	// Folder names substituted too.
	if _, err := os.Stat(filepath.Join(wsDir, "song-x", "bounces-"+date, "readme.txt")); err != nil {
		t.Errorf("dated folder missing: %v", err)
	}

	// The manifest must not be copied.
	if _, err := os.Stat(filepath.Join(wsDir, "song-x", ManifestFileName)); !os.IsNotExist(err) {
		t.Errorf("template.json leaked into project (err=%v)", err)
	}
}

func TestInstantiatePreservesFileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	tplDir := t.TempDir()
	wsDir := t.TempDir()
	script := filepath.Join(tplDir, "run.sh")
	writeFile(t, script, "#!/bin/sh\n")
	if err := os.Chmod(script, 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := Instantiate(tplDir, wsDir, "proj"); err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	info, err := os.Stat(filepath.Join(wsDir, "proj", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("executable bit lost: mode %v", info.Mode())
	}
}

func TestInstantiateSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	tplDir := t.TempDir()
	wsDir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "secret")
	writeFile(t, filepath.Join(tplDir, "kept.txt"), "kept")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(tplDir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tplDir, "linkdir")); err != nil {
		t.Fatal(err)
	}

	if _, err := Instantiate(tplDir, wsDir, "proj"); err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wsDir, "proj", "link.txt")); !os.IsNotExist(err) {
		t.Error("file symlink was copied")
	}
	if _, err := os.Lstat(filepath.Join(wsDir, "proj", "linkdir")); !os.IsNotExist(err) {
		t.Error("dir symlink was copied")
	}
	if _, err := os.Stat(filepath.Join(wsDir, "proj", "kept.txt")); err != nil {
		t.Errorf("regular file missing: %v", err)
	}
}

func TestInstantiateRejectsCollision(t *testing.T) {
	tplDir := t.TempDir()
	wsDir := t.TempDir()
	writeFile(t, filepath.Join(tplDir, "a.txt"), "x")
	if err := os.MkdirAll(filepath.Join(wsDir, "proj"), 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := Instantiate(tplDir, wsDir, "proj"); !errors.Is(err, ErrProjectExists) {
		t.Errorf("expected ErrProjectExists, got %v", err)
	}
}

func TestInstantiateRejectsSubstitutionCollision(t *testing.T) {
	tplDir := t.TempDir()
	wsDir := t.TempDir()
	writeFile(t, filepath.Join(tplDir, "{{name}}.txt"), "a")
	writeFile(t, filepath.Join(tplDir, "proj.txt"), "b")

	if _, err := Instantiate(tplDir, wsDir, "proj"); err == nil {
		t.Fatal("expected substitution collision error")
	}
	// Cleanup guarantee: nothing left behind.
	if _, err := os.Stat(filepath.Join(wsDir, "proj")); !os.IsNotExist(err) {
		t.Errorf("partial project folder left behind (err=%v)", err)
	}
}

func TestInstantiateCleansUpOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("permission-based copy failure not reliable on windows or as root")
	}
	tplDir := t.TempDir()
	wsDir := t.TempDir()
	writeFile(t, filepath.Join(tplDir, "a.txt"), "x")
	unreadable := filepath.Join(tplDir, "b.txt")
	writeFile(t, unreadable, "y")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o640) })

	if _, err := Instantiate(tplDir, wsDir, "proj"); err == nil {
		t.Fatal("expected copy failure")
	}
	if _, err := os.Stat(filepath.Join(wsDir, "proj")); !os.IsNotExist(err) {
		t.Errorf("partial project folder left behind (err=%v)", err)
	}
}

func TestInstantiateRejectsMissingTemplateAndWorkspace(t *testing.T) {
	wsDir := t.TempDir()
	if _, err := Instantiate(filepath.Join(wsDir, "missing"), wsDir, "proj"); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}
	tplDir := t.TempDir()
	if _, err := Instantiate(tplDir, filepath.Join(wsDir, "missing-ws"), "proj"); err == nil {
		t.Error("expected error for missing workspace folder")
	}
}

func TestSubstituteRelPathRejectsTraversal(t *testing.T) {
	if _, err := substituteRelPath("ok/../../escape.txt", "proj"); err == nil {
		t.Error("expected traversal rejection")
	}
	if _, err := substituteRelPath("..", "proj"); err == nil {
		t.Error("expected .. rejection")
	}
	got, err := substituteRelPath("sub/{{name}}.txt", "proj")
	if err != nil {
		t.Fatalf("valid path rejected: %v", err)
	}
	if got != filepath.Join("sub", "proj.txt") {
		t.Errorf("got %q", got)
	}
}
