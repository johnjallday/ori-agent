package projecttemplates

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateProjectEntryPath(t *testing.T) {
	t.Parallel()

	valid := map[string]string{
		"{{name}}.rpp":            "{{name}}.rpp",
		"sessions/{{date}}.rpp":   "sessions/{{date}}.rpp",
		"  nested//project.rpp  ": "nested/project.rpp",
	}
	for input, want := range valid {
		input, want := input, want
		t.Run("valid_"+strings.ReplaceAll(input, "/", "_"), func(t *testing.T) {
			t.Parallel()
			got, err := ValidateProjectEntryPath(input)
			if err != nil {
				t.Fatalf("ValidateProjectEntryPath(%q): %v", input, err)
			}
			if got != want {
				t.Fatalf("ValidateProjectEntryPath(%q) = %q, want %q", input, got, want)
			}
		})
	}

	invalid := []string{
		"",
		" ",
		"/absolute.rpp",
		"//server/share.rpp",
		`C:\song.rpp`,
		`folder\song.rpp`,
		"../escape.rpp",
		"folder/../escape.rpp",
		"./song.rpp",
		"{{workspace}}.rpp",
		"{{name.rpp",
		"song}}.rpp",
		"song\x00.rpp",
	}
	for _, input := range invalid {
		input := input
		t.Run("invalid_"+strings.ReplaceAll(input, "/", "_"), func(t *testing.T) {
			t.Parallel()
			if _, err := ValidateProjectEntryPath(input); !errors.Is(err, ErrInvalidProjectEntry) {
				t.Fatalf("ValidateProjectEntryPath(%q) error = %v, want ErrInvalidProjectEntry", input, err)
			}
		})
	}
}

func TestLoadFolderNormalizesValidProjectEntry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "{{name}}.rpp"), []byte("project"), 0o640); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "Song",
  "project_entry": {
    "relative_path": "{{name}}.rpp",
    "open_after_create_default": false
  },
  "agents": [{"name":"Producer","role":"orchestrator"}]
}`
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}

	tpl, err := LoadFolder(dir)
	if err != nil {
		t.Fatalf("LoadFolder: %v", err)
	}
	if tpl.ProjectEntry == nil {
		t.Fatal("expected normalized project entry")
	}
	if tpl.ProjectEntry.RelativePath != "{{name}}.rpp" || tpl.ProjectEntry.OpenAfterCreateDefault {
		t.Fatalf("unexpected project entry: %#v", tpl.ProjectEntry)
	}
	if len(tpl.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", tpl.Warnings)
	}

	data, err := json.Marshal(tpl)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"open_after_create_default":false`) {
		t.Fatalf("explicit false default missing from API JSON: %s", data)
	}
}

func TestLoadFolderWarnsAndOmitsInvalidProjectEntry(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"traversal":      `{"relative_path":"../escape.rpp","open_after_create_default":true}`,
		"unknown token":  `{"relative_path":"{{workspace}}.rpp","open_after_create_default":true}`,
		"missing source": `{"relative_path":"missing.rpp","open_after_create_default":true}`,
		"wrong type":     `"song.rpp"`,
	}
	for name, rawEntry := range tests {
		name, rawEntry := name, rawEntry
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			manifest := `{"agents":[{"name":"Producer","role":"orchestrator"}],"project_entry":` + rawEntry + `}`
			if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(manifest), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o640); err != nil {
				t.Fatal(err)
			}

			tpl, err := LoadFolder(dir)
			if err != nil {
				t.Fatalf("LoadFolder: %v", err)
			}
			if tpl.ProjectEntry != nil {
				t.Fatalf("invalid project entry was exposed: %#v", tpl.ProjectEntry)
			}
			if len(tpl.Warnings) != 1 || !strings.Contains(tpl.Warnings[0], "project_entry") {
				t.Fatalf("expected project_entry warning, got %v", tpl.Warnings)
			}
		})
	}
}

func TestLoadFolderRejectsSymlinkProjectEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.rpp")
	if err := os.WriteFile(target, []byte("project"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "entry.rpp")); err != nil {
		t.Fatal(err)
	}
	manifest := `{"agents":[{"name":"Producer","role":"orchestrator"}],"project_entry":{"relative_path":"entry.rpp","open_after_create_default":true}}`
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}

	tpl, err := LoadFolder(dir)
	if err != nil {
		t.Fatalf("LoadFolder: %v", err)
	}
	if tpl.ProjectEntry != nil || len(tpl.Warnings) != 1 || !strings.Contains(tpl.Warnings[0], "symlink") {
		t.Fatalf("expected symlink entry to be warned and omitted, got entry=%#v warnings=%v", tpl.ProjectEntry, tpl.Warnings)
	}
}
