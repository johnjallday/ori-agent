package reaper

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryUsesVisibleGlobalPathAndNeverRunnerRoot(t *testing.T) {
	home := t.TempDir()
	library := &Library{homeDir: func() (string, error) { return home, nil }}
	root, err := library.Root()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(home, "Ori Scripts", "reaper"))
	if err != nil {
		t.Fatal(err)
	}
	if root != want {
		t.Fatalf("library root = %q, want %q", root, want)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() || info.Mode().Perm()&0o027 != 0 {
		t.Fatalf("library directory = %+v, %v", info, err)
	}

	// ~/.ori-reaper is the bounded runner exchange. canonicalRunnerRoot rejects
	// a symlink anywhere in that tree or more than 128 entries; placing a growing
	// library there would eventually break runner resolution and live control.
	unsafe := NewLibraryAt(filepath.Join(home, ".ori-reaper"))
	if _, err := unsafe.Root(); !errors.Is(err, ErrLibraryUnsafe) {
		t.Fatalf("runner root accepted as library: %v", err)
	}
}

func TestParseFrontmatterAndMalformedFallback(t *testing.T) {
	valid := ParseFrontmatter([]byte("--[[ori\nname: Add band tracks\ndescription: Creates Drums, Bass, Guitar, Vox\nconfirm: false\n]]--\nreaper.InsertTrackAtIndex(0, true)\n"))
	if !valid.Valid || valid.Name != "Add band tracks" || valid.Description == "" || valid.NeedsConfirmation || !strings.HasPrefix(valid.Body, "reaper.Insert") {
		t.Fatalf("valid frontmatter = %+v", valid)
	}
	for _, malformed := range []string{
		"reaper.ShowConsoleMsg('no metadata')",
		"--[[ori\nname: Broken\nconfirm: maybe\n]]--\nreturn",
		"--[[ori\nname: Missing close",
	} {
		parsed := ParseFrontmatter([]byte(malformed))
		if parsed.Valid || !parsed.NeedsConfirmation || parsed.Body != malformed {
			t.Fatalf("malformed frontmatter did not fail safe: %+v", parsed)
		}
	}
}

func TestLibraryCRUDAndMalformedFilesRemainVisible(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Ori Scripts", "reaper")
	library := NewLibraryAt(root)
	created, err := library.Create(ScriptInput{
		Filename: "band-tracks.lua", Name: "Add band tracks", Description: "Adds a standard band layout.",
		NeedsConfirmation: true, Code: "reaper.InsertTrackAtIndex(0, true)\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "custom:band-tracks.lua" || !created.MetadataValid || !created.NeedsConfirmation {
		t.Fatalf("created script = %+v", created)
	}
	path := filepath.Join(root, "band-tracks.lua")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "--[[ori\nname: Add band tracks") {
		t.Fatalf("shareable file = %q, %v", data, err)
	}

	updated, err := library.Update(created.ID, ScriptInput{
		Name: "Add quartet tracks", Description: "Adds four tracks.", Code: "reaper.InsertTrackAtIndex(0, true)\n",
	})
	if err != nil || updated.Name != "Add quartet tracks" || updated.NeedsConfirmation {
		t.Fatalf("updated script = %+v, %v", updated, err)
	}
	if err := os.WriteFile(filepath.Join(root, "legacy.lua"), []byte("return 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	listed, err := library.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("scripts = %+v", listed)
	}
	var legacy Script
	for _, script := range listed {
		if script.Filename == "legacy.lua" {
			legacy = script
		}
	}
	if legacy.Name != "legacy.lua" || legacy.MetadataValid || !legacy.NeedsConfirmation {
		t.Fatalf("legacy fallback = %+v", legacy)
	}

	if err := library.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete left hidden trash or original file: %v", err)
	}
	if _, err := library.Read(created.ID); !errors.Is(err, ErrScriptNotFound) {
		t.Fatalf("deleted script still readable: %v", err)
	}
}

func TestLibraryRejectsTraversalAndSymlinkScript(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	library := NewLibraryAt(root)
	if _, err := library.Root(); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Create(ScriptInput{Filename: "../escape.lua", Name: "Escape", Code: "return"}); !errors.Is(err, ErrScriptInvalid) {
		t.Fatalf("traversal create error = %v", err)
	}
	target := filepath.Join(t.TempDir(), "outside.lua")
	if err := os.WriteFile(target, []byte("return 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked.lua")); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Read("linked.lua"); !errors.Is(err, ErrScriptInvalid) {
		t.Fatalf("symlink script error = %v", err)
	}
}
