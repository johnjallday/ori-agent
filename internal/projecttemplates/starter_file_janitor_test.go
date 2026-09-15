package projecttemplates_test

import (
	"path/filepath"
	"strings"
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
	if tpl.Name != "File Janitor" {
		t.Fatalf("display name = %q, want %q", tpl.Name, "File Janitor")
	}
	if !tpl.Builtin {
		t.Fatal("file-janitor must be a built-in template")
	}
	if tpl.Retired {
		t.Fatal("file-janitor must not be retired")
	}
	if tpl.BuiltinVersion < 2 {
		t.Fatalf("builtin_version = %d; the ~/Downloads default must bump it so existing installs refresh", tpl.BuiltinVersion)
	}
	if len(tpl.Warnings) != 0 {
		t.Fatalf("file-janitor should load without warnings, got %v", tpl.Warnings)
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
	if strings.TrimSpace(req.Label) == "" {
		t.Fatal("directory requirement needs a display label for the setup card")
	}
}
