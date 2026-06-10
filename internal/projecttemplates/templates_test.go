package projecttemplates

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestListLibraryMissingDirIsEmpty(t *testing.T) {
	templates, err := ListLibrary(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	if len(templates) != 0 {
		t.Fatalf("expected empty library, got %d entries", len(templates))
	}
}

func TestListLibraryDiscoversPlainFolders(t *testing.T) {
	dir := t.TempDir()
	// Plain folder with no manifest must appear under its folder name
	// (PRD success metric 3: authoring = dropping a folder in).
	writeFile(t, filepath.Join(dir, "my-layout", "seed.txt"), "x")
	// Manifest overrides display name and adds a description.
	writeFile(t, filepath.Join(dir, "fancy", ManifestFileName), `{"name":"Fancy Pack","description":"desc here","unknown_field":true}`)
	// Non-directories and hidden folders are not templates.
	writeFile(t, filepath.Join(dir, "stray.txt"), "x")
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o750); err != nil {
		t.Fatal(err)
	}

	templates, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	if len(templates) != 2 {
		t.Fatalf("expected 2 templates, got %d: %+v", len(templates), templates)
	}
	if templates[0].ID != "fancy" || templates[0].Name != "Fancy Pack" || templates[0].Description != "desc here" {
		t.Errorf("manifest template wrong: %+v", templates[0])
	}
	if templates[1].ID != "my-layout" || templates[1].Name != "my-layout" || templates[1].Description != "" {
		t.Errorf("plain template wrong: %+v", templates[1])
	}
}

func TestListLibraryMalformedManifestFallsBack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken", ManifestFileName), `{not json`)

	templates, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	if len(templates) != 1 || templates[0].Name != "broken" {
		t.Fatalf("expected fallback to folder name, got %+v", templates)
	}
}

func TestFindLibraryTemplate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "good", "a.txt"), "x")

	tpl, err := FindLibraryTemplate(dir, "good")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if tpl.ID != "good" || tpl.Path != filepath.Join(dir, "good") {
		t.Errorf("unexpected template: %+v", tpl)
	}

	for _, id := range []string{"missing", "", "../good", "good/../good", ".hidden"} {
		if _, err := FindLibraryTemplate(dir, id); !errors.Is(err, ErrTemplateNotFound) {
			t.Errorf("FindLibraryTemplate(%q): expected ErrTemplateNotFound, got %v", id, err)
		}
	}
}

func TestLoadFolder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "anywhere", ManifestFileName), `{"name":"Anywhere"}`)

	tpl, err := LoadFolder(filepath.Join(dir, "anywhere"))
	if err != nil {
		t.Fatalf("LoadFolder: %v", err)
	}
	if tpl.Name != "Anywhere" || tpl.ID != "anywhere" {
		t.Errorf("unexpected template: %+v", tpl)
	}

	if _, err := LoadFolder(filepath.Join(dir, "nope")); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound for missing folder, got %v", err)
	}
	if _, err := LoadFolder(filepath.Join(dir, "anywhere", ManifestFileName)); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound for file path, got %v", err)
	}
}
