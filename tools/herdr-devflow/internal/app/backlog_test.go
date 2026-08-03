package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
)

// fakeGitHub answers `gh` invocations from a fixture and records every
// argument vector, so a test can assert both what was asked and what the
// command did with the answer. No test in this file reaches the network or the
// real repository's Issues.
type fakeGitHub struct {
	repository string
	issues     string
	failure    error
	calls      [][]string
}

func (f *fakeGitHub) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if f.failure != nil {
		return nil, f.failure
	}
	if len(args) > 1 && args[0] == "repo" && args[1] == "view" {
		if f.repository == "" {
			return []byte(`{"name":"ori-agent","owner":{"login":"johnjallday"}}`), nil
		}
		return []byte(f.repository), nil
	}
	if f.issues == "" {
		return []byte("[]"), nil
	}
	return []byte(f.issues), nil
}

// newBacklogApp builds an App whose GitHub access is the supplied fixture and
// whose checkout is a bare temporary repository.
func newBacklogApp(t *testing.T, remote *fakeGitHub) (*App, *bytes.Buffer, *bytes.Buffer, []string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	application := New(Dependencies{
		Stdout:       stdout,
		Stderr:       stderr,
		Getwd:        func() (string, error) { return repo, nil },
		LookupEnv:    func(string) (string, bool) { return "", false },
		GitHubRunner: remote.run,
	})
	base := []string{"--repo-root", repo, "--home", filepath.Join(t.TempDir(), "runtime"), "backlog"}
	return application, stdout, stderr, base
}

func TestParseBacklogArgsAcceptsEverySupportedListSpelling(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		nil,
		{"list"},
		{"ls"},
	} {
		parsed, err := parseBacklogArgs(args)
		if err != nil {
			t.Fatalf("parseBacklogArgs(%v): %v", args, err)
		}
		if parsed.command != "list" || parsed.scope != github.ScopeMe || parsed.json {
			t.Fatalf("parseBacklogArgs(%v) = %+v, want a default author-scoped list", args, parsed)
		}
	}

	// The flags mean the same thing wherever they appear, because a person
	// types them where the sentence ends, not where a parser prefers.
	for _, args := range [][]string{
		{"--all"},
		{"list", "--all"},
		{"--all", "list"},
	} {
		parsed, err := parseBacklogArgs(args)
		if err != nil || parsed.scope != github.ScopeAll || parsed.command != "list" {
			t.Fatalf("parseBacklogArgs(%v) = %+v, %v; want an all-author list", args, parsed, err)
		}
	}
	for _, args := range [][]string{
		{"--json"},
		{"list", "--json"},
		{"--json", "list", "--all"},
	} {
		parsed, err := parseBacklogArgs(args)
		if err != nil || !parsed.json {
			t.Fatalf("parseBacklogArgs(%v) = %+v, %v; want JSON output", args, parsed, err)
		}
	}
}

func TestParseBacklogArgsRejectsAmbiguousInvocations(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"list", "extra"},
		{"list", "list"},
		{"--every"},
		{"-a"},
		{"promote"},
		{"doing"},
		{"ship"},
		{"drop"},
		{"select"},
	} {
		if parsed, err := parseBacklogArgs(args); err == nil {
			t.Fatalf("parseBacklogArgs(%v) = %+v, want a usage error", args, parsed)
		}
	}
}

func TestParseBacklogArgsExplainsTheRemovedFileCommands(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]string{
		"sync":  "read live",
		"prune": "history",
	} {
		_, err := parseBacklogArgs([]string{command})
		if err == nil {
			t.Fatalf("parseBacklogArgs(%q) was accepted", command)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("parseBacklogArgs(%q) error = %q, want it to explain the replacement", command, err)
		}
	}
}

