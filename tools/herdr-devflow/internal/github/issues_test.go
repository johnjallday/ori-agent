package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// recordingClient answers one payload and records every argument vector it was
// asked to run, so a test can assert both the result and the exact question.
type recordingClient struct {
	*Client
	calls [][]string
}

func newRecordingClient(t *testing.T, payload string, options Options) *recordingClient {
	t.Helper()
	recorder := &recordingClient{}
	options.Run = func(_ context.Context, args ...string) ([]byte, error) {
		recorder.calls = append(recorder.calls, args)
		return []byte(payload), nil
	}
	recorder.Client = New(options)
	return recorder
}

func TestResolveRepositoryNamesTheCheckoutsOwnRepository(t *testing.T) {
	client := newRecordingClient(t, `{"name":"ori-agent","owner":{"login":"johnjallday"}}`, Options{})

	repository, err := client.ResolveRepository(context.Background())
	if err != nil {
		t.Fatalf("ResolveRepository: %v", err)
	}
	if repository.Slug() != "johnjallday/ori-agent" {
		t.Fatalf("repository = %q, want johnjallday/ori-agent", repository.Slug())
	}

	if len(client.calls) != 1 {
		t.Fatalf("made %d calls, want exactly one bounded query", len(client.calls))
	}
	joined := strings.Join(client.calls[0], " ")
	if joined != "repo view --json owner,name" {
		t.Fatalf("args = %q, want the checkout's own repository asked for by structured fields", joined)
	}
	// Naming a repository on the command line would mean the answer came from
	// somewhere other than the checkout the user is standing in.
	for _, arg := range client.calls[0] {
		if arg == "--repo" || strings.Contains(arg, "/") && arg != "owner,name" {
			t.Fatalf("resolution passed a repository of its own: %v", client.calls[0])
		}
	}
}

func TestResolveRepositoryRefusesAnUnusableAnswer(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    ErrorKind
	}{
		{"no owner", `{"name":"ori-agent","owner":{"login":""}}`, ErrorRepository},
		{"no name", `{"name":"","owner":{"login":"johnjallday"}}`, ErrorRepository},
		{"owner carrying a path", `{"name":"ori-agent","owner":{"login":"a/b"}}`, ErrorRepository},
		{"not an object", `["ori-agent"]`, ErrorMalformed},
		{"not JSON at all", `Welcome to GitHub CLI`, ErrorMalformed},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := newRecordingClient(t, testCase.payload, Options{})

			repository, err := client.ResolveRepository(context.Background())

			var remoteErr *Error
			if !errors.As(err, &remoteErr) {
				t.Fatalf("err = %v, want a classified error", err)
			}
			if remoteErr.Kind != testCase.want {
				t.Fatalf("kind = %q, want %q", remoteErr.Kind, testCase.want)
			}
			if !repository.Empty() {
				t.Fatalf("repository = %#v, want nothing usable from a refused answer", repository)
			}
		})
	}
}

func TestResolveRepositoryRefusesAnOversizedResponse(t *testing.T) {
	client := newRecordingClient(t, strings.Repeat("a", maxRepositoryOutputBytes+1), Options{})

	_, err := client.ResolveRepository(context.Background())

	var remoteErr *Error
	if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorMalformed {
		t.Fatalf("err = %v, want an oversized response refused before decoding", err)
	}
}

func TestResolveRepositoryDistinguishesAnUnresolvableCheckout(t *testing.T) {
	// This is what `gh` does in a directory that is not a GitHub-backed
	// checkout. It must not read as a network problem, because retrying will
	// never fix it.
	client := New(Options{Run: func(context.Context, ...string) ([]byte, error) {
		return nil, exitError(t, "failed to run git: fatal: not a git repository")
	}})

	_, err := client.ResolveRepository(context.Background())

	var remoteErr *Error
	if !errors.As(err, &remoteErr) {
		t.Fatalf("err = %v, want a classified error", err)
	}
	if remoteErr.Kind != ErrorRepository {
		t.Fatalf("kind = %q, want %q", remoteErr.Kind, ErrorRepository)
	}
	if !strings.Contains(remoteErr.Recovery(), "gh repo view") {
		t.Fatalf("recovery = %q, want a concrete way to check the checkout", remoteErr.Recovery())
	}
	if strings.Contains(remoteErr.Detail, "not a git repository") {
		t.Fatalf("detail = %q, want the raw CLI text left out", remoteErr.Detail)
	}
}

func TestResolveRepositoryKeepsTheSharedFailureClasses(t *testing.T) {
	cases := []struct {
		name string
		run  Runner
		want ErrorKind
	}{
		{
			name: "missing binary",
			run:  func(context.Context, ...string) ([]byte, error) { return nil, exec.ErrNotFound },
			want: ErrorMissing,
		},
		{
			name: "unauthenticated",
			run: func(context.Context, ...string) ([]byte, error) {
				return nil, exitError(t, "gh: To get started with GitHub CLI, please run: gh auth login")
			},
			want: ErrorUnauthenticated,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := New(Options{Run: testCase.run}).ResolveRepository(context.Background())

			var remoteErr *Error
			if !errors.As(err, &remoteErr) || remoteErr.Kind != testCase.want {
				t.Fatalf("err = %v, want kind %q", err, testCase.want)
			}
			if remoteErr.Recovery() == "" {
				t.Fatal("a classified failure carried no recovery action")
			}
		})
	}
}

