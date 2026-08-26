package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/agents"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
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
				if parsed.issueNumber != 342 || len(parsed.issueNumbers) != 1 || parsed.issueNumbers[0] != 342 || parsed.worktree != "/tmp/dev" || parsed.yes {
					t.Fatalf("parsed = %#v", parsed)
				}
			},
		},
		{
			name: "repeated issues are sorted without flattening",
			args: []string{"--issue", "303", "--worktree", "/tmp/dev", "--issue", "101", "--issue", "202"},
			check: func(t *testing.T, parsed issuePlanArgs) {
				want := []int{101, 202, 303}
				if len(parsed.issueNumbers) != len(want) {
					t.Fatalf("parsed issueNumbers = %v, want %v", parsed.issueNumbers, want)
				}
				for index := range want {
					if parsed.issueNumbers[index] != want[index] {
						t.Fatalf("parsed issueNumbers = %v, want %v", parsed.issueNumbers, want)
					}
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
		{
			name: "Claude planner keeps model and effort",
			args: []string{"--issue", "1", "--kind", "claude", "--model", "sonnet", "--effort", "xhigh", "--worktree", "/tmp/dev"},
			check: func(t *testing.T, parsed issuePlanArgs) {
				if parsed.plannerKind != "claude" || parsed.plannerModel != "sonnet" || parsed.plannerEffort != "xhigh" {
					t.Fatalf("planner selection = %q/%q/%q", parsed.plannerKind, parsed.plannerModel, parsed.plannerEffort)
				}
			},
		},
		{
			name: "opaque Pi planner model stays one value",
			args: []string{"--issue", "1", "--kind", "pi", "--model", "[openai] gpt 5.1; $(echo inert)", "--worktree", "/tmp/dev"},
			check: func(t *testing.T, parsed issuePlanArgs) {
				if parsed.plannerKind != "pi" || parsed.plannerModel != "[openai] gpt 5.1; $(echo inert)" {
					t.Fatalf("planner selection = %q/%q", parsed.plannerKind, parsed.plannerModel)
				}
			},
		},
		{name: "missing issue", args: []string{"--worktree", "/tmp/dev"}, wantErr: true},
		{name: "missing worktree", args: []string{"--issue", "1"}, wantErr: true},
		{name: "zero issue", args: []string{"--issue", "0", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "negative issue", args: []string{"--issue", "-5", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "non-numeric issue", args: []string{"--issue", "abc", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "issue with no value", args: []string{"--issue"}, wantErr: true},
		{name: "duplicate issue", args: []string{"--issue", "1", "--issue", "1", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "duplicate worktree", args: []string{"--issue", "1", "--worktree", "/a", "--worktree", "/b"}, wantErr: true},
		{name: "empty worktree", args: []string{"--issue", "1", "--worktree", "  "}, wantErr: true},
		{name: "kind with no value", args: []string{"--issue", "1", "--worktree", "/tmp/dev", "--kind"}, wantErr: true},
		{name: "unsupported planner kind", args: []string{"--issue", "1", "--worktree", "/tmp/dev", "--kind", "codex"}, wantErr: true},
		{name: "duplicate planner kind", args: []string{"--issue", "1", "--kind", "pi", "--kind", "claude", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "Pi with effort", args: []string{"--issue", "1", "--kind", "pi", "--effort", "high", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "effort with default Pi", args: []string{"--issue", "1", "--effort", "high", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "invalid Claude effort", args: []string{"--issue", "1", "--kind", "claude", "--effort", "unbounded", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "duplicate Claude effort", args: []string{"--issue", "1", "--kind", "claude", "--effort", "low", "--effort", "high", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "effort with no value", args: []string{"--issue", "1", "--kind", "claude", "--worktree", "/tmp/dev", "--effort"}, wantErr: true},
		{name: "model with no value", args: []string{"--issue", "1", "--worktree", "/tmp/dev", "--model"}, wantErr: true},
		{name: "empty model", args: []string{"--issue", "1", "--worktree", "/tmp/dev", "--model", "  "}, wantErr: true},
		{name: "duplicate model", args: []string{"--issue", "1", "--model", "one", "--model", "two", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "flag shaped model", args: []string{"--issue", "1", "--model", "--unsafe", "--worktree", "/tmp/dev"}, wantErr: true},
		{name: "control model", args: []string{"--issue", "1", "--model", "line\nbreak", "--worktree", "/tmp/dev"}, wantErr: true},
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

func TestConfirmIssueBundlePlanRequiresCompatibilityAffirmation(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	application := New(Dependencies{Stdout: &stdout, Stdin: strings.NewReader("n\n")})
	approved, err := application.confirmIssuePlan(agents.IssuePlan{IssueNumber: 101, IssueNumbers: []int{101, 202}})
	if err != nil {
		t.Fatalf("confirmIssuePlan() error = %v", err)
	}
	if approved {
		t.Fatal("declined bundle confirmation was approved")
	}
	for _, want := range []string{"Affirm", "root cause", "shared files", "same UI surface"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("bundle confirmation missing %q: %s", want, stdout.String())
		}
	}
}

func TestSingleIssuePlanPayloadKeepsLegacyShapeAndAddsOnlyCanonicalNumbers(t *testing.T) {
	t.Parallel()
	plan := agents.IssuePlan{
		IssueNumber:  342,
		IssueNumbers: []int{342},
		Title:        "Single",
		Route:        agents.RoutePlanned,
		Slug:         "342-single",
	}
	payload := issuePlanPayload(plan)
	if payload["issue_number"] != 342 || payload["title"] != "Single" || payload["feature"] != "342-single" {
		t.Fatalf("legacy payload fields changed: %#v", payload)
	}
	if _, exists := payload["members"]; exists {
		t.Fatalf("single-Issue payload unexpectedly changed to bundle evidence: %#v", payload)
	}
	if _, exists := payload["compatibility_required"]; exists {
		t.Fatalf("single-Issue payload unexpectedly requires bundle compatibility: %#v", payload)
	}
	if _, exists := payload["planner_model"]; exists {
		t.Fatalf("integration-default planner unexpectedly added a JSON model: %#v", payload)
	}

	plan.PlannerModel = "sonnet"
	plan.PlannerEffort = "xhigh"
	payload = issuePlanPayload(plan)
	if payload["planner_model"] != "sonnet" || payload["planner_effort"] != "xhigh" {
		t.Fatalf("planner selection payload = %#v", payload)
	}
}

func TestIssuePlanPayloadAddsCompleteBundleEvidenceAndKeepsLegacyFields(t *testing.T) {
	t.Parallel()
	fetched := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	plan := agents.IssuePlan{
		IssueNumber:  101,
		IssueNumbers: []int{101, 202},
		Issues: []github.Issue{
			{Number: 101, Title: "Camera", Body: "camera body", URL: "https://example.invalid/101", State: "open", Labels: []string{"backlog", "size:quick"}, FetchedAt: fetched},
			{Number: 202, Title: "Workflow", Body: "workflow body", URL: "https://example.invalid/202", State: "open", Labels: []string{"backlog", "size:prd"}, Comments: []github.IssueComment{{Author: "alice", Body: "shared files", CreatedAt: fetched}}, FetchedAt: fetched},
		},
		Title:         "Camera",
		URL:           "https://example.invalid/101",
		IssueState:    "open",
		Labels:        []string{"backlog", "size:quick"},
		Route:         agents.RoutePRD,
		Slug:          "101-202-camera-workflow",
		ArtifactState: agents.IssueArtifactNone,
	}
	payload := issuePlanPayload(plan)
	if payload["issue_number"] != 101 || payload["title"] != "Camera" || payload["route"] != "prd" {
		t.Fatalf("legacy single-Issue payload fields changed: %#v", payload)
	}
	numbers, ok := payload["issue_numbers"].([]int)
	if !ok || len(numbers) != 2 || numbers[0] != 101 || numbers[1] != 202 {
		t.Fatalf("issue_numbers = %#v", payload["issue_numbers"])
	}
	members, ok := payload["members"].([]map[string]any)
	if !ok || len(members) != 2 || members[1]["body"] != "workflow body" {
		t.Fatalf("members = %#v", payload["members"])
	}
	comments, ok := members[1]["comments"].([]map[string]any)
	if !ok || len(comments) != 1 || comments[0]["body"] != "shared files" {
		t.Fatalf("member comments = %#v", members[1]["comments"])
	}
	if payload["compatibility_required"] != true {
		t.Fatalf("compatibility gate missing: %#v", payload)
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
		{"issue-plan", "--issue", "1", "--issue", "1", "--worktree", "/tmp/does-not-exist"},
		{"issue-plan", "--issue", "1", "--issue", "nope", "--worktree", "/tmp/does-not-exist"},
		{"issue-plan", "--issue", "1", "--kind", "codex", "--worktree", "/tmp/does-not-exist"},
		{"issue-plan", "--issue", "1", "--kind", "pi", "--effort", "high", "--worktree", "/tmp/does-not-exist"},
		{"issue-plan", "--issue", "1", "--kind", "claude", "--effort", "unbounded", "--worktree", "/tmp/does-not-exist"},
		{"issue-plan", "--issue", "1", "--model", "--unsafe", "--worktree", "/tmp/does-not-exist"},
		{"issue-plan", "--issue", "1", "--model", "one", "--model", "two", "--worktree", "/tmp/does-not-exist"},
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