func TestBacklogRejectsInvalidArgumentsBeforeTouchingGitHub(t *testing.T) {
	t.Parallel()
	remote := &fakeGitHub{}
	application, stdout, stderr, base := newBacklogApp(t, remote)

	exit := application.Run(context.Background(), append(base, "--nope"))

	if exit != 2 {
		t.Fatalf("exit = %d, want 2 for an unsupported option; stdout=%q", exit, stdout.String())
	}
	if len(remote.calls) != 0 {
		t.Fatalf("invalid arguments still queried GitHub: %v", remote.calls)
	}
	if !strings.Contains(stderr.String(), "unknown option") {
		t.Fatalf("stderr = %q, want the reason named", stderr.String())
	}
}

func TestBacklogListsTheAuthenticatedUsersOpenIssues(t *testing.T) {
	t.Parallel()
	remote := &fakeGitHub{issues: `[
		{"number":293,"title":"Skin Makeover","author":{"login":"johnjallday"},"labels":[],
		 "url":"https://github.com/johnjallday/ori-agent/issues/293",
		 "createdAt":"2026-08-02T23:37:08Z","updatedAt":"2026-08-02T23:37:08Z"},
		{"number":292,"title":"Coordinate based map","author":{"login":"johnjallday"},
		 "labels":[{"name":"idea"}],
		 "url":"https://github.com/johnjallday/ori-agent/issues/292",
		 "createdAt":"2026-08-02T23:06:49Z","updatedAt":"2026-08-02T23:06:49Z"}
	]`}
	application, stdout, stderr, base := newBacklogApp(t, remote)

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{
		"johnjallday/ori-agent", "2 open Issues", "by @me",
		"#293", "Skin Makeover", "#292", "Coordinate based map", "idea",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("listing = %q, want it to contain %q", output, want)
		}
	}
	// The repository was resolved from the checkout and then named explicitly,
	// so no configured default repository could have answered instead.
	if len(remote.calls) != 2 {
		t.Fatalf("calls = %v, want one resolution and one listing", remote.calls)
	}
	if strings.Join(remote.calls[1], " ") !=
		"issue list --repo johnjallday/ori-agent --state open --limit 100 --json "+
			"number,title,author,labels,url,createdAt,updatedAt --author @me" {
		t.Fatalf("listing args = %v", remote.calls[1])
	}
}

func TestBacklogAllScopeWidensOnlyTheAuthor(t *testing.T) {
	t.Parallel()
	remote := &fakeGitHub{issues: `[{"number":10,"title":"from a collaborator",
		"author":{"login":"someone-else"},"labels":[],
		"url":"https://github.com/johnjallday/ori-agent/issues/10",
		"updatedAt":"2026-08-01T10:00:00Z"}]`}
	application, stdout, stderr, base := newBacklogApp(t, remote)

	if exit := application.Run(context.Background(), append(base, "--all")); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "by all authors") {
		t.Fatalf("listing = %q, want the widened scope stated in the header", stdout.String())
	}
	listing := strings.Join(remote.calls[1], " ")
	if strings.Contains(listing, "--author") {
		t.Fatalf("listing args = %q, want no author filter", listing)
	}
	if !strings.Contains(listing, "--state open") || !strings.Contains(listing, "--repo johnjallday/ori-agent") {
		t.Fatalf("listing args = %q, want the same repository and open-only state", listing)
	}
}

func TestBacklogSaysNothingMatchedWithoutSoundingBroken(t *testing.T) {
	t.Parallel()
	remote := &fakeGitHub{issues: "[]"}
	application, stdout, stderr, base := newBacklogApp(t, remote)

	// An empty backlog is a successful answer, so it exits 0 — a script must
	// not treat "no ideas today" as a failure.
	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0 for an empty backlog; stderr=%q", exit, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "0 open Issues") || !strings.Contains(output, "GitHub returned no open Issues") {
		t.Fatalf("listing = %q, want it to say GitHub answered with nothing", output)
	}
	if !strings.Contains(output, "wt backlog add") {
		t.Fatalf("listing = %q, want the empty state to say how to fill it", output)
	}
}