func TestResolveRepositoryIsBoundedByTheClientTimeout(t *testing.T) {
	client := New(Options{
		Timeout: 10 * time.Millisecond,
		Run: func(ctx context.Context, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})

	_, err := client.ResolveRepository(context.Background())

	var remoteErr *Error
	if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorTimeout {
		t.Fatalf("err = %v, want a timeout error", err)
	}
}

// oriAgent is the repository fixture every Issue test queries. Using one
// constant keeps the tests from asserting against the live repository.
var oriAgent = Repository{Owner: "johnjallday", Name: "ori-agent"}

func TestListIssuesAsksOneBoundedQuestionAboutThisRepository(t *testing.T) {
	client := newRecordingClient(t, "[]", Options{IssueLimit: 25})

	if _, err := client.ListIssues(context.Background(), oriAgent, ScopeMe); err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("made %d calls, want exactly one fresh query", len(client.calls))
	}
	args := client.calls[0]
	joined := strings.Join(args, " ")

	// `issue list` returns Issues; GitHub's search surfaces mix in pull
	// requests, which have no place in a product backlog.
	if !strings.HasPrefix(joined, "issue list ") {
		t.Fatalf("args = %q, want the Issue listing command", joined)
	}
	for _, want := range []string{
		"--repo johnjallday/ori-agent",
		"--state open",
		"--limit 25",
		"--author @me",
		"--json " + issueListFields,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args = %q, want them to contain %q", joined, want)
		}
	}

	// The default listing asks nothing of an Issue beyond being open and mine.
	// A backlog that required a label, a milestone, an assignee, a Project, or
	// an Issue type would hide every idea captured in ten seconds.
	for _, unwanted := range []string{
		"--label", "--milestone", "--assignee", "--mention", "--app",
		"--search", "--project", "--web", "--template",
	} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("args = %q, want no %s filter", joined, unwanted)
		}
	}
	// Requesting bodies would pull every description into memory to render a
	// list that prints none of them.
	if strings.Contains(joined, "body") {
		t.Fatalf("args = %q, want no body field requested", joined)
	}
	for _, arg := range args {
		if strings.ContainsAny(arg, ";|&`\n") {
			t.Fatalf("argument %q could be interpreted by a shell", arg)
		}
	}
}

func TestListIssuesAllScopeRemovesOnlyTheAuthorFilter(t *testing.T) {
	me := newRecordingClient(t, "[]", Options{IssueLimit: 25})
	if _, err := me.ListIssues(context.Background(), oriAgent, ScopeMe); err != nil {
		t.Fatalf("ListIssues(me): %v", err)
	}
	all := newRecordingClient(t, "[]", Options{IssueLimit: 25})
	if _, err := all.ListIssues(context.Background(), oriAgent, ScopeAll); err != nil {
		t.Fatalf("ListIssues(all): %v", err)
	}

	if strings.Join(all.calls[0], " ")+" --author @me" != strings.Join(me.calls[0], " ") {
		t.Fatalf("all-author args = %v\nme args = %v\nwant them to differ only by the author filter",
			all.calls[0], me.calls[0])
	}
	// Widening the author scope must not widen anything else.
	if !strings.Contains(strings.Join(all.calls[0], " "), "--state open") ||
		!strings.Contains(strings.Join(all.calls[0], " "), "--repo johnjallday/ori-agent") {
		t.Fatalf("all-author args = %v, want the same repository and open-only state", all.calls[0])
	}
}

func TestListIssuesReportsWhatItActuallyRead(t *testing.T) {
	payload := `[{"number":292,"title":"Coordinate based map","author":{"login":"johnjallday"},
		"labels":[],"url":"https://github.com/johnjallday/ori-agent/issues/292",
		"createdAt":"2026-08-02T23:06:49Z","updatedAt":"2026-08-02T23:06:49Z"}]`
	before := time.Now()
	client := newRecordingClient(t, payload, Options{})

	list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}

	if list.Repository != oriAgent || list.Scope != ScopeMe || list.State != StateOpen {
		t.Fatalf("list identity = %+v, want the queried repository, scope, and state", list)
	}
	if !list.Complete || list.Truncated {
		t.Fatalf("complete=%v truncated=%v, want a short listing reported as complete", list.Complete, list.Truncated)
	}
	if list.ObservedAt.Before(before) {
		t.Fatalf("observed at %v, want the moment this fresh query completed", list.ObservedAt)
	}
	if len(list.Issues) != 1 {
		t.Fatalf("decoded %d issues, want 1", len(list.Issues))
	}
	issue := list.Issues[0]
	if issue.Number != 292 || issue.Title != "Coordinate based map" || issue.Author != "johnjallday" {
		t.Fatalf("issue = %+v", issue)
	}
	if issue.URL != "https://github.com/johnjallday/ori-agent/issues/292" {
		t.Fatalf("url = %q", issue.URL)
	}
	if issue.CreatedAt.IsZero() || issue.UpdatedAt.IsZero() {
		t.Fatalf("timestamps were dropped: %+v", issue)
	}
	// An Issue with no labels is an ordinary backlog entry, not a broken one.
	if issue.Labels == nil || len(issue.Labels) != 0 {
		t.Fatalf("labels = %#v, want an empty, non-nil list", issue.Labels)
	}
}

