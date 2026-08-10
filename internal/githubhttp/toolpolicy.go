package githubhttp

import (
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// GitHub's hosted MCP server exposes 44 tools on a single connection, and only
// a handful of them belong to an issue-triage workspace. The rest include
// push_files, delete_file, create_or_update_file, merge_pull_request,
// create_repository, and create_branch -- code-level operations this template
// explicitly does not do, all reachable through the same connection the moment
// a token is stored.
//
// A system prompt cannot prevent a model from calling delete_file. Only a
// server-side allowlist can, which is why this is code rather than copy.
//
// The list is an allowlist rather than a denylist on purpose: a denylist
// silently admits every tool GitHub adds in future, and the failure mode of
// getting that wrong is unbounded. An unknown tool is denied.

// ReadToolAllowlist is the set of GitHub MCP tools that only read.
var ReadToolAllowlist = []string{
	"list_issues",
	"issue_read",
	"search_issues",
	"list_issue_types",
	"list_issue_fields",
	"get_label",
	"list_repository_collaborators",
	"get_me",
}

// WriteToolAllowlist is the set of GitHub MCP tools that change something on
// GitHub. They are exposed because the template's whole purpose is to draft
// these changes -- but every one of them is classified `external`, which the
// autonomy policy denies outright, so an agent can never invoke one directly.
// They reach GitHub only through the confirm-gated proposal broker.
var WriteToolAllowlist = []string{
	"add_issue_comment",
	"issue_write",
	"sub_issue_write",
}

// IsAllowedTool reports whether a GitHub MCP tool may be exposed or executed.
// It is the single source of truth for the fail-closed cap: an unknown tool
// returns false.
func IsAllowedTool(tool string) bool {
	name := strings.ToLower(strings.TrimSpace(tool))
	return slices.Contains(ReadToolAllowlist, name) || slices.Contains(WriteToolAllowlist, name)
}

// IsWriteTool reports whether an allowed tool mutates GitHub.
func IsWriteTool(tool string) bool {
	return slices.Contains(WriteToolAllowlist, strings.ToLower(strings.TrimSpace(tool)))
}

// AllowedTools returns every permitted tool name, reads first. Used to seed a
// workspace binding's AllowedTools so the cap is visible in the workspace
// record as well as enforced globally.
func AllowedTools() []string {
	out := make([]string, 0, len(ReadToolAllowlist)+len(WriteToolAllowlist))
	out = append(out, ReadToolAllowlist...)
	out = append(out, WriteToolAllowlist...)
	return out
}

// ToolSideEffects classifies each allowed tool for the workspace autonomy
// policy (see workspace.AutonomyPolicy). Reads are `read`; everything that
// changes GitHub is `external`, which both existing policies -- watch and
// propose -- deny. That denial is what makes "no autonomous writes" a property
// of the system rather than a promise in a prompt.
func ToolSideEffects() map[string]workspace.SideEffect {
	effects := make(map[string]workspace.SideEffect, len(ReadToolAllowlist)+len(WriteToolAllowlist))
	for _, tool := range ReadToolAllowlist {
		effects[tool] = workspace.SideEffectRead
	}
	for _, tool := range WriteToolAllowlist {
		effects[tool] = workspace.SideEffectExternal
	}
	return effects
}
