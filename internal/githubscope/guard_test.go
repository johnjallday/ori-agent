package githubscope

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/toolapi"
)

// recordingTool captures the arguments it was actually called with, which is
// how the rewrite assertions below observe what reached GitHub.
type recordingTool struct {
	name   string
	called bool
	args   string
}

func (r *recordingTool) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{Name: r.name, Description: "does a thing"}
}

func (r *recordingTool) Call(_ context.Context, args string) (string, error) {
	r.called = true
	r.args = args
	return "ok", nil
}

func wrapOne(t *testing.T, repo, toolName string) (*recordingTool, toolapi.Tool) {
	t.Helper()
	guard, ok := New(repo)
	if !ok {
		t.Fatalf("New(%q) failed", repo)
	}
	inner := &recordingTool{name: toolName}
	wrapped := guard.Wrap([]toolapi.Tool{inner})
	if len(wrapped) != 1 {
		t.Fatalf("expected 1 wrapped tool, got %d", len(wrapped))
	}
	return inner, wrapped[0]
}

func argsOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("inner tool received non-JSON args %q: %v", raw, err)
	}
	return parsed
}

func TestNew_RejectsMalformedReferences(t *testing.T) {
	for _, bad := range []string{"", "   ", "octocat", "a/b/c", "/demo", "octocat/"} {
		if _, ok := New(bad); ok {
			t.Errorf("New(%q) should be rejected -- a malformed binding must not become a permissive guard", bad)
		}
	}
	g, ok := New("  octocat / demo  ")
	if !ok || g.FullName() != "octocat/demo" {
		t.Fatalf("New should canonicalize, got %q (ok=%v)", g.FullName(), ok)
	}
}

// The core promise: a call naming another repository never reaches GitHub.
func TestCall_RefusesAnotherRepository(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "list_issues")

	_, err := wrapped.Call(context.Background(), `{"owner":"someone","repo":"private-thing"}`)
	if err == nil {
		t.Fatal("expected the call to be refused")
	}
	if inner.called {
		t.Fatal("the underlying tool must not be invoked for another repository")
	}
	if !strings.Contains(err.Error(), "octocat/demo") || !strings.Contains(err.Error(), "private-thing") {
		t.Fatalf("the refusal should name both repositories: %v", err)
	}
}

// Same owner, different repo is still a different repository.
func TestCall_RefusesSiblingRepositoryOfSameOwner(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "issue_read")

	if _, err := wrapped.Call(context.Background(), `{"owner":"octocat","repo":"other"}`); err == nil {
		t.Fatal("expected a sibling repo to be refused")
	}
	if inner.called {
		t.Fatal("the underlying tool must not be invoked")
	}
}

func TestCall_AllowsTheBoundRepository(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "list_issues")

	out, err := wrapped.Call(context.Background(), `{"owner":"octocat","repo":"demo","state":"open"}`)
	if err != nil {
		t.Fatalf("the bound repo should be allowed: %v", err)
	}
	if out != "ok" || !inner.called {
		t.Fatal("expected the underlying tool to run")
	}
	got := argsOf(t, inner.args)
	if got["state"] != "open" {
		t.Fatalf("unrelated arguments must be preserved: %v", got)
	}
}

// Case differences are not a boundary: GitHub treats owner/repo
// case-insensitively, and refusing here would block legitimate calls.
func TestCall_AcceptsDifferentCasing(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "list_issues")

	if _, err := wrapped.Call(context.Background(), `{"owner":"OctoCat","repo":"DEMO"}`); err != nil {
		t.Fatalf("case-insensitive match should be allowed: %v", err)
	}
	got := argsOf(t, inner.args)
	// ...but the canonical spelling is what gets sent.
	if got["owner"] != "octocat" || got["repo"] != "demo" {
		t.Fatalf("arguments should be normalized to the bound spelling: %v", got)
	}
}

