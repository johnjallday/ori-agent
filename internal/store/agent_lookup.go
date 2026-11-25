package store

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
)

// AgentGetter is the read-only slice of Store that name resolution needs.
//
// Several packages depend on a narrow lookup interface of their own rather than
// the whole Store; accepting the narrow shape lets them all share one rule.
type AgentGetter interface {
	GetAgent(name string) (*agent.Agent, bool)
}

// ResolveAgent looks up an agent by the name a stored reference holds and
// reports the name it resolved to.
//
// It once also redirected the retired system assistant's earlier names to its
// current one. That persona is retired, so every name is now looked up exactly
// and nothing is ever redirected: a reference to "Ask Ori" or "Workspace
// Manager" resolves only to an agent the person actually has under that name.
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
	return nil, "", false
}

// AgentExists reports whether a reference resolves to a runnable agent.
func AgentExists(s AgentGetter, name string) bool {
	_, _, ok := ResolveAgent(s, name)
	return ok
}
