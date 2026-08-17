package store

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
)

// AgentGetter is the read-only slice of Store that name resolution needs.
//
// Several packages depend on a narrow lookup interface of their own rather than
// the whole Store; accepting the narrow shape lets them all share one
// compatibility rule instead of each re-deriving it.
type AgentGetter interface {
	GetAgent(name string) (*agent.Agent, bool)
}

// ResolveAgent looks up an agent by a name that may have been persisted under an
// earlier release's identity, and reports the name it actually resolved to.
//
// Agent references are stored by display name in a lot of places — workspace
// rosters, entry_agent_name, task From/To, session records, workspace-local
// snapshots. Renaming the system assistant would strand every one of them, so
// rather than rewriting stored records (which would touch IDs, timestamps and
// history ordering the migration promises not to disturb), a stale reference is
// resolved forward here at read time (FR52/FR57).
//
// Only the protected system assistant gets this treatment. An ordinary agent
// name is looked up exactly and never redirected, so no user-authored reference
// is ever silently pointed at a different agent (FR56).
func ResolveAgent(s AgentGetter, name string) (*agent.Agent, string, bool) {
	if s == nil {
		return nil, "", false
	}

	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, "", false
	}

	if ag, ok := s.GetAgent(trimmed); ok && ag != nil {
		return ag, trimmed, true
	}

	if !systemassistant.IsKnownName(trimmed) {
		return nil, "", false
	}

	// Canonical first, then the retired names newest-first: an install part-way
	// through migration can still have the record under either.
	for _, candidate := range append(
		[]string{systemassistant.CanonicalName},
		systemassistant.LegacyNames...,
	) {
		if strings.EqualFold(candidate, trimmed) {
			continue
		}
		if ag, ok := s.GetAgent(candidate); ok && ag != nil {
			return ag, candidate, true
		}
	}

	return nil, "", false
}

// AgentExists reports whether a reference resolves to a runnable agent, applying
// the same system-assistant compatibility as ResolveAgent.
func AgentExists(s AgentGetter, name string) bool {
	_, _, ok := ResolveAgent(s, name)
	return ok
}