func TestListIssuesDistinguishesEmptyFromIncomplete(t *testing.T) {
	client := newRecordingClient(t, "[]", Options{})

	list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(list.Issues) != 0 {
		t.Fatalf("issues = %#v, want none", list.Issues)
	}
	// Nothing matching is a successful answer. Only a failed query is a
	// failure, and it never arrives as an empty list.
	if !list.Complete || list.Truncated {
		t.Fatalf("complete=%v truncated=%v, want an empty backlog reported as complete",
			list.Complete, list.Truncated)
	}
}

func TestListIssuesReportsTruncationAtTheBound(t *testing.T) {
	client := newRecordingClient(t,
		`[{"number":2,"updatedAt":"2026-08-02T10:00:00Z"},{"number":1,"updatedAt":"2026-08-01T10:00:00Z"}]`,
		Options{IssueLimit: 2})

	list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if !list.Truncated || list.Complete {
		t.Fatalf("truncated=%v complete=%v, want a full page reported as incomplete",
			list.Truncated, list.Complete)
	}
}

func TestListIssuesOrdersByMostRecentlyUpdatedThenNumberDescending(t *testing.T) {
	// The response arrives in GitHub's own order. Ori imposes its own, so the
	// same backlog always reads the same way — including when two Issues share
	// an update time to the second.
	payload := `[
		{"number":10,"title":"old","url":"https://github.com/johnjallday/ori-agent/issues/10","updatedAt":"2026-07-01T10:00:00Z"},
		{"number":11,"title":"tie low","url":"https://github.com/johnjallday/ori-agent/issues/11","updatedAt":"2026-08-02T09:00:00Z"},
		{"number":13,"title":"newest","url":"https://github.com/johnjallday/ori-agent/issues/13","updatedAt":"2026-08-02T23:00:00Z"},
		{"number":12,"title":"tie high","url":"https://github.com/johnjallday/ori-agent/issues/12","updatedAt":"2026-08-02T09:00:00Z"},
		{"number":9,"title":"undated","url":"https://github.com/johnjallday/ori-agent/issues/9"}
	]`
	client := newRecordingClient(t, payload, Options{})

	list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}

	var numbers []int
	for _, issue := range list.Issues {
		numbers = append(numbers, issue.Number)
	}
	want := []int{13, 12, 11, 10, 9}
	if fmt.Sprint(numbers) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v (updated desc, then number desc)", numbers, want)
	}

	// Determinism is the claim, so the same payload must produce the same order
	// however the map/slice internals happen to fall on a given run.
	for attempt := range 5 {
		repeat, err := newRecordingClient(t, payload, Options{}).
			ListIssues(context.Background(), oriAgent, ScopeMe)
		if err != nil {
			t.Fatalf("ListIssues repeat: %v", err)
		}
		var repeated []int
		for _, issue := range repeat.Issues {
			repeated = append(repeated, issue.Number)
		}
		if fmt.Sprint(repeated) != fmt.Sprint(want) {
			t.Fatalf("repeat %d order = %v, want %v", attempt, repeated, want)
		}
	}
}

func TestListIssuesRefusesToQueryWithoutAResolvedRepository(t *testing.T) {
	client := newRecordingClient(t, "[]", Options{})

	if _, err := client.ListIssues(context.Background(), Repository{}, ScopeMe); err == nil {
		t.Fatal("an unresolved repository was queried")
	}
	if _, err := client.ListIssues(context.Background(), oriAgent, AuthorScope("everyone")); err == nil {
		t.Fatal("an unsupported author scope was queried")
	}
	if len(client.calls) != 0 {
		t.Fatalf("made %d calls, want none before the arguments were valid", len(client.calls))
	}
}

func TestListIssuesDropsRowsThatCannotBeBacklogWork(t *testing.T) {
	// A pull request and a numberless row both arrive shaped like Issues. One
	// is delivery, not backlog; the other cannot be opened, viewed, or referred
	// to at all.
	payload := `[
		{"number":292,"title":"real","url":"https://github.com/johnjallday/ori-agent/issues/292"},
		{"number":291,"title":"a pull request","url":"https://github.com/johnjallday/ori-agent/pull/291"},
		{"number":0,"title":"no number","url":"https://github.com/johnjallday/ori-agent/issues/0"},
		{"number":-4,"title":"negative","url":"https://github.com/johnjallday/ori-agent/issues/-4"}
	]`
	client := newRecordingClient(t, payload, Options{})

	list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(list.Issues) != 1 || list.Issues[0].Number != 292 {
		t.Fatalf("issues = %+v, want only the real Issue", list.Issues)
	}
}