func TestBacklogReportsTruncationRatherThanImplyingCompleteness(t *testing.T) {
	t.Parallel()
	var rows []string
	for number := 1; number <= github.DefaultIssueLimit; number++ {
		rows = append(rows, fmt.Sprintf(
			`{"number":%d,"title":"idea %d","author":{"login":"johnjallday"},"labels":[],
			  "url":"https://github.com/johnjallday/ori-agent/issues/%d",
			  "updatedAt":"2026-08-02T10:00:00Z"}`, number, number, number))
	}
	remote := &fakeGitHub{issues: "[" + strings.Join(rows, ",") + "]"}
	application, stdout, _, base := newBacklogApp(t, remote)

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if !strings.Contains(stdout.String(), "More open Issues matched") {
		t.Fatalf("listing = %q, want a truncation notice", stdout.String())
	}

	// The JSON form says the same thing in its own contract.
	jsonApp, jsonOut, _, jsonBase := newBacklogApp(t, &fakeGitHub{issues: remote.issues})
	if exit := jsonApp.Run(context.Background(), append(jsonBase, "--json")); exit != 0 {
		t.Fatalf("json exit = %d, want 0", exit)
	}
	var payload backlogListPayload
	if err := json.Unmarshal(jsonOut.Bytes(), &payload); err != nil {
		t.Fatalf("json = %q: %v", jsonOut.String(), err)
	}
	if !payload.Truncated || payload.Complete {
		t.Fatalf("payload truncated=%v complete=%v, want a capped listing reported honestly",
			payload.Truncated, payload.Complete)
	}
}

func TestBacklogJSONIsTheOwnedEnvelopeNotRawCLIOutput(t *testing.T) {
	t.Parallel()
	remote := &fakeGitHub{issues: `[{"number":292,"title":"Coordinate based map",
		"author":{"login":"johnjallday"},"labels":[],
		"url":"https://github.com/johnjallday/ori-agent/issues/292",
		"createdAt":"2026-08-02T23:06:49Z","updatedAt":"2026-08-02T23:06:49Z",
		"assignees":[{"login":"johnjallday"}],"body":"a description gh never sent"}]`}
	application, stdout, stderr, base := newBacklogApp(t, remote)

	if exit := application.Run(context.Background(), append(base, "--json")); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr.String())
	}

	var payload backlogListPayload
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout = %q is not the envelope: %v", stdout.String(), err)
	}
	if payload.SchemaVersion != backlogSchemaVersion || payload.Repository != "johnjallday/ori-agent" ||
		payload.AuthorScope != "me" || payload.State != "open" || !payload.Complete {
		t.Fatalf("payload = %+v, want the schema-versioned envelope", payload)
	}
	if payload.ObservedAt == "" {
		t.Fatal("payload carried no observation time")
	}
	// Fields Ori does not promise must not leak through just because `gh`
	// happened to include them.
	for _, unwanted := range []string{"assignees", "body", "isPinned"} {
		if strings.Contains(stdout.String(), unwanted) {
			t.Fatalf("payload passed raw CLI output through: %s", stdout.String())
		}
	}
	// Human prose belongs to the human surface, not to the machine one.
	if strings.Contains(stdout.String(), "Ori backlog") {
		t.Fatalf("JSON output contained the human header: %s", stdout.String())
	}
}

func TestBacklogOutputIsPlainTextWhenItIsNotATerminal(t *testing.T) {
	t.Parallel()
	remote := &fakeGitHub{issues: `[{"number":292,"title":"Coordinate based map",
		"author":{"login":"johnjallday"},"labels":[{"name":"idea"}],
		"url":"https://github.com/johnjallday/ori-agent/issues/292",
		"updatedAt":"2026-08-02T23:06:49Z"}]`}
	application, stdout, _, base := newBacklogApp(t, remote)

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	// A buffer is not a terminal, which is what a pipe and a redirect look
	// like: the listing must stay something grep and less can read.
	if strings.ContainsRune(stdout.String(), '\x1b') {
		t.Fatalf("redirected output carried escape sequences: %q", stdout.String())
	}
}

