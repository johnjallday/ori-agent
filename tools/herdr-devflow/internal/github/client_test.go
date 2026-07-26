package github

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func clientWith(t *testing.T, payload string) *Client {
	t.Helper()
	return New(Options{Run: func(context.Context, ...string) ([]byte, error) {
		return []byte(payload), nil
	}})
}

func TestListPullRequestsDecodesExactFacts(t *testing.T) {
	client := clientWith(t, `[{
		"number": 258,
		"url": "https://github.com/o/r/pull/258",
		"headRefName": "feature/herdr-devflow-bridge",
		"baseRefName": "dev",
		"isDraft": false,
		"state": "MERGED",
		"updatedAt": "2026-07-24T10:00:00Z",
		"mergedAt": "2026-07-24T10:05:00Z",
		"statusCheckRollup": [
			{"__typename":"CheckRun","name":"Unit Tests","status":"COMPLETED","conclusion":"SUCCESS"}
		]
	}]`)

	result, err := client.ListPullRequests(context.Background(), "dev")
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(result.PullRequests) != 1 {
		t.Fatalf("decoded %d pull requests, want 1", len(result.PullRequests))
	}
	pull := result.PullRequests[0]
	if pull.Number != 258 || pull.Head != "feature/herdr-devflow-bridge" || pull.Base != "dev" {
		t.Fatalf("pull = %+v", pull)
	}
	if pull.State != "merged" || !pull.Merged {
		t.Fatalf("state = %q merged=%v, want a normalized merged state", pull.State, pull.Merged)
	}
	if pull.Checks != ChecksPassing {
		t.Fatalf("checks = %q, want passing", pull.Checks)
	}
	if pull.MergedAt.IsZero() || pull.UpdatedAt.IsZero() {
		t.Fatalf("timestamps were dropped: %+v", pull)
	}
}

func TestListPullRequestsAggregatesChecks(t *testing.T) {
	cases := []struct {
		name   string
		rollup string
		want   CheckState
	}{
		{"no checks at all", `[]`, ChecksNone},
		{"all passing", `[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"SUCCESS"}]`, ChecksPassing},
		{"one failing dominates", `[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]`, ChecksFailing},
		{"still running", `[{"status":"IN_PROGRESS","conclusion":""}]`, ChecksPending},
		{"queued", `[{"status":"QUEUED","conclusion":""}]`, ChecksPending},
		{"timed out counts as failing", `[{"status":"COMPLETED","conclusion":"TIMED_OUT"}]`, ChecksFailing},
		{"cancelled counts as failing", `[{"status":"COMPLETED","conclusion":"CANCELLED"}]`, ChecksFailing},
		{"skipped neither passes nor blocks", `[{"status":"COMPLETED","conclusion":"SKIPPED"}]`, ChecksNone},
		{"status context failure", `[{"__typename":"StatusContext","state":"FAILURE"}]`, ChecksFailing},
		{"status context pending", `[{"__typename":"StatusContext","state":"PENDING"}]`, ChecksPending},
		{"failure wins over pending", `[{"status":"IN_PROGRESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]`, ChecksFailing},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := clientWith(t, `[{"number":1,"headRefName":"feature/x","baseRefName":"dev","state":"OPEN","statusCheckRollup":`+testCase.rollup+`}]`)
			result, err := client.ListPullRequests(context.Background(), "dev")
			if err != nil {
				t.Fatalf("ListPullRequests: %v", err)
			}
			if got := result.PullRequests[0].Checks; got != testCase.want {
				t.Fatalf("checks = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestListPullRequestsUsesAFixedArgumentVector(t *testing.T) {
	var seen []string
	client := New(Options{
		CandidateLimit: 25,
		Run: func(_ context.Context, args ...string) ([]byte, error) {
			seen = args
			return []byte("[]"), nil
		},
	})
	if _, err := client.ListPullRequests(context.Background(), "dev"); err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}

	joined := strings.Join(seen, " ")
	for _, want := range []string{"pr list", "--state all", "--base dev", "--limit 25", "--json"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args = %v, want them to contain %q", seen, want)
		}
	}
	for _, arg := range seen {
		if strings.ContainsAny(arg, ";|&`\n") {
			t.Fatalf("argument %q could be interpreted by a shell", arg)
		}
	}
}

func TestListPullRequestsRequiresABase(t *testing.T) {
	if _, err := clientWith(t, "[]").ListPullRequests(context.Background(), "  "); err == nil {
		t.Fatal("an empty base was accepted")
	}
}

func TestListPullRequestsReportsTruncation(t *testing.T) {
	client := New(Options{
		CandidateLimit: 2,
		Run: func(context.Context, ...string) ([]byte, error) {
			return []byte(`[{"number":1,"state":"OPEN"},{"number":2,"state":"OPEN"}]`), nil
		},
	})
	result, err := client.ListPullRequests(context.Background(), "dev")
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if !result.Truncated {
		t.Fatal("a full page was not reported as possibly truncated")
	}
}

func TestListPullRequestsRejectsMalformedJSON(t *testing.T) {
	client := clientWith(t, `{"not":"an array"}`)
	_, err := client.ListPullRequests(context.Background(), "dev")

	var remoteErr *Error
	if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorMalformed {
		t.Fatalf("err = %v, want a malformed-response error", err)
	}
}

// exitError builds an *exec.ExitError carrying stderr, which is how `gh`
// reports authentication failures.
func exitError(t *testing.T, stderr string) error {
	t.Helper()
	command := exec.Command("sh", "-c", "exit 1")
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("could not construct an ExitError: %v", err)
	}
	exitErr.Stderr = []byte(stderr)
	return exitErr
}

func TestListPullRequestsClassifiesFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"missing binary", exec.ErrNotFound, ErrorMissing},
		{"unauthenticated", nil, ErrorUnauthenticated},
		{"generic failure", nil, ErrorNetwork},
	}
	stderrs := []string{"", "gh: To get started with GitHub CLI, please run: gh auth login", "connection reset"}

	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			failure := testCase.err
			if failure == nil {
				failure = exitError(t, stderrs[index])
			}
			client := New(Options{Run: func(context.Context, ...string) ([]byte, error) {
				return nil, failure
			}})

			_, err := client.ListPullRequests(context.Background(), "dev")
			var remoteErr *Error
			if !errors.As(err, &remoteErr) {
				t.Fatalf("err = %v, want a classified error", err)
			}
			if remoteErr.Kind != testCase.want {
				t.Fatalf("kind = %q, want %q", remoteErr.Kind, testCase.want)
			}
			if remoteErr.Recovery() == "" {
				t.Fatal("a classified failure carried no recovery command")
			}
		})
	}
}

func TestListPullRequestsClassifiesTimeout(t *testing.T) {
	client := New(Options{
		Timeout: 10 * time.Millisecond,
		Run: func(ctx context.Context, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	_, err := client.ListPullRequests(context.Background(), "dev")

	var remoteErr *Error
	if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorTimeout {
		t.Fatalf("err = %v, want a timeout error", err)
	}
}

func TestErrorsNeverCarryRawOutput(t *testing.T) {
	// `gh` echoes request bodies and headers on failure. A token reaching a
	// terminal, a JSON payload, or a Herdr board cell would be a real leak.
	secret := "ghp_SUPERSECRETTOKENVALUE0000000000000000"
	client := New(Options{Run: func(context.Context, ...string) ([]byte, error) {
		return nil, exitError(t, "HTTP 401: Bad credentials (Authorization: token "+secret+")")
	}})

	_, err := client.ListPullRequests(context.Background(), "dev")
	if err == nil {
		t.Fatal("expected an error")
	}
	var remoteErr *Error
	if !errors.As(err, &remoteErr) {
		t.Fatalf("err = %v, want a classified error", err)
	}
	for _, rendered := range []string{remoteErr.Error(), remoteErr.Detail, remoteErr.Recovery()} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("a token survived into %q", rendered)
		}
		if strings.Contains(rendered, "Authorization") {
			t.Fatalf("a request header survived into %q", rendered)
		}
	}
	if remoteErr.Kind != ErrorUnauthenticated {
		t.Fatalf("kind = %q, want the failure still classified", remoteErr.Kind)
	}
}

func TestNormalizeSanitizesRemoteValues(t *testing.T) {
	// Branch names and URLs arrive from the network and are printed into a
	// terminal, so escape sequences must not survive decoding.
	payload := "[{\"number\":1,\"headRefName\":\"feature/x\\u001b[31m\"," +
		"\"baseRefName\":\"dev\",\"state\":\"OPEN\",\"url\":\"https://e\\u0000x\"}]"

	result, err := clientWith(t, payload).ListPullRequests(context.Background(), "dev")
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	pull := result.PullRequests[0]
	if strings.ContainsRune(pull.Head, 0x1b) || strings.ContainsRune(pull.Head, 0) {
		t.Fatalf("control characters survived in head: %q", pull.Head)
	}
	if strings.ContainsRune(pull.URL, 0x1b) || strings.ContainsRune(pull.URL, 0) {
		t.Fatalf("control characters survived in URL: %q", pull.URL)
	}
}

func TestDefaultsAreBounded(t *testing.T) {
	client := New(Options{})
	if client.timeout != DefaultTimeout {
		t.Fatalf("timeout = %v, want the bounded default", client.timeout)
	}
	if client.candidateLimit != DefaultCandidateLimit {
		t.Fatalf("candidate limit = %d, want the bounded default", client.candidateLimit)
	}

	explicit := New(Options{Timeout: -1, CandidateLimit: -5})
	if explicit.timeout <= 0 || explicit.candidateLimit <= 0 {
		t.Fatalf("negative options were not corrected: %+v", explicit)
	}
}
