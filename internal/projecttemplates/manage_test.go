package projecttemplates

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestImportFolder(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "{{name}}.rpp"), "seed")
	writeFile(t, filepath.Join(src, "assets", "kick.txt"), "x")

	tpl, err := ImportFolder(libDir, src, "My Drum Kit")
	if err != nil {
		t.Fatalf("ImportFolder: %v", err)
	}
	if tpl.ID != "my-drum-kit" || tpl.Name != "My Drum Kit" {
		t.Fatalf("unexpected template: %+v", tpl)
	}

	// Verbatim copy: the {{name}} token survives un-substituted.
	if _, err := os.Stat(filepath.Join(libDir, "my-drum-kit", "{{name}}.rpp")); err != nil {
		t.Errorf("token filename not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(libDir, "my-drum-kit", "assets", "kick.txt")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}

	// Re-import collides.
	if _, err := ImportFolder(libDir, src, "My Drum Kit"); !errors.Is(err, ErrTemplateExists) {
		t.Errorf("expected ErrTemplateExists, got %v", err)
	}

	// Default ID falls back to the folder name.
	src2 := filepath.Join(t.TempDir(), "Loop Pack")
	writeFile(t, filepath.Join(src2, "a.txt"), "x")
	tpl2, err := ImportFolder(libDir, src2, "")
	if err != nil {
		t.Fatalf("ImportFolder(no name): %v", err)
	}
	if tpl2.ID != "loop-pack" || tpl2.Name != "loop-pack" {
		t.Fatalf("unexpected default-name template: %+v", tpl2)
	}
}

func TestImportFolderRejectsLibraryPaths(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	writeFile(t, filepath.Join(libDir, "existing", "a.txt"), "x")

	// A folder already inside the library.
	if _, err := ImportFolder(libDir, filepath.Join(libDir, "existing"), ""); err == nil {
		t.Error("expected rejection for source inside the library")
	}
	// A parent of the library.
	if _, err := ImportFolder(libDir, filepath.Dir(libDir), ""); err == nil {
		t.Error("expected rejection for source containing the library")
	}
}

func TestImportFolderSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	libDir := filepath.Join(t.TempDir(), "templates")
	src := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(src, "real.txt"), "x")
	if err := os.Symlink(outside, filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}

	if _, err := ImportFolder(libDir, src, "Linked"); err != nil {
		t.Fatalf("ImportFolder: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(libDir, "linked", "link")); !os.IsNotExist(err) {
		t.Error("symlink was imported")
	}
}

func TestUpdateManifest(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	writeFile(t, filepath.Join(libDir, "demo", ManifestFileName), `{"name":"Old","description":"old desc","tags":["music","reaper"],"custom_field":42}`)

	tpl, err := UpdateManifest(libDir, "demo", "New Name", "new desc")
	if err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}
	if tpl.Name != "New Name" || tpl.Description != "new desc" {
		t.Fatalf("unexpected template after update: %+v", tpl)
	}

	// Unknown fields survive the rewrite.
	data, err := os.ReadFile(filepath.Join(libDir, "demo", ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"custom_field"`) {
		t.Errorf("custom field dropped: %s", data)
	}
	if !strings.Contains(string(data), `"tags"`) {
		t.Errorf("tags dropped: %s", data)
	}

	// Clearing the name falls back to the folder name.
	tpl, err = UpdateManifest(libDir, "demo", "", "")
	if err != nil {
		t.Fatalf("UpdateManifest(clear): %v", err)
	}
	if tpl.Name != "demo" || tpl.Description != "" {
		t.Fatalf("expected folder-name fallback, got %+v", tpl)
	}

	// Unknown template.
	if _, err := UpdateManifest(libDir, "missing", "x", ""); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}

	// A template with no manifest gains one.
	writeFile(t, filepath.Join(libDir, "plain", "a.txt"), "x")
	tpl, err = UpdateManifest(libDir, "plain", "Named Now", "")
	if err != nil {
		t.Fatalf("UpdateManifest(plain): %v", err)
	}
	if tpl.Name != "Named Now" {
		t.Fatalf("manifest not created: %+v", tpl)
	}
}

func TestDeleteRemovesTemplate(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	writeFile(t, filepath.Join(libDir, "doomed", "a.txt"), "x")

	if _, err := Delete(libDir, "doomed"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(libDir, "doomed")); !os.IsNotExist(err) {
		t.Error("template folder still present after delete")
	}

	if _, err := Delete(libDir, "doomed"); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound for second delete, got %v", err)
	}
}
