// Package githubscope constrains GitHub MCP tool calls to a single
// repository.
//
// It is a leaf package on purpose: it imports only toolapi, so both the chat
// path (internal/chathttp) and the task path (internal/workspace) can apply it
// without an import cycle through the package that owns the GitHub connection.
//
// Why this exists at all: a GitHub Ops workspace is bound to exactly one
// repository, but the token behind it can reach others -- and, because a
// fine-grained personal access token can read every public repository on
// GitHub regardless of how it is scoped, token scoping cannot enforce the
// boundary even in principle. The agent's system prompt says to stay on the
// bound repo, but a prompt is guidance, not a boundary. This is the boundary.
package githubscope

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/toolapi"
)

// ownerParam and repoParam are the argument names GitHub's MCP tools use to
// address a repository.
const (
	ownerParam = "owner"
	repoParam  = "repo"
	queryParam = "query"
)

// How each allowed GitHub tool relates to a repository. Every tool the
// binding permits must appear in exactly one of these three sets -- a drift
// test in internal/githubhttp enforces that -- so a tool can never be exposed
// without someone having decided how it is constrained.
//
// The lists are explicit rather than inferred, so a tool nobody classified is
// refused rather than silently rewritten with arguments it does not accept.
var (
	// repoAddressingTools take owner and repo arguments.
	repoAddressingTools = map[string]bool{
		"list_issues":                   true,
		"issue_read":                    true,
		"issue_write":                   true,
		"sub_issue_write":               true,
		"add_issue_comment":             true,
		"list_issue_types":              true,
		"list_issue_fields":             true,
		"get_label":                     true,
		"list_repository_collaborators": true,
	}
	// queryScopedTools address a repository through a search query string,
	// so they need the repo qualifier injected into the query instead.
	queryScopedTools = map[string]bool{
		"search_issues": true,
	}
	// repoAgnosticTools do not name a repository at all, so there is
	// nothing to constrain. `get_me` reports who the token belongs to,
	// which is the same answer regardless of which repo is bound. Listing
	// them explicitly is what keeps "unclassified" meaning "nobody has
	// thought about this yet" rather than "harmless".
	repoAgnosticTools = map[string]bool{
		"get_me": true,
	}
)

// Classified reports whether toolName is one this package knows how to
// constrain to a repository.
//
// It exists so the package that owns the binding's tool allowlist can assert
// the two lists have not drifted: a tool that is allowed but unclassified
// would pass through the guard untouched and could reach any repository.
func Classified(toolName string) bool {
	name := strings.ToLower(strings.TrimSpace(toolName))
	return repoAddressingTools[name] || queryScopedTools[name] || repoAgnosticTools[name]
}

// Guard wraps GitHub MCP tools so every call is confined to one repository.
type Guard struct {
	owner string
	repo  string
}

// New builds a guard for "owner/name". It returns ok == false for anything
// that is not exactly two non-empty segments -- a malformed binding must never
// be turned into a permissive guard.
func New(fullName string) (*Guard, bool) {
	parts := strings.Split(strings.TrimSpace(fullName), "/")
	if len(parts) != 2 {
		return nil, false
	}
	owner := strings.TrimSpace(parts[0])
	repo := strings.TrimSpace(parts[1])
	if owner == "" || repo == "" {
		return nil, false
	}
	return &Guard{owner: owner, repo: repo}, true
}

// FullName returns the guarded "owner/name".
func (g *Guard) FullName() string { return g.owner + "/" + g.repo }

// Wrap returns tools that can only act on the guarded repository. Tools are
// returned in the same order; a nil tool is dropped.
func (g *Guard) Wrap(tools []toolapi.Tool) []toolapi.Tool {
	if g == nil {
		return tools
	}
	wrapped := make([]toolapi.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		wrapped = append(wrapped, &scopedTool{inner: tool, guard: g})
	}
	return wrapped
}

// scopedTool enforces the repository boundary on one tool.
type scopedTool struct {
	inner toolapi.Tool
	guard *Guard
}

// Definition passes the underlying definition through unchanged, so the model
// still sees the tool's real schema. The description gains a note about the
// binding: the model behaves better when the constraint is visible rather than
// only discovered by having a call rejected.
func (t *scopedTool) Definition() toolapi.ToolDefinition {
	def := t.inner.Definition()
	def.Description = strings.TrimSpace(def.Description) +
		"\n\nThis workspace is bound to the repository " + t.guard.FullName() +
		". Calls to any other repository are refused."
	return def
}

