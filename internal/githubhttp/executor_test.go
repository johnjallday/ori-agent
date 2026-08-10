package githubhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

// apiCall is one request the executor made, captured so tests can assert on
// the exact verb, path, and body that reached GitHub.
type apiCall struct {
	method string
	path   string
	body   string
}

type recordingAPI struct {
	mu     sync.Mutex
	calls  []apiCall
	status map[string]int // path suffix -> status override
}

func (r *recordingAPI) record(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.calls = append(r.calls, apiCall{method: req.Method, path: req.URL.Path, body: strings.TrimSpace(string(body))})
	r.mu.Unlock()
}

func newExecutor(t *testing.T, api *recordingAPI) Executor {
	t.Helper()
	store := withFakeStore(t)
	authRef := mcp.NormalizedAuthRef(MCPServerConfig())
	store.byRef[authRef] = mcp.RemoteCredential{
		AuthRef: authRef, AccessToken: testToken, TokenType: mcp.StaticBearerTokenType,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		api.record(req)
		for suffix, code := range api.status {
			if strings.HasSuffix(req.URL.Path, suffix) {
				w.WriteHeader(code)
				return
			}
		}
		switch {
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/comments"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"html_url":"https://github.com/octocat/demo/issues/7#issuecomment-99"}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	conn := NewConnection(&http.Client{Transport: rewriteHostTransport{target: srv.URL, next: http.DefaultTransport}})
	return NewExecutor(conn)
}

func TestExecutor_PostsTheExactReviewedComment(t *testing.T) {
	api := &recordingAPI{}
	exec := newExecutor(t, api)

	const text = "Looks like a duplicate of #1.\n\nClosing in favour of that one."
	url, err := exec.Apply(context.Background(), Change{
		Kind: ProposalComment, Repo: "octocat/demo", Issue: 7, Body: text,
		Rationale: "internal note the user read",
	})
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if url == "" {
		t.Fatal("expected the resulting comment URL")
	}

	if len(api.calls) != 1 {
		t.Fatalf("expected 1 API call, got %d: %+v", len(api.calls), api.calls)
	}
	call := api.calls[0]
	if call.method != http.MethodPost || call.path != "/repos/octocat/demo/issues/7/comments" {
		t.Fatalf("unexpected request: %s %s", call.method, call.path)
	}
	if !strings.Contains(call.body, "duplicate of #1") {
		t.Fatalf("the posted body must be the reviewed text, got %s", call.body)
	}
	// The rationale is for the user, not for GitHub.
	if strings.Contains(call.body, "internal note") {
		t.Fatalf("the rationale must not be sent to GitHub: %s", call.body)
	}
}

// Removals run before additions, so swapping one label for another never
// leaves the issue briefly carrying neither.
func TestExecutor_RemovesLabelsBeforeAdding(t *testing.T) {
	api := &recordingAPI{}
	exec := newExecutor(t, api)

	if _, err := exec.Apply(context.Background(), Change{
		Kind: ProposalLabels, Repo: "octocat/demo", Issue: 2,
		AddLabels: []string{"bug"}, RemoveLabels: []string{"needs-triage"},
	}); err != nil {
		t.Fatalf("Apply error: %v", err)
	}

	if len(api.calls) != 2 {
		t.Fatalf("expected 2 API calls, got %+v", api.calls)
	}
	if api.calls[0].method != http.MethodDelete {
		t.Fatalf("the removal must come first, got %s %s", api.calls[0].method, api.calls[0].path)
	}
	if !strings.Contains(api.calls[0].path, "needs-triage") {
		t.Fatalf("unexpected removal path: %s", api.calls[0].path)
	}
	if api.calls[1].method != http.MethodPost || !strings.Contains(api.calls[1].body, "bug") {
		t.Fatalf("unexpected addition: %+v", api.calls[1])
	}
}

// Removing a label that is not there produces the state the user asked for, so
// GitHub's 404 is not a failure.
func TestExecutor_AlreadyAbsentLabelIsNotAFailure(t *testing.T) {
	api := &recordingAPI{status: map[string]int{"/labels/gone": http.StatusNotFound}}
	exec := newExecutor(t, api)

	if _, err := exec.Apply(context.Background(), Change{
		Kind: ProposalLabels, Repo: "octocat/demo", Issue: 2, RemoveLabels: []string{"gone"},
	}); err != nil {
		t.Fatalf("removing an absent label should succeed, got: %v", err)
	}
}

func TestExecutor_ClosesWithReason(t *testing.T) {
	api := &recordingAPI{}
	exec := newExecutor(t, api)

	if _, err := exec.Apply(context.Background(), Change{
		Kind: ProposalState, Repo: "octocat/demo", Issue: 6,
		State: "closed", StateReason: "duplicate",
	}); err != nil {
		t.Fatalf("Apply error: %v", err)
	}

	call := api.calls[0]
	if call.method != http.MethodPatch || call.path != "/repos/octocat/demo/issues/6" {
		t.Fatalf("unexpected request: %s %s", call.method, call.path)
	}
	if !strings.Contains(call.body, `"state":"closed"`) || !strings.Contains(call.body, `"state_reason":"duplicate"`) {
		t.Fatalf("unexpected body: %s", call.body)
	}
}

// A reopen carries no close reason.
func TestExecutor_ReopenOmitsStateReason(t *testing.T) {
	api := &recordingAPI{}
	exec := newExecutor(t, api)

	if _, err := exec.Apply(context.Background(), Change{
		Kind: ProposalState, Repo: "octocat/demo", Issue: 6, State: "open", StateReason: "duplicate",
	}); err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if strings.Contains(api.calls[0].body, "state_reason") {
		t.Fatalf("a reopen must not send a close reason: %s", api.calls[0].body)
	}
}

// Failures are reported in language a user who just clicked Approve can act
// on, never as a raw GitHub API error.
func TestExecutor_ReportsFailuresInPlainLanguage(t *testing.T) {
	cases := map[int]string{
		http.StatusForbidden:    "not permitted",
		http.StatusUnauthorized: "Reconnect GitHub",
		http.StatusGone:         "issues are disabled",
	}
	for code, want := range cases {
		t.Run(http.StatusText(code), func(t *testing.T) {
			api := &recordingAPI{status: map[string]int{"/comments": code}}
			exec := newExecutor(t, api)

			_, err := exec.Apply(context.Background(), Change{
				Kind: ProposalComment, Repo: "octocat/demo", Issue: 7, Body: "hello",
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %q, want it to mention %q", err, want)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatalf("the token leaked into an error: %v", err)
			}
		})
	}
}

func TestExecutor_RefusesAMalformedRepository(t *testing.T) {
	api := &recordingAPI{}
	exec := newExecutor(t, api)

	if _, err := exec.Apply(context.Background(), Change{
		Kind: ProposalComment, Repo: "not-a-repo", Issue: 1, Body: "hi",
	}); err == nil {
		t.Fatal("expected a malformed repository to be refused")
	}
	if len(api.calls) != 0 {
		t.Fatal("nothing may be sent for a malformed repository")
	}
}

func TestExecutor_RefusesWithoutAConnection(t *testing.T) {
	withFakeStore(t)
	api := &recordingAPI{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { api.record(r); w.WriteHeader(http.StatusCreated) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	conn := NewConnection(&http.Client{Transport: rewriteHostTransport{target: srv.URL, next: http.DefaultTransport}})
	exec := NewExecutor(conn)

	if _, err := exec.Apply(context.Background(), Change{
		Kind: ProposalComment, Repo: "octocat/demo", Issue: 1, Body: "hi",
	}); err == nil {
		t.Fatal("expected an error with no stored connection")
	}
	if len(api.calls) != 0 {
		t.Fatal("nothing may be sent without a connection")
	}
}
