package githubhttp

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/githubscope"
	"github.com/johnjallday/ori-agent/internal/mcp"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Every code-level tool GitHub's hosted MCP server exposes. None of these may
// ever be reachable from an issue-triage workspace, and this list is written
// out rather than derived so that adding one to the allowlist by accident
// fails loudly here.
var codeLevelTools = []string{
	"push_files",
	"delete_file",
	"create_or_update_file",
	"create_branch",
	"create_repository",
	"fork_repository",
	"merge_pull_request",
	"create_pull_request",
	"update_pull_request",
	"pull_request_review_write",
	"run_secret_scanning",
	"search_code",
}

func TestHardenBinding_AllowsOnlyIssueTools(t *testing.T) {
	got := HardenBinding(agentworkspace.MCPBinding{ServerName: mcp.GitHubServerName})

	if got.AllowedTools == nil {
		t.Fatal("AllowedTools must not be nil -- nil means every tool is allowed")
	}
	if len(got.AllowedTools) != len(AllowedTools()) {
		t.Fatalf("the binding allowlist must match the policy, got %d vs %d", len(got.AllowedTools), len(AllowedTools()))
	}

	for _, tool := range codeLevelTools {
		if got.ToolAllowed(tool) {
			t.Errorf("code-level tool %q must never be allowed in an issue-triage workspace", tool)
		}
	}
	for _, tool := range AllowedTools() {
		if !got.ToolAllowed(tool) {
			t.Errorf("issue tool %q should be allowed", tool)
		}
	}
}

// Anything not explicitly named must be denied, including a tool GitHub adds
// to the endpoint after this code was written. That is the whole reason this
// is an allowlist.
func TestHardenBinding_DeniesUnknownFutureTools(t *testing.T) {
	got := HardenBinding(agentworkspace.MCPBinding{ServerName: mcp.GitHubServerName})
	for _, tool := range []string{"some_future_tool", "delete_repository", ""} {
		if got.ToolAllowed(tool) {
			t.Errorf("unknown tool %q must be denied by default", tool)
		}
	}
}

func TestHardenBinding_ClassifiesSideEffects(t *testing.T) {
	got := HardenBinding(agentworkspace.MCPBinding{ServerName: mcp.GitHubServerName})

	// An unlisted tool must fall back to the stricter classification, never
	// to "read".
	if got.DefaultSideEffect != agentworkspace.SideEffectExternal {
		t.Fatalf("DefaultSideEffect = %q, want %q", got.DefaultSideEffect, agentworkspace.SideEffectExternal)
	}

	for _, tool := range ReadToolAllowlist {
		if got.ToolOverrides[tool] != agentworkspace.SideEffectRead {
			t.Errorf("%s classified %q, want read", tool, got.ToolOverrides[tool])
		}
	}
	// Issue mutations are `external`, not `write`: their effect leaves the
	// workspace and cannot be quietly undone. `external` is also what both
	// existing autonomy policies deny outright, which is what makes "no
	// autonomous writes" a property of the system rather than a promise.
	for _, tool := range WriteToolAllowlist {
		if got.ToolOverrides[tool] != agentworkspace.SideEffectExternal {
			t.Errorf("%s classified %q, want external", tool, got.ToolOverrides[tool])
		}
	}
}

func TestHardenBinding_IsIdempotent(t *testing.T) {
	once := HardenBinding(agentworkspace.MCPBinding{ServerName: mcp.GitHubServerName})
	twice := HardenBinding(once)

	if len(twice.AllowedTools) != len(once.AllowedTools) {
		t.Fatalf("re-hardening changed the allowlist size: %d -> %d", len(once.AllowedTools), len(twice.AllowedTools))
	}
	if len(twice.ToolOverrides) != len(once.ToolOverrides) {
		t.Fatalf("re-hardening changed the overrides: %v -> %v", once.ToolOverrides, twice.ToolOverrides)
	}
}

// A binding widened by hand (or predating hardening) must be corrected the
// next time the workspace is configured.
func TestBindRepo_ReHardensAWidenedBinding(t *testing.T) {
	ws := newWorkspace()
	if err := ws.UpsertMCPBinding(agentworkspace.MCPBinding{
		ID:         "github",
		ServerName: mcp.GitHubServerName,
		Enabled:    true,
		// nil AllowedTools == every tool, including push_files.
		AllowedTools: nil,
	}); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}

	if err := BindRepo(ws, "octocat/demo"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}

	binding, ok := FindGitHubBinding(ws)
	if !ok {
		t.Fatal("expected a github binding")
	}
	if binding.AllowsAllTools() {
		t.Fatal("BindRepo must re-apply the tool allowlist to a widened binding")
	}
	if binding.ToolAllowed("push_files") {
		t.Fatal("push_files must not survive re-hardening")
	}
}

// The binding created by BindRepo carries both halves: the repo scope and the
// tool boundary. They travel together on one record, which is the point of
// storing the scope on the binding.
func TestBindRepo_CarriesScopeAndToolBoundaryTogether(t *testing.T) {
	ws := newWorkspace()
	if err := BindRepo(ws, "octocat/demo"); err != nil {
		t.Fatalf("BindRepo: %v", err)
	}

	binding, ok := FindGitHubBinding(ws)
	if !ok {
		t.Fatal("expected a github binding")
	}
	if binding.Scope[repoScopeKey] != "octocat/demo" {
		t.Fatalf("scope = %v, want octocat/demo", binding.Scope[repoScopeKey])
	}
	if binding.AllowsAllTools() {
		t.Fatal("the binding must carry a tool allowlist")
	}
	repo, ok := BoundRepo(ws)
	if !ok || repo != "octocat/demo" {
		t.Fatalf("BoundRepo = %q (ok=%v)", repo, ok)
	}
}

// Every tool the binding allows must also be one the repo guard knows how to
// constrain. A tool that is allowed but unclassified would pass through the
// guard untouched and could reach any repository the token can see -- which
// for a fine-grained PAT is every public repo on GitHub.
func TestAllowedToolsAreAllRepoScoped(t *testing.T) {
	binding := HardenBinding(agentworkspace.MCPBinding{ServerName: mcp.GitHubServerName})
	for _, tool := range binding.AllowedTools {
		if !githubscope.Classified(tool) {
			t.Errorf("tool %q is allowed but githubscope cannot constrain it to a repository; "+
				"classify it in internal/githubscope or remove it from the allowlist", tool)
		}
	}
}

// The allowlist must name only tools the live server actually exposes -- a
// typo would silently deny a tool the template needs.
func TestAllowlistNamesAreLowercaseAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range AllowedTools() {
		if tool != strings.ToLower(strings.TrimSpace(tool)) {
			t.Errorf("tool name %q should be lower-case and trimmed", tool)
		}
		if seen[tool] {
			t.Errorf("tool %q listed twice", tool)
		}
		seen[tool] = true
	}
}
