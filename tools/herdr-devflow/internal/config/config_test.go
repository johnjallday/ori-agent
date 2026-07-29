package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if err := os.WriteFile(path, []byte("[primary]\nkind = \"invented-agent\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, nil); err == nil || !strings.Contains(err.Error(), "Herdr-supported") {
		t.Fatalf("Load() error = %v, want supported-kind validation error", err)
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

func TestDefaultsBoundTheGitHubQuery(t *testing.T) {
	defaults := Default()
	if defaults.GitHubTimeout() <= 0 {
		t.Fatalf("github timeout = %v, want a positive default", defaults.GitHubTimeout())
	}
	if defaults.GitHubRefreshInterval() < MinGitHubRefreshInterval {
		t.Fatalf("refresh interval = %v, want at least %v", defaults.GitHubRefreshInterval(), MinGitHubRefreshInterval)
	}
	if defaults.Status.GitHubCandidateLimit <= 0 {
		t.Fatalf("candidate limit = %d, want a positive default", defaults.Status.GitHubCandidateLimit)
	}
	if err := defaults.Validate(); err != nil {
		t.Fatalf("the built-in defaults do not validate: %v", err)
	}
}

func TestValidateRejectsUnsafeGitHubSettings(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"timeout is not a duration", func(c *Config) { c.Status.GitHubTimeout = "soon" }},
		{"timeout is too small", func(c *Config) { c.Status.GitHubTimeout = "100ms" }},
		{"timeout is too large", func(c *Config) { c.Status.GitHubTimeout = "10m" }},
		{"refresh below the floor", func(c *Config) { c.Status.GitHubRefreshInterval = "1s" }},
		{"refresh is not a duration", func(c *Config) { c.Status.GitHubRefreshInterval = "often" }},
		{"candidate limit of zero", func(c *Config) { c.Status.GitHubCandidateLimit = 0 }},
		{"candidate limit too large", func(c *Config) { c.Status.GitHubCandidateLimit = 5000 }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := Default()
			testCase.mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("an unsafe GitHub setting was accepted")
			}
		})
	}
}

func TestGitHubRefreshIntervalIsClampedNotTrusted(t *testing.T) {
	// Validation rejects a sub-floor interval, but a config that reaches the
	// accessor another way must still not be able to hammer the API.
	config := Default()
	config.Status.GitHubRefreshInterval = "1s"
	if got := config.GitHubRefreshInterval(); got < MinGitHubRefreshInterval {
		t.Fatalf("interval = %v, want it clamped to %v", got, MinGitHubRefreshInterval)
	}
}

func TestOvernightDefaultsMatchThePRD(t *testing.T) {
	cfg := Default()
	if cfg.Overnight.MaxResumes != DefaultMaxResumes {
		t.Fatalf("max resumes = %d, want the documented default of %d", cfg.Overnight.MaxResumes, DefaultMaxResumes)
	}
	if cfg.Overnight.Deadline != "07:00" || cfg.Overnight.StartTime != "now" {
		t.Fatalf("overnight defaults = %+v", cfg.Overnight)
	}
	if cfg.WakeLead() != 2*time.Minute {
		t.Fatalf("wake lead = %v, want 2m", cfg.WakeLead())
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the defaults do not validate: %v", err)
	}
}

// TestOvernightConfigRejectsValuesItCannotHonor keeps a bad value a hard
// failure. A silently corrected deadline or ceiling would have the run sleeping
// the Mac against a boundary the user never chose.
func TestOvernightConfigRejectsValuesItCannotHonor(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"a start time that is not a clock time", func(c *Config) { c.Overnight.StartTime = "tonight" }, "start_time"},
		{"a deadline out of range", func(c *Config) { c.Overnight.Deadline = "25:00" }, "deadline"},
		{"a deadline with no minutes", func(c *Config) { c.Overnight.Deadline = "7" }, "deadline"},
		{"an unknown time zone", func(c *Config) { c.Overnight.Timezone = "Mars/Olympus" }, "timezone"},
		{"a zero resume ceiling", func(c *Config) { c.Overnight.MaxResumes = 0 }, "max_resumes"},
		{"a resume ceiling beyond one night", func(c *Config) { c.Overnight.MaxResumes = MaxAllowedResumes + 1 }, "max_resumes"},
		{"a wake lead too short for macOS", func(c *Config) { c.Overnight.WakeLead = "1s" }, "wake_lead"},
		{"a wake lead that keeps the Mac awake", func(c *Config) { c.Overnight.WakeLead = "1h" }, "wake_lead"},
		{"a wake lead that is not a duration", func(c *Config) { c.Overnight.WakeLead = "soon" }, "wake_lead"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := Default()
			testCase.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("an unusable overnight value validated")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want it to name %q", err, testCase.want)
			}
		})
	}
}

func TestOvernightConfigAcceptsAnImmediateStartAndACrossMidnightDeadline(t *testing.T) {
	cfg := Default()
	cfg.Overnight.StartTime = "23:00"
	cfg.Overnight.Deadline = "07:00"
	cfg.Overnight.Timezone = "America/New_York"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a run that crosses midnight was rejected: %v", err)
	}
	start, err := ParseClockTime(cfg.Overnight.StartTime)
	if err != nil || start.Hour != 23 || start.Minute != 0 {
		t.Fatalf("start = %+v, %v", start, err)
	}
}

// TestOvernightKeysAreOptionalInAnExistingConfig protects every installed
// devflow.toml: none of them mentions overnight, and all of them must load.
func TestOvernightKeysAreOptionalInAnExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devflow.toml")
	existing := `
[bridge]
enabled = true
min_herdr_version = "0.7.5"
source_id = "ori.devflow"
[primary]
role = "builder"
kind = "claude"
[roles]
default_kind = "claude"
[bootstrap]
timeout_seconds = 30
[scheduler]
retry_window = "15m"
[metadata]
enabled = true
[status]
watch_poll_interval = "2s"
`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("an existing config without overnight keys failed to load: %v", err)
	}
	if cfg.Overnight.MaxResumes != DefaultMaxResumes {
		t.Fatalf("max resumes = %d, want the default applied", cfg.Overnight.MaxResumes)
	}
}