func TestBacklogColorIsDecorationOnlyAndNeverChangesTheReading(t *testing.T) {
	t.Parallel()
	list := github.IssueList{
		Repository: github.Repository{Owner: "johnjallday", Name: "ori-agent"},
		Scope:      github.ScopeMe,
		State:      github.StateOpen,
		Complete:   true,
		ObservedAt: time.Date(2026, 8, 2, 23, 40, 0, 0, time.UTC),
		Issues: []github.Issue{{
			Number: 292, Title: "Coordinate based map", Author: "johnjallday",
			Labels:    []string{"idea"},
			UpdatedAt: time.Date(2026, 8, 2, 23, 6, 49, 0, time.UTC),
		}},
	}

	var plain, colored bytes.Buffer
	plainApp := New(Dependencies{Stdout: &plain, LookupEnv: func(string) (string, bool) { return "", false }})
	plainApp.renderBacklogListStyled(list, backlogStyle{})
	coloredApp := New(Dependencies{Stdout: &colored, LookupEnv: func(string) (string, bool) { return "", false }})
	coloredApp.renderBacklogListStyled(list, backlogStyle{
		bold: "\x1b[1m", dim: "\x1b[2m", cyan: "\x1b[36m", reset: "\x1b[0m",
	})

	if strings.ContainsRune(plain.String(), '\x1b') {
		t.Fatalf("the plain palette still emitted escapes: %q", plain.String())
	}
	if !strings.Contains(colored.String(), "\x1b[36m") {
		t.Fatalf("the colored palette emitted no color: %q", colored.String())
	}
	// Stripping the escapes must recover exactly the plain rendering: color may
	// not add, drop, or reorder anything a reader relies on.
	stripped := colored.String()
	for _, code := range []string{"\x1b[1m", "\x1b[2m", "\x1b[36m", "\x1b[0m"} {
		stripped = strings.ReplaceAll(stripped, code, "")
	}
	if stripped != plain.String() {
		t.Fatalf("colored listing differs beyond decoration:\n%q\n%q", stripped, plain.String())
	}
}