func TestListIssuesKeepsAnIssueWhoseTitleMentionsPull(t *testing.T) {
	// The pull-request exclusion matches the URL's path segment. A backlog idea
	// about "pull to refresh" is an ordinary Issue and must survive it.
	payload := `[{"number":300,"title":"pull to refresh the map",
		"url":"https://github.com/johnjallday/ori-agent/issues/300"}]`
	client := newRecordingClient(t, payload, Options{})

	list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(list.Issues) != 1 {
		t.Fatalf("issues = %+v, want the Issue kept", list.Issues)
	}
}

func TestListIssuesBoundsAndSanitizesEveryRemoteField(t *testing.T) {
	longTitle := strings.Repeat("t", maxTitleRunes+50)
	payload := `[{
		"number": 292,
		"title": "` + longTitle + `",
		"author": {"login": "user\u001b[31mred"},
		"labels": [
			{"name": "bug\u0000"}, {"name": "\u001b]0;pwned\u0007"}, {"name": ""},
			{"name": "l3"}, {"name": "l4"}, {"name": "l5"}, {"name": "l6"},
			{"name": "l7"}, {"name": "l8"}, {"name": "l9"}, {"name": "l10"},
			{"name": "l11"}, {"name": "l12"}
		],
		"url": "https://github.com/johnjallday/ori-agent/issues/292\u001b[2J",
		"createdAt": "2026-08-02T23:06:49Z",
		"updatedAt": "not a timestamp"
	}]`
	client := newRecordingClient(t, payload, Options{})

	list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	issue := list.Issues[0]

	for name, value := range map[string]string{
		"title": issue.Title, "author": issue.Author, "url": issue.URL,
	} {
		if strings.ContainsAny(value, "\x1b\x00\x07") {
			t.Fatalf("%s kept a control character: %q", name, value)
		}
	}
	if runes := []rune(issue.Title); len(runes) > maxTitleRunes+1 {
		t.Fatalf("title is %d runes, want it bounded to %d plus an ellipsis", len(runes), maxTitleRunes)
	}
	if !strings.HasSuffix(issue.Title, "…") {
		t.Fatalf("title = %q, want a truncated title marked as truncated", issue.Title)
	}
	if len(issue.Labels) != MaxLabelsPerIssue {
		t.Fatalf("labels = %#v, want the row bounded to %d", issue.Labels, MaxLabelsPerIssue)
	}
	for _, label := range issue.Labels {
		if label == "" || strings.ContainsAny(label, "\x1b\x00\x07") {
			t.Fatalf("labels = %#v, want every one non-empty and sanitized", issue.Labels)
		}
	}
	if issue.CreatedAt.IsZero() {
		t.Fatal("a valid timestamp was dropped")
	}
	// An unparsable timestamp stays absent rather than becoming "now", which
	// would make a stale Issue sort to the top of the backlog.
	if !issue.UpdatedAt.IsZero() {
		t.Fatalf("updated at %v, want the unreadable timestamp reported as absent", issue.UpdatedAt)
	}
}

func TestListIssuesStripsInvisibleReorderingCharacters(t *testing.T) {
	// A right-to-left override reorders a rendered line without appearing in
	// it, so a title can be made to read as something it is not.
	payload := `[{"number":292,"title":"safe\u202etitle\u2066spoof\u2069",
		"url":"https://github.com/johnjallday/ori-agent/issues/292"}]`
	client := newRecordingClient(t, payload, Options{})

	list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	for _, unwanted := range []rune{0x202e, 0x2066, 0x2069} {
		if strings.ContainsRune(list.Issues[0].Title, unwanted) {
			t.Fatalf("title = %q kept U+%04X", list.Issues[0].Title, unwanted)
		}
	}
}

func TestListIssuesBoundsHowManyIssuesItDecodes(t *testing.T) {
	// The request already carries a limit, but the response is remote data: the
	// process must hold only what it agreed to hold, whatever arrives.
	var rows []string
	for number := 1; number <= 40; number++ {
		rows = append(rows, fmt.Sprintf(
			`{"number":%d,"title":"idea %d","url":"https://github.com/johnjallday/ori-agent/issues/%d"}`,
			number, number, number))
	}
	client := newRecordingClient(t, "["+strings.Join(rows, ",")+"]", Options{IssueLimit: 5})

	list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(list.Issues) != 5 {
		t.Fatalf("decoded %d issues, want the configured bound of 5", len(list.Issues))
	}
	if !list.Truncated || list.Complete {
		t.Fatalf("truncated=%v complete=%v, want an over-long response reported honestly",
			list.Truncated, list.Complete)
	}
}

func TestListIssuesRejectsMalformedAndOversizedResponses(t *testing.T) {
	for name, payload := range map[string]string{
		"not an array":   `{"number":1}`,
		"not JSON":       `gh: something went wrong`,
		"oversized body": strings.Repeat("a", MaxOutputBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			client := newRecordingClient(t, payload, Options{})

			_, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)

			var remoteErr *Error
			if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorMalformed {
				t.Fatalf("err = %v, want a malformed-response error", err)
			}
		})
	}
}