// Call rewrites the arguments so they name the bound repository, and refuses
// the call outright when they name a different one.
//
// Rewriting rather than only rejecting is deliberate: a missing owner/repo
// would otherwise reach GitHub unqualified, and the failure mode of a
// too-broad query is worse than that of a needlessly explicit one.
func (t *scopedTool) Call(ctx context.Context, args string) (string, error) {
	rewritten, err := t.guard.constrain(t.inner.Definition().Name, args)
	if err != nil {
		return "", err
	}
	return t.inner.Call(ctx, rewritten)
}

// constrain returns args with the repository forced to the guarded one.
func (g *Guard) constrain(toolName, args string) (string, error) {
	trimmed := strings.TrimSpace(args)
	parsed := map[string]any{}
	if trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			// Arguments Ori cannot parse cannot be verified either, and an
			// unverifiable call to a repository-addressing tool is exactly
			// what this guard exists to stop.
			return "", fmt.Errorf("this workspace could not verify which repository that call targets, so it was not made")
		}
	}

	switch {
	case queryScopedTools[toolName]:
		return g.constrainQuery(parsed)
	case repoAddressingTools[toolName]:
		return g.constrainOwnerRepo(parsed)
	case repoAgnosticTools[toolName]:
		// Nothing to constrain: these name no repository.
		return encode(parsed)
	default:
		// Refuse rather than pass through. An unclassified tool reaching a
		// guarded binding means the allowlist and this package have
		// drifted, and the safe reading of that is "nobody decided how to
		// constrain this", not "it must be harmless".
		return "", fmt.Errorf(
			"refused: %q is not a tool this workspace knows how to confine to %s",
			toolName, g.FullName())
	}
}

// constrainOwnerRepo enforces the owner/repo argument pair, filling in either
// one that is missing.
func (g *Guard) constrainOwnerRepo(parsed map[string]any) (string, error) {
	gotOwner, ownerPresent := stringArg(parsed, ownerParam)
	gotRepo, repoPresent := stringArg(parsed, repoParam)

	if ownerPresent && !strings.EqualFold(gotOwner, g.owner) ||
		repoPresent && !strings.EqualFold(gotRepo, g.repo) {
		return "", g.refusal(joinRepo(gotOwner, gotRepo))
	}

	// Set both unconditionally: an omitted argument would otherwise reach
	// GitHub unqualified, and a too-broad query is a worse failure than a
	// needlessly explicit one.
	parsed[ownerParam] = g.owner
	parsed[repoParam] = g.repo
	return encode(parsed)
}

// constrainQuery enforces the repo qualifier inside a search query.
func (g *Guard) constrainQuery(parsed map[string]any) (string, error) {
	query, _ := stringArg(parsed, queryParam)

	// Reject a query that pins a different repository rather than silently
	// adding a second, contradictory qualifier.
	for _, field := range strings.Fields(query) {
		value, ok := strings.CutPrefix(strings.ToLower(field), "repo:")
		if !ok {
			continue
		}
		if !strings.EqualFold(value, g.FullName()) {
			return "", g.refusal(value)
		}
	}

	if !strings.Contains(strings.ToLower(query), "repo:"+strings.ToLower(g.FullName())) {
		query = strings.TrimSpace("repo:" + g.FullName() + " " + query)
	}
	parsed[queryParam] = query
	return encode(parsed)
}

// refusal is the single message shape for a blocked call. It names both
// repositories so the model can correct itself, and reads as a rule rather
// than as an error the model should retry around.
func (g *Guard) refusal(attempted string) error {
	target := strings.TrimSpace(attempted)
	if target == "" {
		target = "another repository"
	}
	return fmt.Errorf(
		"refused: this workspace is bound to %s and cannot act on %s. "+
			"To work on a different repository, create a workspace bound to it",
		g.FullName(), target)
}

func stringArg(parsed map[string]any, key string) (string, bool) {
	raw, ok := parsed[key]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	if strings.TrimSpace(value) == "" {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func joinRepo(owner, repo string) string {
	switch {
	case owner != "" && repo != "":
		return owner + "/" + repo
	case repo != "":
		return repo
	default:
		return owner
	}
}

func encode(parsed map[string]any) (string, error) {
	out, err := json.Marshal(parsed)
	if err != nil {
		return "", fmt.Errorf("this workspace could not prepare that call safely, so it was not made")
	}
	return string(out), nil
}
