package githubhttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/mcp"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// repoScopeKey is the key under MCPBinding.Scope holding this workspace's
// bound repository, as "owner/name".
const repoScopeKey = "repo"

// WorkspaceStore reads and writes the workspace record the repo binding lives
// on. Matches calendarhttp.FolderStore so server wiring passes the same thing.
type WorkspaceStore interface {
	GetFolderWorkspace(id string) (*agentworkspace.Workspace, error)
	Save(ws *agentworkspace.Workspace) error
}

// Where the repo binding lives, and why.
//
// It is stored in the workspace's GitHub MCPBinding.Scope rather than in a new
// requirement type or in the wizard's own recorded step choice.
//
// Not the wizard's SelectedOption: that is setup-progress state. Re-running or
// resetting setup would drop the binding, and — more importantly — the
// server-side repo scoping that keeps an agent on its own repository would
// then be reading its boundary out of a UI progress record. A security check
// should not depend on wizard bookkeeping.
//
// Not a new DirectoryRequirement-style type: that would mean a new manifest
// type, new persistence, and new plumbing to answer a question MCPBinding.Scope
// already exists to answer. Scope is a per-workspace, free-form qualifier on
// the exact binding being constrained, so the binding and its limit travel
// together — the enforcement point reads the constraint from the same record
// that names the server it constrains.

// HardenBinding stamps this template's tool policy onto a GitHub MCP binding:
// the allowlist from toolpolicy.go plus its side-effect classification.
//
// It exists because a binding created from a blueprint's `tools.mcp_servers`
// list carries no restriction at all (`AllowedTools == nil` means "every
// tool", the legacy default), which for this server would hand an agent the
// ability to rewrite the user's code.
//
// This is the second of two enforcement points, and they are deliberately
// different in kind. The global exposure hook (see
// ServerBuilder.mcpToolExposureAllowed) caps the endpoint no matter who binds
// it or under what name; this stamps the same cap onto the workspace record,
// so the restriction is visible where a user inspects their workspace rather
// than only implied by server wiring. Both read AllowedTools()/
// ToolSideEffects(), so there is one list, not two.
//
// Idempotent, so it is safe to apply at workspace creation and again whenever
// the binding is touched.
func HardenBinding(binding agentworkspace.MCPBinding) agentworkspace.MCPBinding {
	binding.AllowedTools = AllowedTools()

	// Default to the stricter classification: a tool that somehow reaches
	// this binding without an explicit override is treated as leaving the
	// workspace, never as a harmless read.
	binding.DefaultSideEffect = agentworkspace.SideEffectExternal
	binding.ToolOverrides = ToolSideEffects()

	return binding
}

// FindGitHubBinding returns the workspace's binding for GitHub's MCP server.
func FindGitHubBinding(ws *agentworkspace.Workspace) (agentworkspace.MCPBinding, bool) {
	if ws == nil {
		return agentworkspace.MCPBinding{}, false
	}
	for _, binding := range ws.GetMCPBindings() {
		if strings.EqualFold(strings.TrimSpace(binding.ServerName), mcp.GitHubServerName) {
			return binding, true
		}
	}
	return agentworkspace.MCPBinding{}, false
}

// BoundRepo returns the repository this workspace is bound to, or ok == false
// when none has been chosen. A malformed stored value reports ok == false
// rather than being passed along to a request path.
func BoundRepo(ws *agentworkspace.Workspace) (string, bool) {
	binding, found := FindGitHubBinding(ws)
	if !found || binding.Scope == nil {
		return "", false
	}
	raw, ok := binding.Scope[repoScopeKey].(string)
	if !ok {
		return "", false
	}
	owner, name, valid := SplitRepo(raw)
	if !valid {
		return "", false
	}
	// Return the canonical "owner/name" rather than the stored bytes: a value
	// like " owner / name " has two valid segments but would otherwise be
	// displayed with its inner spaces and would not compare equal to the same
	// repo written normally.
	return owner + "/" + name, true
}

// BindRepo records fullName as this workspace's repository, creating the
// GitHub binding if the workspace does not have one yet.
//
// It is idempotent: binding the same repo twice is a no-op beyond the
// timestamp, which is what the wizard's retry-safe Confirm contract needs.
func BindRepo(ws *agentworkspace.Workspace, fullName string) error {
	if ws == nil {
		return fmt.Errorf("github: no workspace to bind")
	}
	owner, name, ok := SplitRepo(fullName)
	if !ok {
		return fmt.Errorf("github: %q is not an owner/name repository reference", fullName)
	}
	// Store the canonical form so the persisted value and every later
	// comparison agree.
	normalized := owner + "/" + name

	binding, found := FindGitHubBinding(ws)
	if !found {
		binding = agentworkspace.MCPBinding{
			ID:         "github",
			ServerName: mcp.GitHubServerName,
			Enabled:    true,
		}
	}
	if binding.Scope == nil {
		binding.Scope = map[string]any{}
	}
	binding.Scope[repoScopeKey] = normalized

	// Re-apply the tool boundary on every bind, so a binding that predates
	// hardening (or was widened by hand) is corrected the moment the
	// workspace is configured.
	return ws.UpsertMCPBinding(HardenBinding(binding))
}