func TestListIssuesClassifiesEveryFailureItCanBeGiven(t *testing.T) {
	// Each stderr below is the shape `gh` really produces. The classification
	// exists so the reader is told what to do, and the four HTTP failures are
	// four different actions: log in, ask for access, wait, or check the name.
	cases := []struct {
		name   string
		stderr string
		want   ErrorKind
	}{
		{"no credential", "gh: To get started with GitHub CLI, please run: gh auth login", ErrorUnauthenticated},
		{"expired credential", "HTTP 401: Bad credentials (https://api.github.com/graphql)", ErrorUnauthenticated},
		{"rate limited", "HTTP 403: API rate limit exceeded for user ID 1234", ErrorRateLimit},
		{"secondary rate limit", "You have exceeded a secondary limit. Please wait a few minutes", ErrorRateLimit},
		{"authorization denied", "HTTP 403: Resource not accessible by integration", ErrorForbidden},
		{"single sign-on required", "SAML enforcement: your token has not been authorized", ErrorForbidden},
		{"unknown repository", "HTTP 404: Not Found (https://api.github.com/repos/o/r/issues)", ErrorNotFound},
		{"offline", "dial tcp: lookup api.github.com: no such host", ErrorNetwork},
		{"tls failure", "Post \"https://api.github.com/graphql\": tls: failed to verify certificate", ErrorNetwork},
		{"github outage", "HTTP 502: Bad gateway", ErrorNetwork},
		{"unrecognized failure", "something nobody has seen before", ErrorNetwork},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			failure := exitError(t, testCase.stderr)
			client := New(Options{Run: func(context.Context, ...string) ([]byte, error) {
				return nil, failure
			}})

			list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)

			var remoteErr *Error
			if !errors.As(err, &remoteErr) {
				t.Fatalf("err = %v, want a classified error", err)
			}
			if remoteErr.Kind != testCase.want {
				t.Fatalf("kind = %q, want %q", remoteErr.Kind, testCase.want)
			}
			if remoteErr.Recovery() == "" {
				t.Fatal("a classified failure carried no recovery action")
			}
			// A failed query is never rendered as an empty backlog: the caller
			// gets nothing to print alongside the error.
			if len(list.Issues) != 0 || list.Complete {
				t.Fatalf("list = %+v, want no result from a failed query", list)
			}
		})
	}
}

func TestIssueFailuresNeverEchoRemoteTextOrCredentials(t *testing.T) {
	// `gh` prints request bodies and headers on failure. Everything a caller
	// sees is written by this package from the failure class alone.
	secret := "ghp_SUPERSECRETTOKENVALUE0000000000000000"
	stderr := "HTTP 403: Resource not accessible (Authorization: token " + secret +
		") body=" + strings.Repeat("x", 5000)
	client := New(Options{Run: func(context.Context, ...string) ([]byte, error) {
		return nil, exitError(t, stderr)
	}})

	_, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)

	var remoteErr *Error
	if !errors.As(err, &remoteErr) {
		t.Fatalf("err = %v, want a classified error", err)
	}
	for _, rendered := range []string{remoteErr.Error(), remoteErr.Detail, remoteErr.Recovery()} {
		for _, leak := range []string{secret, "Authorization", "body="} {
			if strings.Contains(rendered, leak) {
				t.Fatalf("%q survived into %q", leak, rendered)
			}
		}
		if len(rendered) > 400 {
			t.Fatalf("a %d-character diagnostic is not bounded: %q", len(rendered), rendered)
		}
	}
}

