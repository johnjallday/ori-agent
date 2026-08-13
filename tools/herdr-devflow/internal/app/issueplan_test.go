package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestParseIssuePlanArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		check   func(t *testing.T, parsed issuePlanArgs)
	}{
		{
			name: "valid minimal",
			args: []string{"--issue", "342", "--worktree", "/tmp/dev"},
			check: func(t *testing.T, parsed issuePlanArgs) {
				if parsed.issueNumber != 342 || parsed.worktree != "/tmp/dev" || parsed.yes {
					t.Fatalf("parsed = %#v", parsed)
				}
			},
		},
		{
			name: "yes and confirm are the same flag",
			args: []string{"--issue", "1", "--worktree", "/tmp/dev", "--yes"},
			check: func(t *testing.T, parsed issuePlanArgs) {
				if !parsed.yes {
					t.Fatalf("--yes did not set yes: %#v", parsed)
				}
			},
		},
		{name: "missing issue", args: []string{"--worktree", "/tmp/dev"}, wantErr: true},
		{name: "missing worktree", args: []string{"--issue", "1"}, wantErr: true},
		{name: "zero issue", args: []string{"--issue", "0", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "negative issue", args: []string{"--issue", "-5", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "non-numeric issue", args: []string{"--issue", "abc", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "issue with no value", args: []string{"--issue"}, wantErr: true},
		{name: "duplicate issue", args: []string{"--issue", "1", "--issue", "2", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "duplicate worktree", args: []string{"--issue", "1", "--worktree", "/a", "--worktree", "/b"}, wantErr: true},
		{name: "empty worktree", args: []string{"--issue", "1", "--worktree", "  "}, wantErr: true},
		{name: "unknown flag", args: []string{"--issue", "1", "--worktree", "/tmp/dev", "--bogus"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := parseIssuePlanArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseIssuePlanArgs(%v) = %#v, want an error", tc.args, parsed)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIssuePlanArgs(%v) error = %v", tc.args, err)
			}
			if tc.check != nil {
				tc.check(t, parsed)
			}
		})
	}
}

// TestIssuePlanRejectsBadArgumentsBeforeAnyIO proves AR1: a malformed,
// duplicate, or missing argument fails before this command reads the working
// directory, contacts GitHub, or touches Git/Herdr state. It runs with no Git
// repository, no GitHub CLI, and no Herdr binary on the path, and still must
// fail on argument shape alone.
func TestIssuePlanRejectsBadArgumentsBeforeAnyIO(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"issue-plan"},
		{"issue-plan", "--issue", "0", "--worktree", "/tmp/does-not-exist"},
		{"issue-plan", "--issue", "abc", "--worktree", "/tmp/does-not-exist"},
		{"issue-plan", "--worktree", "/tmp/does-not-exist"},
		{"issue-plan", "--issue", "1"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			application := New(Dependencies{
				Stdout: &stdout,
				Stderr: &stderr,
				Stdin:  strings.NewReader(""),
				Getwd: func() (string, error) {
					t.Fatal("must not resolve a working directory before args are validated")
					return "", nil
				},
				LookupEnv: func(string) (string, bool) {
					t.Fatal("must not read the environment before args are validated")
					return "", false
				},
			})
			exit := application.Run(context.Background(), args)
			if exit != 2 {
				t.Fatalf("exit = %d, stdout=%q stderr=%q, want 2 (bad arguments)", exit, stdout.String(), stderr.String())
			}
		})
	}
}
