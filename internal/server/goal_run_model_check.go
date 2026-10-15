package server

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// goalRunModelCheck reports whether a workspace's goal runs have a model to
// run on: its entry agent's provider, or the system model's when the agent
// names none, must be registered (workspace-goal-suggestions FR24). It is
// deliberately lenient — it blocks only when no provider can be resolved or
// the named one is not registered — so it never refuses a setup that runs.
func (b *ServerBuilder) goalRunModelCheck(ws *workspace.Workspace) (bool, string) {
	provider := ""
	if agentName := strings.TrimSpace(ws.EntryAgentName()); agentName != "" && b.st != nil {
		if ag, _, ok := store.ResolveAgent(b.st, agentName); ok && ag != nil {
			provider = strings.TrimSpace(ag.Settings.Provider)
		}
	}
	if provider == "" {
		provider, _ = b.systemModel()
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return false, workspace.MissionNoModelReason
	}
	if b.llmFactory == nil {
		return true, ""
	}
	if _, err := b.llmFactory.GetProvider(provider); err != nil {
		return false, workspace.MissionNoModelReason
	}
	return true, ""
}