func TestListIssuesClassifiesATimeoutRatherThanAnEmptyBacklog(t *testing.T) {
	client := New(Options{
		Timeout: 10 * time.Millisecond,
		Run: func(ctx context.Context, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})

	list, err := client.ListIssues(context.Background(), oriAgent, ScopeMe)

	var remoteErr *Error
	if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorTimeout {
		t.Fatalf("err = %v, want a timeout error", err)
	}
	if list.Complete {
		t.Fatalf("list = %+v, want a timeout reported as a failure, not as an empty backlog", list)
	}
}

func TestViewIssueReadsOneIssueOfThisRepositoryInFull(t *testing.T) {
	payload := `{
		"number":292,"title":"Coordinate based map","author":{"login":"johnjallday"},
		"labels":[{"name":"idea"}],"url":"https://github.com/johnjallday/ori-agent/issues/292",
		"createdAt":"2026-08-02T23:06:49Z","updatedAt":"2026-08-02T23:10:00Z",
		"state":"OPEN","stateReason":"","closedAt":"",
		"body":"Show stations at real coordinates.\n\n- [ ] pick a projection\n"
	}`
	client := newRecordingClient(t, payload, Options{})

	detail, err := client.ViewIssue(context.Background(), oriAgent, 292)
	if err != nil {
		t.Fatalf("ViewIssue: %v", err)
	}

	if detail.Number != 292 || detail.Title != "Coordinate based map" || detail.Author != "johnjallday" {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.State != StateOpen || detail.StateReason != "" || !detail.ClosedAt.IsZero() {
		t.Fatalf("state = %q reason = %q closed = %v", detail.State, detail.StateReason, detail.ClosedAt)
	}
	// The body is the reason `view` exists, and its Markdown structure is what
	// makes it readable.
	if !strings.Contains(detail.Body, "- [ ] pick a projection") || !strings.Contains(detail.Body, "\n") {
		t.Fatalf("body = %q, want the Markdown preserved as text", detail.Body)
	}

	joined := strings.Join(client.calls[0], " ")
	if joined != "issue view 292 --repo johnjallday/ori-agent --json "+issueDetailFields {
		t.Fatalf("args = %q", joined)
	}
	if strings.Contains(joined, "--web") || strings.Contains(joined, "--comments") {
		t.Fatalf("args = %q, want no browser and no comment fetch", joined)
	}
}

func TestViewIssueShowsAClosedIssueAndItsReason(t *testing.T) {
	// The open filter is what a backlog listing means. It is not a rule about
	// what you may look at: a closed Issue is exactly what you want to read
	// when you are checking whether something already shipped.
	payload := `{
		"number":270,"title":"Downloads Janitor","author":{"login":"johnjallday"},"labels":[],
		"url":"https://github.com/johnjallday/ori-agent/issues/270",
		"createdAt":"2026-07-20T10:00:00Z","updatedAt":"2026-07-26T10:00:00Z",
		"state":"CLOSED","stateReason":"COMPLETED","closedAt":"2026-07-26T10:00:00Z",
		"body":"done"
	}`
	client := newRecordingClient(t, payload, Options{})

	detail, err := client.ViewIssue(context.Background(), oriAgent, 270)
	if err != nil {
		t.Fatalf("ViewIssue: %v", err)
	}
	if detail.State != StateClosed || detail.StateReason != "COMPLETED" || detail.ClosedAt.IsZero() {
		t.Fatalf("detail = %+v, want a closed Issue with its reason and time", detail)
	}
}

func TestViewIssueRefusesAnIssueFromAnotherRepository(t *testing.T) {
	// Issue numbers are repository-local, so #292 exists in thousands of
	// repositories. Showing the wrong one is worse than showing none.
	payload := `{"number":292,"title":"someone else's issue","author":{"login":"stranger"},
		"labels":[],"url":"https://github.com/other/project/issues/292",
		"state":"OPEN","body":"not ours"}`
	client := newRecordingClient(t, payload, Options{})

	_, err := client.ViewIssue(context.Background(), oriAgent, 292)

	var remoteErr *Error
	if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorNotFound {
		t.Fatalf("err = %v, want the wrong-repository Issue refused", err)
	}
}

func TestViewIssueRefusesAnAnswerAboutADifferentNumber(t *testing.T) {
	payload := `{"number":11,"title":"a different issue","author":{"login":"johnjallday"},
		"labels":[],"url":"https://github.com/johnjallday/ori-agent/issues/11","state":"OPEN"}`
	client := newRecordingClient(t, payload, Options{})

	_, err := client.ViewIssue(context.Background(), oriAgent, 292)

	var remoteErr *Error
	if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorMalformed {
		t.Fatalf("err = %v, want an answer about the wrong Issue refused", err)
	}
}

func TestViewIssueBoundsAndSanitizesHostileContent(t *testing.T) {
	body := strings.Repeat("b", MaxBodyLength+500)
	payload := `{"number":292,"title":"title\u001b[31m","author":{"login":"a\u0000b"},
		"labels":[{"name":"l\u0007"}],
		"url":"https://github.com/johnjallday/ori-agent/issues/292",
		"state":"OPEN","stateReason":"\u001b]0;pwned\u0007",
		"body":"line one\u001b[2Jline two\u202espoof\n` + body + `"}`
	client := newRecordingClient(t, payload, Options{})

	detail, err := client.ViewIssue(context.Background(), oriAgent, 292)
	if err != nil {
		t.Fatalf("ViewIssue: %v", err)
	}

	for name, value := range map[string]string{
		"title": detail.Title, "author": detail.Author,
		"state reason": detail.StateReason, "body": detail.Body,
	} {
		if strings.ContainsAny(value, "\x1b\x00\x07") || strings.ContainsRune(value, 0x202e) {
			t.Fatalf("%s kept a control character: %q", name, value)
		}
	}
	if len([]rune(detail.Body)) > MaxBodyLength+2 {
		t.Fatalf("body is %d runes, want it bounded to %d", len([]rune(detail.Body)), MaxBodyLength)
	}
	// Sanitizing a body must not flatten it: the line structure is the content.
	if !strings.Contains(detail.Body, "line one") || !strings.Contains(detail.Body, "\n") {
		t.Fatalf("body = %q, want its lines preserved", detail.Body[:80])
	}
}

func TestViewIssueRejectsUnusableRequestsAndResponses(t *testing.T) {
	client := newRecordingClient(t, "{}", Options{})
	for _, number := range []int{0, -1} {
		if _, err := client.ViewIssue(context.Background(), oriAgent, number); err == nil {
			t.Fatalf("ViewIssue(%d) was accepted", number)
		}
	}
	if _, err := client.ViewIssue(context.Background(), Repository{}, 292); err == nil {
		t.Fatal("ViewIssue accepted an unresolved repository")
	}
	if len(client.calls) != 0 {
		t.Fatalf("made %d calls, want none before the request was valid", len(client.calls))
	}

	for name, payload := range map[string]string{
		"not JSON":  `gh: something went wrong`,
		"no Issue":  `{}`,
		"oversized": strings.Repeat("a", maxIssueOutputBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newRecordingClient(t, payload, Options{}).
				ViewIssue(context.Background(), oriAgent, 292)
			var remoteErr *Error
			if !errors.As(err, &remoteErr) {
				t.Fatalf("err = %v, want a classified error", err)
			}
		})
	}
}

func TestCreateIssueSendsOnlyATitleAndBody(t *testing.T) {
	client := newRecordingClient(t,
		"https://github.com/johnjallday/ori-agent/issues/294\n", Options{})

	created, err := client.CreateIssue(context.Background(), oriAgent,
		"  Coordinate based map  ", "Show stations at real coordinates.\n\n- [ ] projection")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if created.Number != 294 || created.Title != "Coordinate based map" ||
		created.State != StateOpen || created.Repository != oriAgent {
		t.Fatalf("created = %+v", created)
	}
	if created.URL != "https://github.com/johnjallday/ori-agent/issues/294" {
		t.Fatalf("url = %q", created.URL)
	}

	args := client.calls[0]
	if args[0] != "issue" || args[1] != "create" {
		t.Fatalf("args = %v, want the creation command", args)
	}
	// The title and body are their own argument-vector elements, so the body
	// keeps its newlines and neither is ever a fragment of a command string.
	if !slicesContainPair(args, "--title", "Coordinate based map") {
		t.Fatalf("args = %v, want the trimmed title passed as data", args)
	}
	if !slicesContainPair(args, "--body", "Show stations at real coordinates.\n\n- [ ] projection") {
		t.Fatalf("args = %v, want the body passed verbatim", args)
	}
	if !slicesContainPair(args, "--repo", "johnjallday/ori-agent") {
		t.Fatalf("args = %v, want the repository named explicitly", args)
	}

	// Everything this command deliberately does not do.
	for _, unwanted := range []string{
		"--label", "--assignee", "--milestone", "--project", "--template",
		"--editor", "--web", "--recover", "--body-file",
	} {
		if slices.Contains(args, unwanted) {
			t.Fatalf("args = %v, want no %s", args, unwanted)
		}
	}
}

func TestCreateIssueOmitsTheBodyFlagWhenThereIsNoBody(t *testing.T) {
	client := newRecordingClient(t,
		"https://github.com/johnjallday/ori-agent/issues/295\n", Options{})

	if _, err := client.CreateIssue(context.Background(), oriAgent, "Just a title", "   "); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if slices.Contains(client.calls[0], "--body") {
		t.Fatalf("args = %v, want no empty body flag", client.calls[0])
	}
}

func TestCreateIssueTreatsHostileTitlesAndBodiesAsData(t *testing.T) {
	hostile := []struct {
		name  string
		title string
		body  string
	}{
		{"command substitution", "$(rm -rf /) idea", "backtick `id` and $(whoami)"},
		{"shell operators", "a; b && c | d > e", "pipe | and semicolon ;"},
		{"quotes", `he said "quote" and 'quote'`, `mixed "quotes" everywhere`},
		{"markdown and urls", "fix the map", "See https://example.test/a_b?c=d&e=f\n\n```go\nfmt.Println()\n```"},
	}
	for _, testCase := range hostile {
		t.Run(testCase.name, func(t *testing.T) {
			client := newRecordingClient(t,
				"https://github.com/johnjallday/ori-agent/issues/296\n", Options{})

			created, err := client.CreateIssue(context.Background(), oriAgent, testCase.title, testCase.body)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			if created.Number != 296 {
				t.Fatalf("created = %+v", created)
			}
			// Every character arrives at `gh` unchanged, as one argument. It is
			// data in an Issue, not syntax on a machine.
			if !slicesContainPair(client.calls[0], "--title", testCase.title) {
				t.Fatalf("title was altered: %v", client.calls[0])
			}
			if !slicesContainPair(client.calls[0], "--body", testCase.body) {
				t.Fatalf("body was altered: %v", client.calls[0])
			}
		})
	}
}

func TestCreateIssueStripsTerminalEscapesAndFlattensTitleLines(t *testing.T) {
	client := newRecordingClient(t,
		"https://github.com/johnjallday/ori-agent/issues/297\n", Options{})

	created, err := client.CreateIssue(context.Background(), oriAgent,
		"first line\nsecond line\x1b[31m", "body\x1b[2J stays\nmulti-line")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	// A title is one line, so the break becomes a space rather than vanishing
	// and running two words together into a third nobody typed. The escape
	// character is removed and its printable remainder is left alone: "[31m"
	// with no escape ahead of it is text, and rewriting somebody's text further
	// than safety requires is its own kind of wrong.
	if created.Title != "first line second line[31m" {
		t.Fatalf("title = %q", created.Title)
	}
	for _, arg := range client.calls[0] {
		if strings.ContainsRune(arg, 0x1b) {
			t.Fatalf("an escape sequence was sent to GitHub: %q", arg)
		}
	}
	if !slicesContainPair(client.calls[0], "--body", "body[2J stays\nmulti-line") {
		t.Fatalf("body = %v, want its lines kept and its escapes removed", client.calls[0])
	}
}

func TestCreateIssueRefusesInputItCannotFaithfullyCreate(t *testing.T) {
	client := newRecordingClient(t, "", Options{})
	cases := map[string]struct{ title, body string }{
		"no title":           {"", ""},
		"whitespace title":   {"   \n\t ", ""},
		"escapes-only title": {"\x1b\x00", ""},
		"title too long":     {strings.Repeat("t", MaxTitleLength+1), ""},
		"body too long":      {"fine", strings.Repeat("b", MaxBodyLength+1)},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := client.CreateIssue(context.Background(), oriAgent, testCase.title, testCase.body); err == nil {
				t.Fatal("unusable input was sent to GitHub")
			}
		})
	}
	if len(client.calls) != 0 {
		t.Fatalf("made %d calls, want nothing created from invalid input", len(client.calls))
	}
}

