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

	tpl, err := UpdateManifest(libDir, "demo", "New Name", "new desc", nil)
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
	tpl, err = UpdateManifest(libDir, "demo", "", "", nil)
	if err != nil {
		t.Fatalf("UpdateManifest(clear): %v", err)
	}
	if tpl.Name != "demo" || tpl.Description != "" {
		t.Fatalf("expected folder-name fallback, got %+v", tpl)
	}

	// Unknown template.
	if _, err := UpdateManifest(libDir, "missing", "x", "", nil); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}

	// A template with no manifest gains one.
	writeFile(t, filepath.Join(libDir, "plain", "a.txt"), "x")
	tpl, err = UpdateManifest(libDir, "plain", "Named Now", "", nil)
	if err != nil {
		t.Fatalf("UpdateManifest(plain): %v", err)
	}
	if tpl.Name != "Named Now" {
		t.Fatalf("manifest not created: %+v", tpl)
	}
}

func TestUpdateManifestTagsTriState(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	writeFile(t, filepath.Join(libDir, "demo", ManifestFileName), `{"name":"Demo","tags":["music","reaper"]}`)

	// nil preserves existing tags — the legacy manage modal never sends tags.
	tpl, err := UpdateManifest(libDir, "demo", "Demo", "", nil)
	if err != nil {
		t.Fatalf("UpdateManifest(nil tags): %v", err)
	}
	if len(tpl.Tags) != 2 {
		t.Fatalf("nil tags should preserve existing, got %v", tpl.Tags)
	}

	// A non-empty slice replaces and normalizes (lowercase/trim/dedupe).
	newTags := []string{"Synth", "synth", "  Bass  "}
	tpl, err = UpdateManifest(libDir, "demo", "Demo", "", &newTags)
	if err != nil {
		t.Fatalf("UpdateManifest(set tags): %v", err)
	}
	if len(tpl.Tags) != 2 || tpl.Tags[0] != "synth" || tpl.Tags[1] != "bass" {
		t.Fatalf("expected normalized [synth bass], got %v", tpl.Tags)
	}

	// An explicit empty slice clears the key entirely.
	empty := []string{}
	tpl, err = UpdateManifest(libDir, "demo", "Demo", "", &empty)
	if err != nil {
		t.Fatalf("UpdateManifest(clear tags): %v", err)
	}
	if len(tpl.Tags) != 0 {
		t.Fatalf("expected tags cleared, got %v", tpl.Tags)
	}
	data, err := os.ReadFile(filepath.Join(libDir, "demo", ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"tags"`) {
		t.Errorf("tags key should be removed, manifest: %s", data)
	}
}

func TestCreateBlank(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")

	tpl, err := CreateBlank(libDir, "My New Template")
	if err != nil {
		t.Fatalf("CreateBlank: %v", err)
	}
	if tpl.ID != "my-new-template" || tpl.Name != "My New Template" {
		t.Fatalf("unexpected template: %+v", tpl)
	}
	if _, err := os.Stat(filepath.Join(libDir, "my-new-template", ManifestFileName)); err != nil {
		t.Errorf("manifest not written: %v", err)
	}

	// Re-create collides.
	if _, err := CreateBlank(libDir, "My New Template"); !errors.Is(err, ErrTemplateExists) {
		t.Errorf("expected ErrTemplateExists, got %v", err)
	}

	// A blank name is rejected.
	if _, err := CreateBlank(libDir, "   "); !errors.Is(err, ErrInvalidTemplateName) {
		t.Errorf("expected ErrInvalidTemplateName for blank name, got %v", err)
	}
}

func TestDuplicate(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	writeFile(t, filepath.Join(libDir, "drum-kit", ManifestFileName), `{"name":"Drum Kit","tags":["music"],"onboarding":{"version":"1"}}`)
	writeFile(t, filepath.Join(libDir, "drum-kit", "{{name}}.rpp"), "seed")

	// Default name → "<source name> copy".
	dup, err := Duplicate(libDir, "drum-kit", "")
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if dup.ID != "drum-kit-copy" || dup.Name != "Drum Kit copy" {
		t.Fatalf("unexpected duplicate: %+v", dup)
	}
	// Verbatim copy: token filename preserved.
	if _, err := os.Stat(filepath.Join(libDir, "drum-kit-copy", "{{name}}.rpp")); err != nil {
		t.Errorf("template file not copied: %v", err)
	}
	// Tags and onboarding block carry over.
	if len(dup.Tags) != 1 || dup.Tags[0] != "music" {
		t.Errorf("tags not carried, got %v", dup.Tags)
	}
	data, err := os.ReadFile(filepath.Join(libDir, "drum-kit-copy", ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"onboarding"`) {
		t.Errorf("onboarding block not carried: %s", data)
	}

	// Explicit new name seeds id and display name.
	dup2, err := Duplicate(libDir, "drum-kit", "Fancy Kit")
	if err != nil {
		t.Fatalf("Duplicate(named): %v", err)
	}
	if dup2.ID != "fancy-kit" || dup2.Name != "Fancy Kit" {
		t.Fatalf("unexpected named duplicate: %+v", dup2)
	}

	// Duplicating the same default name again collides.
	if _, err := Duplicate(libDir, "drum-kit", ""); !errors.Is(err, ErrTemplateExists) {
		t.Errorf("expected ErrTemplateExists, got %v", err)
	}

	// Unknown source.
	if _, err := Duplicate(libDir, "missing", ""); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
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
