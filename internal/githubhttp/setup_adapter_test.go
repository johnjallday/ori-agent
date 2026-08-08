package githubhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// --- fakes -------------------------------------------------------------------

type fakeWorkspaceStore struct {
	ws     *agentworkspace.Workspace
	saved  int
	getErr error
}

func (f *fakeWorkspaceStore) GetFolderWorkspace(string) (*agentworkspace.Workspace, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.ws, nil
}

func (f *fakeWorkspaceStore) Save(*agentworkspace.Workspace) error {
	f.saved++
	return nil
}

func newWorkspace() *agentworkspace.Workspace {
	return &agentworkspace.Workspace{ID: "ws-1", Name: "GitHub Ops"}
}

// githubAPI serves the endpoints the adapter touches: /user for the connection
// test, /user/repos for the picker, and /repos/{owner}/{name} for the per-repo
// reachability check.
type githubAPI struct {
	user  http.HandlerFunc
	repos http.HandlerFunc
	repo  http.HandlerFunc
	// writeProbe answers the POST .../issues write-capability probe. The
	// default is 422, meaning "authorized, body rejected, nothing created".
	writeProbe http.HandlerFunc
}

func newAdapter(t *testing.T, api githubAPI, ws *agentworkspace.Workspace) (*SetupAdapter, *fakeWorkspaceStore, *fakeCredentialStore) {
	t.Helper()
	store := withFakeStore(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		if api.repos != nil {
			api.repos(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if api.user != nil {
			api.user(w, r)
			return
		}
		okUser("octocat")(w, r)
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		// A POST to .../issues is the write-capability probe, not a repo read.
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues") {
			if api.writeProbe != nil {
				api.writeProbe(w, r)
				return
			}
			// Authorized; the empty body is what was rejected.
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		if api.repo != nil {
			api.repo(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"full_name":"octocat/demo"}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	conn := NewConnection(&http.Client{Transport: rewriteHostTransport{target: srv.URL, next: http.DefaultTransport}})

	wsStore := &fakeWorkspaceStore{ws: ws}
	return NewSetupAdapter(conn, wsStore), wsStore, store
}

func connectToken(t *testing.T, store *fakeCredentialStore) {
	t.Helper()
	authRef := mcp.NormalizedAuthRef(MCPServerConfig())
	store.byRef[authRef] = mcp.RemoteCredential{
		AuthRef:     authRef,
		AccessToken: testToken,
		TokenType:   mcp.StaticBearerTokenType,
	}
}

func step(kind string) setupwizard.StepRequest {
	return setupwizard.StepRequest{
		WorkspaceID: "ws-1",
		Step:        agentworkspace.SetupWizardStep{ID: kind, Kind: kind},
	}
}

func reposJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// --- tests -------------------------------------------------------------------

func TestSetupAdapter_ID(t *testing.T) {
	adapter, _, _ := newAdapter(t, githubAPI{}, newWorkspace())
	if adapter.ID() != SetupAdapterID || SetupAdapterID != "github_ops" {
		t.Fatalf("adapter ID = %q, want github_ops", adapter.ID())
	}
}

// No connection at all: not ready, and categorized as something the user has
// yet to do rather than as a fault.
func TestSetupAdapter_NoConnection(t *testing.T) {
	adapter, _, _ := newAdapter(t, githubAPI{}, newWorkspace())

	got, err := adapter.Evaluate(context.Background(), step(agentworkspace.SetupStepKindAccountLink))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if got.Ready {
		t.Fatal("must not be ready with no connection")
	}
	if got.ErrorCategory != setupwizard.ErrorCategoryNotConfigured {
		t.Fatalf("category = %q, want %q", got.ErrorCategory, setupwizard.ErrorCategoryNotConfigured)
	}
	if !strings.Contains(got.Summary, "Settings") {
		t.Fatalf("summary should route the user to Settings: %s", got.Summary)
	}
}

// A connection but no repo: the account step passes, the repo step is pending
// and offers the choices.
func TestSetupAdapter_ConnectionButNoRepo(t *testing.T) {
	ws := newWorkspace()
	adapter, _, store := newAdapter(t, githubAPI{
		repos: reposJSON(`[{"full_name":"octocat/demo","private":false,"open_issues_count":3},
		                   {"full_name":"octocat/other","private":true,"open_issues_count":0}]`),
	}, ws)
	connectToken(t, store)

	account, _ := adapter.Evaluate(context.Background(), step(agentworkspace.SetupStepKindAccountLink))
	if !account.Ready {
		t.Fatalf("the account step should pass with a working connection: %+v", account)
	}
	if !strings.Contains(account.Summary, "@octocat") {
		t.Fatalf("the account step should name the connected login: %s", account.Summary)
	}

	repo, _ := adapter.Evaluate(context.Background(), step(agentworkspace.SetupStepKindCapabilityConfigure))
	if repo.Ready {
		t.Fatal("the repo step must not be ready before a repo is chosen")
	}
	if repo.ErrorCategory != setupwizard.ErrorCategoryNotConfigured {
		t.Fatalf("category = %q, want %q", repo.ErrorCategory, setupwizard.ErrorCategoryNotConfigured)
	}
	if len(repo.Options) != 2 {
		t.Fatalf("expected 2 repository options, got %+v", repo.Options)
	}
	if repo.Options[0].ID != "octocat/demo" {
		t.Fatalf("option ID must be the owner/name reference, got %q", repo.Options[0].ID)
	}
	if !strings.Contains(repo.Options[0].Description, "3 open issues") {
		t.Fatalf("option description should show issue count: %s", repo.Options[0].Description)
	}
	if !strings.Contains(repo.Options[1].Description, "Private") {
		t.Fatalf("option description should show visibility: %s", repo.Options[1].Description)
	}
}

// Both present: every step ready, and the summary states the confirmation
// boundary rather than just declaring success.
func TestSetupAdapter_ConnectionAndRepoAreReady(t *testing.T) {
	ws := newWorkspace()
	if err := BindRepo(ws, "octocat/demo"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}
	adapter, _, store := newAdapter(t, githubAPI{}, ws)
	connectToken(t, store)

	for _, kind := range []string{
		agentworkspace.SetupStepKindAccountLink,
		agentworkspace.SetupStepKindCapabilityConfigure,
		agentworkspace.SetupStepKindReadiness,
		agentworkspace.SetupStepKindSummary,
	} {
		got, err := adapter.Evaluate(context.Background(), step(kind))
		if err != nil {
			t.Fatalf("%s: Evaluate error: %v", kind, err)
		}
		if !got.Ready {
			t.Fatalf("%s should be ready: %+v", kind, got)
		}
	}

	summary, _ := adapter.Evaluate(context.Background(), step(agentworkspace.SetupStepKindSummary))
	if !strings.Contains(summary.Summary, "octocat/demo") {
		t.Fatalf("the summary should name the bound repo: %s", summary.Summary)
	}
	if !strings.Contains(strings.ToLower(summary.Summary), "confirmation") {
		t.Fatalf("the summary should restate the confirmation boundary: %s", summary.Summary)
	}
}

// A revoked token must read as blocked, never as "not started yet" -- a broken
// workspace that looks merely unconfigured invites the user to redo setup they
// already did.
func TestSetupAdapter_RevokedTokenIsBlockedNotSilentlyReady(t *testing.T) {
	ws := newWorkspace()
	if err := BindRepo(ws, "octocat/demo"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}
	adapter, _, store := newAdapter(t, githubAPI{user: status(http.StatusUnauthorized)}, ws)
	connectToken(t, store)

	got, _ := adapter.Evaluate(context.Background(), step(agentworkspace.SetupStepKindReadiness))
	if got.Ready {
		t.Fatal("a revoked token must not report ready")
	}
	if !got.Blocked {
		t.Fatalf("a revoked token must report blocked, got %+v", got)
	}
	if got.ErrorCategory != setupwizard.ErrorCategoryPermissionRequired {
		t.Fatalf("category = %q, want %q", got.ErrorCategory, setupwizard.ErrorCategoryPermissionRequired)
	}
	if strings.Contains(got.Summary, testToken) {
		t.Fatalf("the token leaked into a wizard summary: %s", got.Summary)
	}
}

// The case that motivates checking the repo separately from the connection: a
// replacement token that works but can no longer see this workspace's repo.
func TestSetupAdapter_NarrowerTokenCannotReachBoundRepo(t *testing.T) {
	ws := newWorkspace()
	if err := BindRepo(ws, "octocat/demo"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}
	adapter, _, store := newAdapter(t, githubAPI{
		// The connection itself is fine...
		user: okUser("octocat"),
		// ...but this repo is invisible to the token now.
		repo: status(http.StatusNotFound),
	}, ws)
	connectToken(t, store)

	got, _ := adapter.Evaluate(context.Background(), step(agentworkspace.SetupStepKindReadiness))
	if got.Ready {
		t.Fatal("must not report ready when the bound repo is unreachable")
	}
	if !got.Blocked || got.ErrorCategory != setupwizard.ErrorCategoryPermissionRequired {
		t.Fatalf("expected a blocked permission_required, got %+v", got)
	}
	if !strings.Contains(got.Summary, "octocat/demo") {
		t.Fatalf("the summary should name the unreachable repo: %s", got.Summary)
	}
}

// Confirm persists the choice, and is safe to run twice.
func TestSetupAdapter_ConfirmBindsRepoIdempotently(t *testing.T) {
	ws := newWorkspace()
	adapter, wsStore, store := newAdapter(t, githubAPI{
		repos: reposJSON(`[{"full_name":"octocat/demo","private":false,"open_issues_count":3}]`),
	}, ws)
	connectToken(t, store)

	req := step(agentworkspace.SetupStepKindCapabilityConfigure)
	action := setupwizard.StepAction{Type: setupwizard.ActionConfirm, Option: "octocat/demo"}

	got, err := adapter.Confirm(context.Background(), req, action)
	if err != nil {
		t.Fatalf("Confirm error: %v", err)
	}
	if !got.Ready {
		t.Fatalf("the repo step should be ready after confirming: %+v", got)
	}
	bound, ok := BoundRepo(ws)
	if !ok || bound != "octocat/demo" {
		t.Fatalf("BoundRepo = %q (ok=%v), want octocat/demo", bound, ok)
	}

	// Twice is a no-op, not a second binding.
	if _, err := adapter.Confirm(context.Background(), req, action); err != nil {
		t.Fatalf("second Confirm error: %v", err)
	}
	bindings := ws.GetMCPBindings()
	githubBindings := 0
	for _, b := range bindings {
		if strings.EqualFold(b.ServerName, mcp.GitHubServerName) {
			githubBindings++
		}
	}
	if githubBindings != 1 {
		t.Fatalf("expected exactly one github binding after two confirms, got %d", githubBindings)
	}
	if wsStore.saved < 2 {
		t.Fatalf("expected the workspace to be saved on each confirm, got %d", wsStore.saved)
	}
}

// The picker can only offer repositories the token can READ, which is a much
// wider set than it can write to -- a fine-grained PAT reads every public repo
// on GitHub. Binding one the token cannot write to would produce a workspace
// that triages happily and then fails on the first approved change, so the
// choice is verified before it is recorded.
func TestSetupAdapter_RefusesRepoTheTokenCannotWriteTo(t *testing.T) {
	ws := newWorkspace()
	adapter, wsStore, store := newAdapter(t, githubAPI{
		repos: reposJSON(`[{"full_name":"someone/readonly","private":false,"open_issues_count":9}]`),
		// Readable...
		repo: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"full_name":"someone/readonly"}`))
		},
		// ...but not writable.
		writeProbe: status(http.StatusForbidden),
	}, ws)
	connectToken(t, store)

	_, err := adapter.Confirm(context.Background(),
		step(agentworkspace.SetupStepKindCapabilityConfigure),
		setupwizard.StepAction{Type: setupwizard.ActionConfirm, Option: "someone/readonly"})

	// It must be an ERROR, not a blocked readiness: the service discards
	// Confirm's readiness and re-evaluates, so a readiness-only refusal would
	// reach the user as a silently unchanged step.
	if err == nil {
		t.Fatal("expected Confirm to reject a repo the token cannot write to")
	}
	if !errors.Is(err, setupwizard.ErrStepRejected) {
		t.Fatalf("the refusal must be marked user-safe with ErrStepRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "someone/readonly") {
		t.Fatalf("the message should name the repo: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "issues") {
		t.Fatalf("the message should name the permission to fix: %v", err)
	}

	// Nothing was persisted, so the workspace is not left pointing at a
	// repository it cannot act on.
	if _, ok := BoundRepo(ws); ok {
		t.Fatal("an unwritable repository must not be bound")
	}
	if wsStore.saved != 0 {
		t.Fatalf("expected no workspace save, got %d", wsStore.saved)
	}
}

// The probe must not create anything: it sends a body GitHub is guaranteed to
// reject, and a 422 is the success signal.
func TestCheckWriteAccess_TreatsUnprocessableAsAuthorized(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	store := withFakeStore(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.WriteHeader(http.StatusUnprocessableEntity)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	conn := NewConnection(&http.Client{Transport: rewriteHostTransport{target: srv.URL, next: http.DefaultTransport}})
	connectToken(t, store)

	if err := conn.CheckWriteAccess(context.Background(), "octocat/demo"); err != nil {
		t.Fatalf("422 must be read as authorized: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/repos/octocat/demo/issues" {
		t.Fatalf("unexpected probe request: %s %s", gotMethod, gotPath)
	}
	// An empty object has no title, so GitHub can only reject it -- the
	// request could never have created an issue.
	if strings.TrimSpace(gotBody) != "{}" {
		t.Fatalf("the probe body must be an empty object, got %q", gotBody)
	}
}

func TestCheckWriteAccess_RequiresAConnection(t *testing.T) {
	withFakeStore(t)
	conn, _ := newFakeGitHub(t, okUser("octocat"))

	err := conn.CheckWriteAccess(context.Background(), "octocat/demo")
	var connErr *ConnectionError
	if !errors.As(err, &connErr) || connErr.Category != ErrorCategoryNotConnected {
		t.Fatalf("expected not_connected, got %v", err)
	}
}

// Confirming a malformed reference must not persist anything.
func TestSetupAdapter_ConfirmRejectsMalformedRepo(t *testing.T) {
	ws := newWorkspace()
	adapter, _, store := newAdapter(t, githubAPI{}, ws)
	connectToken(t, store)

	_, err := adapter.Confirm(context.Background(),
		step(agentworkspace.SetupStepKindCapabilityConfigure),
		setupwizard.StepAction{Type: setupwizard.ActionConfirm, Option: "not-a-repo"})
	if err == nil {
		t.Fatal("expected a malformed repository reference to be rejected")
	}
	if !errors.Is(err, setupwizard.ErrStepRejected) {
		t.Fatalf("expected ErrStepRejected, got %v", err)
	}
	// The message must describe the malformed reference, not whatever GitHub
	// would have said about a nonsense path.
	if !strings.Contains(err.Error(), "not a repository reference") {
		t.Fatalf("unexpected message: %v", err)
	}
	if _, ok := BoundRepo(ws); ok {
		t.Fatal("a malformed repository reference must not be persisted")
	}
}

// A workspace that already chose a repo stays ready even when listing fails --
// losing the ability to enumerate choices is not the same as having no choice.
func TestSetupAdapter_BoundRepoSurvivesAListingFailure(t *testing.T) {
	ws := newWorkspace()
	if err := BindRepo(ws, "octocat/demo"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}
	adapter, _, store := newAdapter(t, githubAPI{repos: status(http.StatusForbidden)}, ws)
	connectToken(t, store)

	got, _ := adapter.Evaluate(context.Background(), step(agentworkspace.SetupStepKindCapabilityConfigure))
	if !got.Ready {
		t.Fatalf("an already-bound workspace should stay ready: %+v", got)
	}
	if !strings.Contains(got.Summary, "octocat/demo") {
		t.Fatalf("summary should name the bound repo: %s", got.Summary)
	}
}

func TestSetupAdapter_UnwiredReportsUnavailable(t *testing.T) {
	adapter := NewSetupAdapter(nil, nil)
	got, err := adapter.Evaluate(context.Background(), step(agentworkspace.SetupStepKindReadiness))
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if !got.Blocked || got.ErrorCategory != setupwizard.ErrorCategoryUnavailable {
		t.Fatalf("expected a blocked adapter_unavailable, got %+v", got)
	}
}

// --- binding helpers ---------------------------------------------------------

func TestBoundRepo_RejectsMalformedStoredValues(t *testing.T) {
	cases := map[string]any{
		"empty":         "",
		"no slash":      "justaname",
		"too many":      "a/b/c",
		"blank owner":   "/name",
		"blank name":    "owner/",
		"not a string":  42,
		"whitespace":    "   ",
		"slash spacing": " owner / name ",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			ws := newWorkspace()
			if err := ws.UpsertMCPBinding(agentworkspace.MCPBinding{
				ID:         "github",
				ServerName: mcp.GitHubServerName,
				Enabled:    true,
				Scope:      map[string]any{repoScopeKey: value},
			}); err != nil {
				t.Fatalf("UpsertMCPBinding: %v", err)
			}
			got, ok := BoundRepo(ws)
			if name == "slash spacing" {
				// " owner / name " has two valid segments, so it is accepted
				// -- but it must come back canonicalized, not with its inner
				// spaces intact, or it would display wrong and fail to
				// compare equal to the same repo written normally.
				if !ok {
					t.Fatalf("expected spaced owner/name to be accepted, got ok=false")
				}
				if got != "owner/name" {
					t.Fatalf("BoundRepo = %q, want the canonical %q", got, "owner/name")
				}
				return
			}
			if ok {
				t.Fatalf("expected %v to be rejected, got %q", value, got)
			}
		})
	}
}

func TestSplitRepo(t *testing.T) {
	owner, name, ok := SplitRepo("  octocat/demo  ")
	if !ok || owner != "octocat" || name != "demo" {
		t.Fatalf("SplitRepo = (%q, %q, %v)", owner, name, ok)
	}
	for _, bad := range []string{"", "octocat", "a/b/c", "/demo", "octocat/", "   "} {
		if _, _, ok := SplitRepo(bad); ok {
			t.Fatalf("SplitRepo(%q) should be rejected", bad)
		}
	}
}