func TestCreateIssueRefusesAnUnconfirmableResult(t *testing.T) {
	// If the address of the new Issue cannot be read back, the command must not
	// report a number it guessed. Something may still have been created, and
	// saying so honestly is the only safe answer.
	for name, payload := range map[string]string{
		"nothing printed":     "",
		"no URL":              "Creating issue in johnjallday/ori-agent\n",
		"another repository":  "https://github.com/other/project/issues/12\n",
		"not an Issue at all": "https://github.com/johnjallday/ori-agent/pull/12\n",
	} {
		t.Run(name, func(t *testing.T) {
			client := newRecordingClient(t, payload, Options{})

			created, err := client.CreateIssue(context.Background(), oriAgent, "an idea", "")

			var remoteErr *Error
			if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorMalformed {
				t.Fatalf("err = %v, want an unconfirmable creation reported as such", err)
			}
			if created.Number != 0 {
				t.Fatalf("created = %+v, want no invented identity", created)
			}
		})
	}
}

func TestCreateIssueClassifiesFailuresWithoutLeakingOutput(t *testing.T) {
	secret := "ghp_SUPERSECRETTOKENVALUE0000000000000000"
	client := New(Options{Run: func(context.Context, ...string) ([]byte, error) {
		return nil, exitError(t, "HTTP 403: Resource not accessible (Authorization: token "+secret+")")
	}})

	_, err := client.CreateIssue(context.Background(), oriAgent, "an idea", "")

	var remoteErr *Error
	if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorForbidden {
		t.Fatalf("err = %v, want a classified authorization failure", err)
	}
	if strings.Contains(remoteErr.Error(), secret) {
		t.Fatalf("a token survived into %q", remoteErr.Error())
	}
}