func TestBacklogFailsLoudlyRatherThanShowingAnEmptyBacklog(t *testing.T) {
	t.Parallel()
	remote := &fakeGitHub{failure: errNoNetwork{}}
	application, stdout, stderr, base := newBacklogApp(t, remote)

	exit := application.Run(context.Background(), base)

	if exit != 1 {
		t.Fatalf("exit = %d, want 1 when GitHub cannot answer", exit)
	}
	if strings.Contains(stdout.String(), "0 open Issues") {
		t.Fatalf("a failed query was rendered as an empty backlog: %q", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("a failed query printed no reason")
	}
	if !strings.Contains(stderr.String(), "Recovery:") {
		t.Fatalf("stderr = %q, want a recovery action", stderr.String())
	}

	// The JSON form reports the same failure in the machine contract.
	jsonApp, jsonOut, _, jsonBase := newBacklogApp(t, &fakeGitHub{failure: errNoNetwork{}})
	if exit := jsonApp.Run(context.Background(), append(jsonBase, "--json")); exit != 1 {
		t.Fatalf("json exit = %d, want 1", exit)
	}
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
		Error         struct {
			Code     string `json:"code"`
			Message  string `json:"message"`
			Recovery string `json:"recovery"`
		} `json:"error"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &envelope); err != nil {
		t.Fatalf("json = %q: %v", jsonOut.String(), err)
	}
	if envelope.Error.Code == "" || envelope.Error.Recovery == "" || envelope.SchemaVersion != backlogSchemaVersion {
		t.Fatalf("error envelope = %+v, want a classified, versioned failure", envelope)
	}
	if strings.Contains(jsonOut.String(), `"issues"`) {
		t.Fatalf("a failed query still emitted an issues array: %s", jsonOut.String())
	}
}

// errNoNetwork stands in for a `gh` invocation that could not be run at all.
type errNoNetwork struct{}

func (errNoNetwork) Error() string { return "gh could not be started" }

// TestBacklogListPayloadIsAStableSchemaVersionOneEnvelope pins the JSON
// contract itself, separately from any command that emits it.
//
// A published contract is only useful if it is the same shape every time, so
// the two states that most often break a consumer are asserted directly: an
// Issue with no labels and a backlog with no Issues must both encode as empty
// arrays rather than as `null`.
func TestBacklogListPayloadIsAStableSchemaVersionOneEnvelope(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 2, 23, 6, 49, 0, time.UTC)
	updated := time.Date(2026, 8, 2, 23, 40, 0, 0, time.UTC)
	list := github.IssueList{
		Repository: github.Repository{Owner: "johnjallday", Name: "ori-agent"},
		Scope:      github.ScopeMe,
		State:      github.StateOpen,
		ObservedAt: updated,
		Complete:   true,
		Issues: []github.Issue{{
			Number:    292,
			Title:     "Coordinate based map",
			Author:    "johnjallday",
			URL:       "https://github.com/johnjallday/ori-agent/issues/292",
			CreatedAt: created,
			UpdatedAt: created,
		}},
	}

	encoded, err := json.Marshal(newBacklogListPayload(list))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}

	for key, want := range map[string]any{
		"schema_version": float64(backlogSchemaVersion),
		"repository":     "johnjallday/ori-agent",
		"author_scope":   "me",
		"state":          "open",
		"complete":       true,
		"truncated":      false,
		"observed_at":    "2026-08-02T23:40:00Z",
	} {
		if decoded[key] != want {
			t.Fatalf("%s = %#v, want %#v", key, decoded[key], want)
		}
	}

	if strings.Contains(string(encoded), "null") {
		t.Fatalf("payload encoded a null where an array belongs: %s", encoded)
	}
	if strings.Contains(string(encoded), `"body"`) {
		t.Fatalf("list payload carried an Issue body: %s", encoded)
	}

	issues, ok := decoded["issues"].([]any)
	if !ok || len(issues) != 1 {
		t.Fatalf("issues = %#v, want one item", decoded["issues"])
	}
	issue, ok := issues[0].(map[string]any)
	if !ok {
		t.Fatalf("issue = %#v, want an object", issues[0])
	}
	for key, want := range map[string]any{
		"number":     float64(292),
		"title":      "Coordinate based map",
		"author":     "johnjallday",
		"url":        "https://github.com/johnjallday/ori-agent/issues/292",
		"created_at": "2026-08-02T23:06:49Z",
		"updated_at": "2026-08-02T23:06:49Z",
	} {
		if issue[key] != want {
			t.Fatalf("issue.%s = %#v, want %#v", key, issue[key], want)
		}
	}
	if labels, ok := issue["labels"].([]any); !ok || len(labels) != 0 {
		t.Fatalf("labels = %#v, want an empty array for an unlabelled Issue", issue["labels"])
	}

	// An empty backlog is a successful result with an empty array, not a null
	// and not a missing key.
	empty, err := json.Marshal(newBacklogListPayload(github.IssueList{
		Repository: github.Repository{Owner: "johnjallday", Name: "ori-agent"},
		Scope:      github.ScopeAll,
		State:      github.StateOpen,
		Complete:   true,
	}))
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if !strings.Contains(string(empty), `"issues":[]`) {
		t.Fatalf("empty backlog = %s, want an empty issues array", empty)
	}
	// An absent observation time is stated as absent rather than rendered as
	// the zero year, which reads like a real date.
	if !strings.Contains(string(empty), `"observed_at":""`) {
		t.Fatalf("empty backlog = %s, want an empty observed_at", empty)
	}
}
