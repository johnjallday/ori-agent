package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAppliesExplicitOverridesAndRoleDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "devflow.toml")
	contents := `
[bridge]
enabled = false
min_herdr_version = "0.7.5"
source_id = "ori.devflow"

[primary]
role = "builder"
kind = "claude"

[roles]
default_kind = "codex"

[roles.defaults]
reviewer = "claude"

[bootstrap]
timeout_seconds = 30

[scheduler]
retry_window = "15m"

[metadata]
enabled = true

[status]
watch_poll_interval = "2s"
`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"HERDR_DEVFLOW_ENABLED":      "true",
		"HERDR_DEVFLOW_PRIMARY_KIND": "codex",
		"HERDR_DEVFLOW_RETRY_WINDOW": "20m",
	}
	cfg, err := Load(path, func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Bridge.Enabled || cfg.Primary.Kind != "codex" || cfg.Scheduler.RetryWindow != "20m" {
		t.Fatalf("unexpected overrides: %#v", cfg)
	}
	if got := cfg.RoleKind("reviewer"); got != "claude" {
		t.Fatalf("RoleKind(reviewer) = %q, want claude", got)
	}
	if got := cfg.RoleKind("new-role"); got != "codex" {
		t.Fatalf("RoleKind(new-role) = %q, want codex", got)
	}
}

func TestLoadRejectsUnknownAndUnsafeConfiguration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "devflow.toml")
	if err := os.WriteFile(path, []byte("[bridge]\nunknown = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown devflow config") {
		t.Fatalf("Load() error = %v, want unknown-key error", err)
	}

	if err := os.WriteFile(path, []byte("[primary]\nrole = \"../builder\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(path, nil)
	if err == nil || !strings.Contains(err.Error(), "primary.role") {
		t.Fatalf("Load() error = %v, want role validation error", err)
	}
}

func TestParseVersion(t *testing.T) {
	t.Parallel()
	installed, err := ParseVersion("v0.7.5-preview.1")
	if err != nil {
		t.Fatal(err)
	}
	minimum, _ := ParseVersion("0.7.5")
	if !installed.AtLeast(minimum) {
		t.Fatalf("%v should meet %v", installed, minimum)
	}
	if _, err := ParseVersion("herdr 0.7.5"); err == nil {
		t.Fatal("ParseVersion accepted a command banner")
	}
}
