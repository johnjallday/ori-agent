package server

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// goalRunModelCheck checks stored agent/model configuration without building a
// runtime, materializing connectors or invoking a model. Local snapshots are
// valid agents; a missing/disabled agent cannot borrow the system provider.
func (b *ServerBuilder) goalRunModelCheck(ws *workspace.Workspace) (bool, string) {
	var ag *agent.Agent
	name := strings.TrimSpace(ws.EntryAgentName())
	if b.workspaceStore != nil {
		local, found, err := b.workspaceStore.GetWorkspaceAgent(ws.ID, name)
		if err != nil {
			return false, "Repair this workspace's agent configuration, then choose Run now"
		}
		if found {
			ag = local
		}
	}
	if ag == nil && b.st != nil {
		ag, _, _ = store.ResolveAgent(b.st, name)
	}
	if ag == nil || ag.Status == types.AgentStatusDisabled {
		return false, "Add or enable this workspace's entry agent, then choose Run now"
	}
	provider, model := strings.TrimSpace(ag.Settings.Provider), strings.TrimSpace(ag.Settings.Model)
	if provider == "" {
		provider, model = b.systemModel()
	}
	if b.llmFactory == nil {
		return false, workspace.MissionNoModelReason
	}
	if _, err := b.llmFactory.GetSystemModelProvider(strings.ToLower(strings.TrimSpace(provider)), model); err != nil {
		return false, workspace.MissionNoModelReason
	}
	return true, ""
}
