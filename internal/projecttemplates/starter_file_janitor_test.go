package projecttemplates_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

func loadFileJanitorTemplate(t *testing.T) projecttemplates.Template {
	t.Helper()
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "file-janitor")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(file-janitor): %v", err)
	}
	return tpl
}

// TestFileJanitorStarterTemplate_Identity pins the built-in's stable identity:
// it is the successor blueprint, not retired, and loads cleanly (PRD FR-10,
// FR-16).
func TestFileJanitorStarterTemplate_Identity(t *testing.T) {
	tpl := loadFileJanitorTemplate(t)

	if tpl.ID != "file-janitor" {
		t.Fatalf("template ID must be file-janitor, got %q", tpl.ID)
	}
	if tpl.Name != "Filing" {
		t.Fatalf("display name = %q, want %q", tpl.Name, "Filing")
	}
	if tpl.Tagline != "Organize loose files in one folder, with your approval for every move." {
		t.Fatalf("tagline = %q", tpl.Tagline)
	}
	if !tpl.Builtin {
		t.Fatal("file-janitor must be a built-in template")
	}
	if tpl.Retired {
		t.Fatal("file-janitor must not be retired")
	}
	if tpl.BuiltinVersion != 6 {
		t.Fatalf("builtin_version = %d; Filing display refresh requires version 6", tpl.BuiltinVersion)
	}
	if len(tpl.Warnings) != 0 {
		t.Fatalf("file-janitor should load without warnings, got %v", tpl.Warnings)
	}
}

func TestFileJanitorStarterTemplate_RefreshesVersion5ManifestOnly(t *testing.T) {
	libDir := t.TempDir()
	folder := filepath.Join(libDir, "file-janitor")
	if err := os.Mkdir(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	old := `{"name":"File Janitor","builtin":true,"builtin_version":5,"agents":[{"name":"File Curator","role":"specialist"}]}`
	if err := os.WriteFile(filepath.Join(folder, projecttemplates.ManifestFileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(folder, "user-notes.md")
	if err := os.WriteFile(notes, []byte("keep my notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatal(err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "file-janitor")
	if err != nil || tpl.Name != "Filing" || tpl.BuiltinVersion != 6 || tpl.Agents[0].Name != "File Curator" {
		t.Fatalf("refreshed library = %+v, %v", tpl, err)
	}
	if data, err := os.ReadFile(notes); err != nil || string(data) != "keep my notes" {
		t.Fatalf("user notes changed: %q %v", data, err)
	}
}

// TestFileJanitorStarterTemplate_SuggestsDownloadsByDefault pins the new
// default: exactly one directory requirement suggesting the unresolved
// ~/Downloads (PRD FR-10, FR-11).
func TestFileJanitorStarterTemplate_SuggestsDownloadsByDefault(t *testing.T) {
	tpl := loadFileJanitorTemplate(t)

	if len(tpl.DirectoryRequirements) != 1 {
		t.Fatalf("expected exactly one directory requirement, got %d: %+v", len(tpl.DirectoryRequirements), tpl.DirectoryRequirements)
	}
	req := tpl.DirectoryRequirements[0]
	if req.Key != "file-janitor-root" {
		t.Fatalf("directory key = %q, want file-janitor-root", req.Key)
	}
	if req.SuggestedPath != "~/Downloads" {
		t.Fatalf("suggested path = %q, want the unresolved ~/Downloads", req.SuggestedPath)
	}
	if req.Label != "Folder to file" {
		t.Fatal("directory requirement needs a display label for the setup card")
	}
}
