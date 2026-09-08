package projecttemplates

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

func TestPreviewInstantiationUsesCreationNamesWithoutReadingOrWritingContent(t *testing.T) {
	tplDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tplDir, "Audio"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tplDir, "{{name}}.rpp"), []byte("private template content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tplDir, "Audio", "notes.txt"), []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := PreviewInstantiation(Template{Path: tplDir, HasSkeleton: true}, "First Idea")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, []string{"Audio/notes.txt", "first-idea.rpp"}) {
		t.Fatalf("preview files = %#v", files)
	}
	if _, err := os.Stat(filepath.Join(tplDir, "first-idea.rpp")); !os.IsNotExist(err) {
		t.Fatal("preview wrote substituted output into the template")
	}
}

func TestInstantiateTemplateResolvesProjectEntryWithScaffoldTokens(t *testing.T) {
	date := pinnedDate(t)
	tplDir := t.TempDir()
	wsDir := t.TempDir()
	writeFile(t, filepath.Join(tplDir, "sessions", "{{name}}-{{date}}.rpp"), "project")
	writeFile(t, filepath.Join(tplDir, ManifestFileName), `{
  "name":"Song",
  "project_entry":{"relative_path":"sessions/{{name}}-{{date}}.rpp","open_after_create_default":true},
  "agents":[{"name":"Producer","role":"orchestrator"}]
}`)

	tpl, err := LoadFolder(tplDir)
	if err != nil {
		t.Fatalf("LoadFolder: %v", err)
	}
	result, err := InstantiateTemplate(tpl, wsDir, "Midnight Song")
	if err != nil {
		t.Fatalf("InstantiateTemplate: %v", err)
	}
	if result.ProjectPath != "midnight-song" {
		t.Fatalf("ProjectPath = %q", result.ProjectPath)
	}
	wantEntry := "sessions/midnight-song-" + date + ".rpp"
	if result.ProjectEntryPath != wantEntry || result.ProjectWarning != "" {
		t.Fatalf("unexpected entry result: %+v, want %q", result, wantEntry)
	}
	if _, err := os.Stat(filepath.Join(wsDir, result.ProjectPath, filepath.FromSlash(result.ProjectEntryPath))); err != nil {
		t.Fatalf("resolved project entry does not exist: %v", err)
	}
}

func TestInstantiateTemplateEntryFailureIsNonFatal(t *testing.T) {
	tplDir := t.TempDir()
	wsDir := t.TempDir()
	entrySource := filepath.Join(tplDir, "{{name}}.rpp")
	writeFile(t, entrySource, "project")
	writeFile(t, filepath.Join(tplDir, "keep.txt"), "keep")
	writeFile(t, filepath.Join(tplDir, ManifestFileName), `{
  "project_entry":{"relative_path":"{{name}}.rpp","open_after_create_default":true},
  "agents":[{"name":"Producer","role":"orchestrator"}]
}`)
	tpl, err := LoadFolder(tplDir)
	if err != nil || tpl.ProjectEntry == nil {
		t.Fatalf("LoadFolder entry = %#v, err = %v", tpl.ProjectEntry, err)
	}
	if err := os.Remove(entrySource); err != nil {
		t.Fatal(err)
	}

	result, err := InstantiateTemplate(tpl, wsDir, "Song")
	if err != nil {
		t.Fatalf("entry failure should not fail instantiation: %v", err)
	}
	if result.ProjectPath != "song" || result.ProjectEntryPath != "" || result.ProjectWarning == "" {
		t.Fatalf("unexpected non-fatal result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(wsDir, "song", "keep.txt")); err != nil {
		t.Fatalf("project was rolled back after entry warning: %v", err)
	}
}

// Field-token substitution left with the intake engine: any leftover
// {{fields.<id>}} token in a template entry name is a clear error instead of a
// silently-literal folder name.
func TestInstantiateRejectsFieldTokens(t *testing.T) {
	tplDir := t.TempDir()
	wsDir := t.TempDir()
	writeFile(t, filepath.Join(tplDir, "{{fields.song_name}}.txt"), "hello")

	if _, err := Instantiate(tplDir, wsDir, "Song X"); err == nil {
		t.Fatal("expected unknown field token error")
	}
	if _, err := os.Stat(filepath.Join(wsDir, "song-x")); !os.IsNotExist(err) {
		t.Errorf("partial project folder left behind (err=%v)", err)
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