// Omitted arguments are filled in rather than passed through: an unqualified
// call is a broader query than a needlessly explicit one.
func TestCall_InjectsMissingOwnerAndRepo(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "list_issues")

	if _, err := wrapped.Call(context.Background(), `{"state":"open"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := argsOf(t, inner.args)
	if got["owner"] != "octocat" || got["repo"] != "demo" {
		t.Fatalf("expected owner/repo to be injected, got %v", got)
	}
	if got["state"] != "open" {
		t.Fatalf("existing arguments must survive injection: %v", got)
	}
}

func TestCall_HandlesEmptyArguments(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "list_issues")

	for _, args := range []string{"", "   ", "{}", "null"} {
		inner.called = false
		if _, err := wrapped.Call(context.Background(), args); err != nil {
			t.Fatalf("args %q: unexpected error: %v", args, err)
		}
		got := argsOf(t, inner.args)
		if got["owner"] != "octocat" || got["repo"] != "demo" {
			t.Fatalf("args %q: expected the repo to be injected, got %v", args, got)
		}
	}
}

// Unparseable arguments cannot be verified, and an unverifiable call to a
// repository-addressing tool is exactly what this guard exists to stop.
func TestCall_RefusesUnparseableArguments(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "list_issues")

	if _, err := wrapped.Call(context.Background(), `{"owner": broken`); err == nil {
		t.Fatal("expected unparseable arguments to be refused")
	}
	if inner.called {
		t.Fatal("the underlying tool must not run on unverifiable arguments")
	}
}

// A tool that names no repository has nothing to constrain, so it passes
// through untouched rather than being rewritten with arguments it does not
// accept.
func TestCall_RepoAgnosticToolPassesThrough(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "get_me")

	if _, err := wrapped.Call(context.Background(), `{}`); err != nil {
		t.Fatalf("a repo-agnostic tool should be allowed: %v", err)
	}
	if !inner.called {
		t.Fatal("expected the underlying tool to run")
	}
	got := argsOf(t, inner.args)
	if _, present := got["owner"]; present {
		t.Fatalf("owner must not be injected into a repo-agnostic tool: %v", got)
	}
	if _, present := got["repo"]; present {
		t.Fatalf("repo must not be injected into a repo-agnostic tool: %v", got)
	}
}

// An unclassified tool reaching a guarded binding means the allowlist and this
// package have drifted. The safe reading is "nobody decided how to constrain
// this", not "it must be harmless".
func TestCall_RefusesUnclassifiedTool(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "delete_file")

	_, err := wrapped.Call(context.Background(), `{"path":"main.go"}`)
	if err == nil {
		t.Fatal("expected an unclassified tool to be refused")
	}
	if inner.called {
		t.Fatal("an unclassified tool must not run")
	}
	if !strings.Contains(err.Error(), "delete_file") {
		t.Fatalf("the refusal should name the tool: %v", err)
	}
}

func TestClassified(t *testing.T) {
	for _, tool := range []string{"list_issues", "search_issues", "get_me", "list_repository_collaborators"} {
		if !Classified(tool) {
			t.Errorf("%q should be classified", tool)
		}
	}
	for _, tool := range []string{"delete_file", "push_files", "some_future_tool", ""} {
		if Classified(tool) {
			t.Errorf("%q must not be classified", tool)
		}
	}
}

// --- query-scoped tools ------------------------------------------------------

func TestSearch_InjectsRepoQualifier(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "search_issues")

	if _, err := wrapped.Call(context.Background(), `{"query":"is:open crash"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := argsOf(t, inner.args)
	query, _ := got["query"].(string)
	if !strings.Contains(query, "repo:octocat/demo") {
		t.Fatalf("expected the repo qualifier to be injected, got %q", query)
	}
	if !strings.Contains(query, "is:open crash") {
		t.Fatalf("the original query must be preserved, got %q", query)
	}
}

func TestSearch_RefusesQueryPinningAnotherRepository(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "search_issues")

	_, err := wrapped.Call(context.Background(), `{"query":"repo:someone/private is:open"}`)
	if err == nil {
		t.Fatal("expected a query naming another repo to be refused")
	}
	if inner.called {
		t.Fatal("the underlying tool must not run")
	}
	if !strings.Contains(err.Error(), "someone/private") {
		t.Fatalf("the refusal should name the attempted repo: %v", err)
	}
}

func TestSearch_DoesNotDoubleQualify(t *testing.T) {
	inner, wrapped := wrapOne(t, "octocat/demo", "search_issues")

	if _, err := wrapped.Call(context.Background(), `{"query":"repo:octocat/demo is:open"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	query, _ := argsOf(t, inner.args)["query"].(string)
	if strings.Count(strings.ToLower(query), "repo:") != 1 {
		t.Fatalf("expected exactly one repo qualifier, got %q", query)
	}
}

// --- definition --------------------------------------------------------------

// The constraint is stated in the tool description so the model can see it,
// rather than only discovering it by having a call refused.
func TestDefinition_DisclosesTheBinding(t *testing.T) {
	_, wrapped := wrapOne(t, "octocat/demo", "list_issues")
	def := wrapped.Definition()

	if def.Name != "list_issues" {
		t.Fatalf("the tool name must pass through unchanged, got %q", def.Name)
	}
	if !strings.Contains(def.Description, "octocat/demo") {
		t.Fatalf("the description should name the bound repo: %s", def.Description)
	}
	if !strings.Contains(def.Description, "does a thing") {
		t.Fatalf("the original description must be preserved: %s", def.Description)
	}
}

func TestWrap_DropsNilTools(t *testing.T) {
	guard, _ := New("octocat/demo")
	got := guard.Wrap([]toolapi.Tool{nil, &recordingTool{name: "list_issues"}, nil})
	if len(got) != 1 {
		t.Fatalf("expected nil tools to be dropped, got %d", len(got))
	}
}

// A nil guard must pass tools through untouched rather than panic -- callers
// build one only when a repo is bound.
func TestWrap_NilGuardIsAPassThrough(t *testing.T) {
	var guard *Guard
	inner := &recordingTool{name: "list_issues"}
	got := guard.Wrap([]toolapi.Tool{inner})
	if len(got) != 1 || got[0] != toolapi.Tool(inner) {
		t.Fatal("a nil guard should return the tools unchanged")
	}
}
