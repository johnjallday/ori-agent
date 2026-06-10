package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTemplatesRootPrecedence(t *testing.T) {
	configured := t.TempDir()
	envDir := t.TempDir()

	// Settings win over everything.
	t.Setenv("ORI_TEMPLATES_DIR", envDir)
	if got := ResolveTemplatesRoot(configured); got != filepath.Clean(configured) {
		t.Errorf("configured root: got %q, want %q", got, configured)
	}

	// Environment wins when settings are empty.
	if got := ResolveTemplatesRoot(""); got != filepath.Clean(envDir) {
		t.Errorf("env root: got %q, want %q", got, envDir)
	}

	// Default applies when both are empty: ORI_DATA_DIR/templates when set.
	t.Setenv("ORI_TEMPLATES_DIR", "")
	dataDir := t.TempDir()
	t.Setenv("ORI_DATA_DIR", dataDir)
	if got := ResolveTemplatesRoot(""); got != filepath.Join(dataDir, "templates") {
		t.Errorf("data-dir default: got %q, want %q", got, filepath.Join(dataDir, "templates"))
	}

	// Without ORI_DATA_DIR the default lands next to the app's working dir.
	t.Setenv("ORI_DATA_DIR", "")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolveTemplatesRoot(""); got != filepath.Join(cwd, "templates") {
		t.Errorf("cwd default: got %q, want %q", got, filepath.Join(cwd, "templates"))
	}
}

func TestManagerTemplatesRoot(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := m.GetTemplatesRoot(); got != "" {
		t.Errorf("unset templates root: got %q, want empty", got)
	}

	dir := t.TempDir()
	if err := m.SetTemplatesRoot(dir); err != nil {
		t.Fatalf("SetTemplatesRoot: %v", err)
	}
	if got := m.GetTemplatesRoot(); got != filepath.Clean(dir) {
		t.Errorf("templates root: got %q, want %q", got, dir)
	}

	// A file path is rejected; the previous value survives.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTemplatesRoot(file); err == nil {
		t.Error("expected error for file path")
	}
	if got := m.GetTemplatesRoot(); got != filepath.Clean(dir) {
		t.Errorf("templates root after rejected set: got %q, want %q", got, dir)
	}

	// Clearing works.
	if err := m.SetTemplatesRoot(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := m.GetTemplatesRoot(); got != "" {
		t.Errorf("cleared templates root: got %q", got)
	}
}
