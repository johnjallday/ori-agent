package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/claudeusage"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// recorderFixture builds an app whose stdin carries one Claude payload.
func recorderFixture(t *testing.T, payload string) (*App, *bytes.Buffer, string, string) {
	t.Helper()
	repo, _ := createPrimaryCheckoutWithFeature(t)
	home := filepath.Join(t.TempDir(), "runtime")
	output := &bytes.Buffer{}
	application := New(Dependencies{
		Stdout:    output,
		Stderr:    &bytes.Buffer{},
		Stdin:     strings.NewReader(payload),
		Getwd:     func() (string, error) { return repo, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	return application, output, repo, home
}

func usageDir(t *testing.T, repo, home string) string {
	t.Helper()
	paths, err := worktree.Resolve(repo, func(key string) (string, bool) {
		if key == worktree.HomeOverrideEnv {
			return home, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths.UsageDir
}

const statuslinePayload = `{
  "session_id": "11111111-2222-3333-4444-555555555555",
  "version": "2.1.220",
  "transcript_path": "/fixture/transcript.jsonl",
  "cwd": "/fixture/repo",
  "cost": {"total_cost_usd": 1.23},
  "context_window": {"used_percentage": 30.0},
  "rate_limits": {
    "five_hour": {"used_percentage": 100, "resets_at": 1785358800},
    "seven_day": {"used_percentage": 40.0, "resets_at": 1785790800}
  }
}`

// TestRecorderPersistsOnlyWindowStateAndKeepsTheUsersStatusLine covers the two
// things the recorder must get right: it stores the handful of fields an
// Overnight Run may reason about and nothing else, and it does not cost the
// user the status line they already had.
func TestRecorderPersistsOnlyWindowStateAndKeepsTheUsersStatusLine(t *testing.T) {
	application, output, repo, home := recorderFixture(t, statuslinePayload)
	args := []string{"--repo-root", repo, "--home", home, "claude-usage", "record", "--statusline",
		"--", "printf", "my own status line"}
	if exit := application.Run(context.Background(), args); exit != 0 {
		t.Fatalf("record exit = %d", exit)
	}
	if output.String() != "my own status line" {
		t.Fatalf("status line output = %q, want the wrapped command's output unchanged", output.String())
	}

	store := claudeusage.NewStore(usageDir(t, repo, home))
	sample, err := store.Sample("11111111-2222-3333-4444-555555555555")
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if !sample.FiveHour.Exhausted() || sample.FiveHour.ResetsAt.Unix() != 1785358800 {
		t.Fatalf("sample = %+v, want the exhausted window and Claude's reset", sample)
	}
	if !sample.PlanBacked() {
		t.Fatal("a subscription session was not recorded as plan-backed")
	}

	raw, err := os.ReadFile(filepath.Join(usageDir(t, repo, home), "11111111-2222-3333-4444-555555555555.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("the record is not JSON: %v: %s", err, raw)
	}
	// The record's shape is the boundary, so assert on its keys rather than on
	// the text: observed_at carries nanoseconds, whose digits can spell any
	// short number a value search looks for.
	permitted := map[string]bool{
		"version": true, "source": true, "session_id": true, "claude_version": true,
		"observed_at": true, "five_hour": true, "seven_day": true,
		"context_used_percentage": true, "context_present": true,
	}
	for key := range record {
		if !permitted[key] {
			t.Fatalf("the record kept the payload's %q field: %s", key, raw)
		}
	}
	delete(record, "observed_at")
	rest, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"transcript", "cwd", "cost", "1.23", "/fixture"} {
		if strings.Contains(string(rest), forbidden) {
			t.Fatalf("the record kept %q from the payload: %s", forbidden, raw)
		}
	}
}

// TestRecorderStaysSilentWhenItCannotRecord keeps a Claude session healthy even
// when Ori's side is broken: a status line that printed an error, or a hook
// that exited nonzero, would degrade the user's session over a problem that is
// entirely Ori's.
func TestRecorderStaysSilentWhenItCannotRecord(t *testing.T) {
	application, output, _, home := recorderFixture(t, "this is not JSON")
	unconfigured := t.TempDir()
	args := []string{"--repo-root", unconfigured, "--home", home, "claude-usage", "record", "--statusline"}
	if exit := application.Run(context.Background(), args); exit != 0 {
		t.Fatalf("record exit = %d, want 0 so Claude's own session is unaffected", exit)
	}
	if output.String() != "" {
		t.Fatalf("the recorder printed %q into the status line", output.String())
	}
}

func TestRecorderRejectsAnUnrecognizedFailureClass(t *testing.T) {
	application, _, repo, home := recorderFixture(t, `{"session_id":"a","hook_event_name":"StopFailure"}`)
	args := []string{"--repo-root", repo, "--home", home, "claude-usage", "record", "--stop-failure", "made_up"}
	application.Run(context.Background(), args)

	store := claudeusage.NewStore(usageDir(t, repo, home))
	if _, err := store.Failure("a"); err == nil {
		t.Fatal("an unrecognized failure class was persisted")
	}
}

// TestInstallOnlyPrintsTheSettingsFragment is the boundary that matters most
// here: the file being described is the user's own Claude configuration.
func TestInstallOnlyPrintsTheSettingsFragment(t *testing.T) {
	application, output, repo, home := recorderFixture(t, "")
	claudeSettings := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"statusLine":{"type":"command","command":"my own status line"}}`)
	if err := os.WriteFile(claudeSettings, original, 0o600); err != nil {
		t.Fatal(err)
	}

	args := []string{"--repo-root", repo, "--home", home, "claude-usage", "install"}
	if exit := application.Run(context.Background(), args); exit != 0 {
		t.Fatalf("install exit = %d", exit)
	}
	for _, want := range []string{"claude-usage record --statusline", "StopFailure", "rate_limit", "never edits"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("install output did not mention %q:\n%s", want, output.String())
		}
	}
	after, err := os.ReadFile(claudeSettings)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("install modified a Claude settings file: %q, %v", after, err)
	}
}

func TestClaudeUsageStatusReportsWhetherRecordsExist(t *testing.T) {
	application, output, repo, home := recorderFixture(t, "")
	args := []string{"--repo-root", repo, "--home", home, "claude-usage", "status", "--json"}
	if exit := application.Run(context.Background(), args); exit != 0 {
		t.Fatalf("status exit = %d", exit)
	}
	var payload map[string]any
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("status JSON = %q: %v", output.String(), err)
	}
	if payload["installed"] != false {
		t.Fatalf("installed = %v, want false before any record exists", payload["installed"])
	}
	if payload["minimum_claude_version"] != claudeusage.MinimumClaudeVersion {
		t.Fatalf("minimum version = %v", payload["minimum_claude_version"])
	}
}

// TestRecordedUsageMakesAnAgentEligibleInFeatureOverview is the whole path in one test:
// Claude pushes a payload, the recorder persists it, the adapter reads it, and
// the feature overview changes its answer about whether that agent may be run
// unattended. The live-agent roster deliberately does not include eligibility.
func TestRecordedUsageMakesAnAgentEligibleInFeatureOverview(t *testing.T) {
	primary, feature := createPrimaryCheckoutWithFeature(t)
	home := filepath.Join(t.TempDir(), "runtime")
	writePrimaryCheckoutBridgeState(t, home, feature)

	featureOverview := func() string {
		t.Helper()
		var output, stderr bytes.Buffer
		application := New(Dependencies{
			Stdout:    &output,
			Stderr:    &stderr,
			Getwd:     func() (string, error) { return primary, nil },
			LookupEnv: func(string) (string, bool) { return "", false },
			Runner:    primaryCheckoutRunner{primary: primary, feature: feature},
		})
		application.Run(context.Background(), []string{
			"--repo-root", primary, "--home", home, "--herdr-bin", "fake-herdr",
			"feature-overview", "--feature", "bridge", "--no-color",
		})
		return output.String()
	}

	// Before any record exists the honest answer is a refusal, not a guess.
	before := featureOverview()
	if !strings.Contains(before, "overnight: not eligible") {
		t.Fatalf("status before recording:\n%s", before)
	}
	if !strings.Contains(before, "No Claude usage record exists") {
		t.Fatalf("status did not explain why the agent is not eligible:\n%s", before)
	}

	// The bridge fixture's saved native session is native-123; record a
	// plan-backed window for exactly that session.
	recorder := New(Dependencies{
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
		Stdin:     strings.NewReader(strings.Replace(statuslinePayload, "11111111-2222-3333-4444-555555555555", "native-123", 1)),
		Getwd:     func() (string, error) { return primary, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	if exit := recorder.Run(context.Background(), []string{
		"--repo-root", primary, "--home", home, "claude-usage", "record", "--statusline",
	}); exit != 0 {
		t.Fatalf("record exit = %d", exit)
	}

	after := featureOverview()
	if !strings.Contains(after, "overnight: eligible") {
		t.Fatalf("status after recording a plan-backed window:\n%s", after)
	}
}
