package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/overview"
)

// TestFeatureOverviewCarriesNoBacklogEvidence proves the status contract after
// the backlog source was removed: the JSON is the new schema version, no
// backlog field or finding survives anywhere in it, and no Issue is listed as
// though it were a feature.
func TestFeatureOverviewCarriesNoBacklogEvidence(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	// A real checkout: the status collector inspects linked worktrees, so a
	// directory merely containing a .git entry is not enough.
	repo := filepath.Join(t.TempDir(), "repo")
	runGitFixture(t, "", "init", "-b", "dev", repo)
	if err := os.Mkdir(filepath.Join(repo, ".herdr"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".herdr", "devflow.toml"),
		[]byte(devflowConfigFixture), 0600); err != nil {
		t.Fatal(err)
	}
	// A leftover backlog file must not be read back in by any surface.
	backlog := "# Backlog\n\n## Ideas\n- an unplanned idea\n\n## Doing\n- ghost -> PRD at tasks/prd-ghost.md\n"
	if err := os.WriteFile(filepath.Join(repo, "BACKLOG.md"), []byte(backlog), 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	application := New(Dependencies{
		Stdout:    &stdout,
		Stderr:    &stderr,
		Getwd:     func() (string, error) { return repo, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	args := []string{
		"--repo-root", repo, "--home", filepath.Join(t.TempDir(), "runtime"),
		"--herdr-bin", "fake-herdr", "feature-overview", "--json",
	}
	// Exit 1 is expected here: Herdr is unavailable in this fixture, so the
	// snapshot is incomplete. The payload is still the contract under test.
	application.Run(context.Background(), args)

	var snapshot struct {
		SchemaVersion int              `json:"schema_version"`
		Features      []map[string]any `json:"features"`
		Sources       []struct {
			Kind string `json:"kind"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("stdout = %q: %v", stdout.String(), err)
	}
	if snapshot.SchemaVersion != overview.SchemaVersion {
		t.Fatalf("schema_version = %d, want %d; stdout=%s stderr=%s",
			snapshot.SchemaVersion, overview.SchemaVersion, stdout.String(), stderr.String())
	}
	for _, source := range snapshot.Sources {
		if source.Kind == "backlog" {
			t.Fatalf("sources still include a backlog: %+v", snapshot.Sources)
		}
	}
	for _, feature := range snapshot.Features {
		if _, present := feature["backlog"]; present {
			t.Fatalf("a feature row still carries backlog evidence: %v", feature)
		}
		if feature["slug"] == "ghost" {
			t.Fatal("a leftover backlog entry became a feature row")
		}
	}
	if strings.Contains(stdout.String(), "backlog") {
		t.Fatalf("the status payload still mentions a backlog: %s", stdout.String())
	}
}

// runGitFixture runs one Git command for a test fixture.
func runGitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	// #nosec G204 -- every argument is a fixed literal or a path this test made.
	if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

// refusingRunner fails the test if anything asks it to run a Herdr command. It
// is how a test states "this path must not contact Herdr" as an assertion
// rather than as a comment.
type refusingRunner struct{ t *testing.T }

func (r refusingRunner) Run(context.Context, herdr.Command) (herdr.CommandResult, error) {
	r.t.Helper()
	r.t.Fatal("an argument-only path invoked Herdr")
	return herdr.CommandResult{}, nil
}

// TestHelperArgumentBoundaryIsExplicitAndSideEffectFree characterizes the CLI
// contract `wt` already relies on, before `wt backlog` starts routing through
// the same helper.
//
// Three properties are pinned. Global options are recognized only ahead of the
// command, so a value that looks like a flag cannot be mistaken for one. An
// unrecognized command is a usage error — exit 2 — and not an operation that
// half-runs. And neither case contacts Herdr, GitHub, or any other service:
// argument rejection happens before any work begins.
func TestHelperArgumentBoundaryIsExplicitAndSideEffectFree(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "runtime")

	newApp := func(stdout, stderr *bytes.Buffer) *App {
		return New(Dependencies{
			Stdout:    stdout,
			Stderr:    stderr,
			Getwd:     func() (string, error) { return repo, nil },
			LookupEnv: func(string) (string, bool) { return "", false },
			Runner:    refusingRunner{t: t},
		})
	}

	t.Run("an unknown command is a usage error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		exit := newApp(&stdout, &stderr).Run(context.Background(),
			[]string{"--repo-root", repo, "--home", home, "definitely-not-a-command"})
		if exit != 2 {
			t.Fatalf("exit = %d, want 2 for an unsupported command; stderr=%q", exit, stderr.String())
		}
		if !strings.Contains(stderr.String(), "unknown command") {
			t.Fatalf("stderr = %q, want it to name the unknown command", stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want a rejected command to print no result", stdout.String())
		}
	})

	t.Run("--json turns a usage error into a machine-readable envelope", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		exit := newApp(&stdout, &stderr).Run(context.Background(),
			[]string{"--json", "--repo-root", repo, "definitely-not-a-command"})
		if exit != 2 {
			t.Fatalf("exit = %d, want 2; stdout=%q", exit, stdout.String())
		}
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("stdout = %q is not JSON: %v", stdout.String(), err)
		}
		if envelope.Error.Code != "invalid_command" || envelope.Error.Message == "" {
			t.Fatalf("error envelope = %+v, want a coded, described usage error", envelope.Error)
		}
	})

	t.Run("a global option missing its value is rejected before any command runs", func(t *testing.T) {
		for _, args := range [][]string{
			{"--repo-root"},
			{"--repo-root", "--json", "doctor"},
			{"--home"},
			{"--config"},
			{"--herdr-bin"},
		} {
			var stdout, stderr bytes.Buffer
			if exit := newApp(&stdout, &stderr).Run(context.Background(), args); exit != 2 {
				t.Fatalf("Run(%v) exit = %d, want 2; stderr=%q", args, exit, stderr.String())
			}
			if !strings.Contains(stderr.String(), "requires a value") {
				t.Fatalf("Run(%v) stderr = %q, want the missing-value reason", args, stderr.String())
			}
		}
	})

	t.Run("no arguments and help print the command surface and succeed", func(t *testing.T) {
		for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
			var stdout, stderr bytes.Buffer
			if exit := newApp(&stdout, &stderr).Run(context.Background(), args); exit != 0 {
				t.Fatalf("Run(%v) exit = %d, want 0; stderr=%q", args, exit, stderr.String())
			}
			// The help text is the discoverable surface `wt` documents against,
			// so the commands the shell already forwards must stay listed.
			for _, want := range []string{"wt herd setup", "wt herd status", "--repo-root", "--json"} {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("Run(%v) help = %q, want it to document %q", args, stdout.String(), want)
				}
			}
		}
	})
}
