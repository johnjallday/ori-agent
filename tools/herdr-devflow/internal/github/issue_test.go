package github

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGetIssueDecodesExactFacts(t *testing.T) {
	client := clientWith(t, `{
		"number": 342,
		"title": "Start a codex session from a ready Issue",
		"body": "line one\nline two with `+"`code`"+` and $(rm -rf /)\n",
		"url": "https://github.com/o/r/issues/342",
		"state": "OPEN",
		"labels": [{"name":"backlog"},{"name":"size:planned"}],
		"comments": [
			{"author":{"login":"johnjallday"},"body":"a comment","createdAt":"2026-08-12T10:00:00Z"}
		]
	}`)

	issue, err := client.GetIssue(context.Background(), 342)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Number != 342 || issue.State != "open" {
		t.Fatalf("issue = %+v", issue)
	}
	if !strings.Contains(issue.Body, "`code`") || !strings.Contains(issue.Body, "$(rm -rf /)") {
		t.Fatalf("issue body must preserve inert Markdown/shell-looking text verbatim: %q", issue.Body)
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "backlog" || issue.Labels[1] != "size:planned" {
		t.Fatalf("labels = %v", issue.Labels)
	}
	if len(issue.Comments) != 1 || issue.Comments[0].Author != "johnjallday" || issue.Comments[0].Body != "a comment" {
		t.Fatalf("comments = %+v", issue.Comments)
	}
	if issue.Comments[0].CreatedAt.IsZero() {
		t.Fatalf("comment timestamp was dropped")
	}
}

func TestGetIssueRejectsNonPositiveNumbers(t *testing.T) {
	client := clientWith(t, `{}`)
	if _, err := client.GetIssue(context.Background(), 0); err == nil {
		t.Fatalf("GetIssue(0) should reject a non-positive Issue number")
	}
	if _, err := client.GetIssue(context.Background(), -1); err == nil {
		t.Fatalf("GetIssue(-1) should reject a non-positive Issue number")
	}
}

func TestGetIssueClassifiesFailuresWithoutLeakingRawOutput(t *testing.T) {
	client := New(Options{Run: func(context.Context, ...string) ([]byte, error) {
		return nil, exitError(t, "gh: To get started with GitHub CLI, please run:  gh auth login")
	}})
	_, err := client.GetIssue(context.Background(), 1)
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Kind != ErrorUnauthenticated {
		t.Fatalf("GetIssue() error = %v, want a classified ErrorUnauthenticated", err)
	}
	if strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("classified error must not leak raw gh stderr: %v", err)
	}
}

func TestSanitizeTextPreservesInertContentButStripsControlBytes(t *testing.T) {
	hostile := "line one\nline two\t`backticks` $(rm -rf /) \"quotes\" --leading-dash\x1b[31mred\x00null"
	got := SanitizeText(hostile)
	for _, want := range []string{"line one", "line two", "`backticks`", "$(rm -rf /)", "\"quotes\"", "--leading-dash"} {
		if !strings.Contains(got, want) {
			t.Fatalf("SanitizeText dropped inert content %q: %q", want, got)
		}
	}
	if strings.Contains(got, "\x1b[31m") || strings.Contains(got, "\x00") {
		t.Fatalf("SanitizeText must strip raw ANSI/control bytes: %q", got)
	}
}