func TestParseIssueReferenceAcceptsNumbersAndThisRepositorysURLs(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"292", "#292"} {
		repository, number, err := ParseIssueReference(input)
		if err != nil || number != 292 || !repository.Empty() {
			t.Fatalf("ParseIssueReference(%q) = %#v, %d, %v", input, repository, number, err)
		}
	}

	repository, number, err := ParseIssueReference("https://github.com/johnjallday/ori-agent/issues/292")
	if err != nil || number != 292 || repository.Slug() != "johnjallday/ori-agent" {
		t.Fatalf("ParseIssueReference(url) = %#v, %d, %v", repository, number, err)
	}

	for _, input := range []string{
		"", "  ", "0", "-4", "#0", "abc", "292abc",
		"https://github.com/johnjallday/ori-agent/pull/292",
		"https://github.com/johnjallday/ori-agent/issues",
		"https://github.com/johnjallday/ori-agent/issues/abc",
		"ftp://github.com/johnjallday/ori-agent/issues/292",
		"github.com/johnjallday/ori-agent/issues/292",
	} {
		if repository, number, err := ParseIssueReference(input); err == nil {
			t.Fatalf("ParseIssueReference(%q) = %#v, %d; want a rejection", input, repository, number)
		}
	}
}

// slicesContainPair reports whether flag is immediately followed by value.
func slicesContainPair(args []string, flag, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flag && args[index+1] == value {
			return true
		}
	}
	return false
}

func TestParseRepositoryAcceptsOnlyAnExactSlug(t *testing.T) {
	t.Parallel()
	repository, err := ParseRepository("johnjallday/ori-agent")
	if err != nil || repository.Owner != "johnjallday" || repository.Name != "ori-agent" {
		t.Fatalf("ParseRepository() = %#v, %v", repository, err)
	}
	if repository.Empty() {
		t.Fatal("a complete repository reported itself empty")
	}

	for _, input := range []string{
		"", "ori-agent", "/ori-agent", "johnjallday/", "a/b/c",
		"https://github.com/johnjallday/ori-agent",
		"johnjallday/ori agent", "johnjallday/ori;agent",
		"johnjallday/ori\x1b[31magent",
		"john$(id)day/ori-agent",
	} {
		if _, err := ParseRepository(input); err == nil {
			t.Fatalf("ParseRepository(%q) was accepted", input)
		}
	}
}
