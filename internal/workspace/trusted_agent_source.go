package workspace

import (
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
)

// TrustedWorkspaceAgentSource lets the agent roster read workspaces' own agent
// copies where they are (<workspace>/agents/<slug>/config.json), instead of
// copying them into the global agent store at startup.
//
// Only workspaces this machine trusts contribute: on the allowlist and not
// trashed. A workspace that arrived by zip or sync therefore shows no agents
// until it is imported, which is what adds it to the allowlist.
type TrustedWorkspaceAgentSource struct {
	workspaces Store
	allowlist  *Allowlist
}

// NewTrustedWorkspaceAgentSource reads copies from workspaces, gated by
// allowlist. A nil allowlist trusts nothing.
func NewTrustedWorkspaceAgentSource(workspaces Store, allowlist *Allowlist) *TrustedWorkspaceAgentSource {
	return &TrustedWorkspaceAgentSource{workspaces: workspaces, allowlist: allowlist}
}

// WorkspaceAgents lists every agent copy a trusted workspace references.
func (s *TrustedWorkspaceAgentSource) WorkspaceAgents() []store.WorkspaceAgentEntry {
	if s == nil || s.workspaces == nil || s.allowlist == nil {
		return nil
	}
	var entries []store.WorkspaceAgentEntry
	eachWorkspaceMeta(s.workspaces, func(ws *Workspace) {
		if ws.Status == StatusTrashed || ws.Status == StatusMissing || !s.allowlist.Contains(ws.ID) {
			return
		}
		for _, name := range referencedAgentNames(ws) {
			ag, ok, err := s.workspaces.GetWorkspaceAgent(ws.ID, name)
			if err != nil {
				logger.Warn("Workspace agent copy could not be read", logger.Fields{
					"workspace_id": ws.ID,
					"agent":        name,
					"error":        err.Error(),
				})
				continue
			}
			if !ok || ag == nil {
				continue
			}
			entries = append(entries, store.WorkspaceAgentEntry{
				WorkspaceID:   ws.ID,
				WorkspaceName: ws.Name,
				AgentName:     name,
				Agent:         ag,
			})
		}
	})
	return entries
}

var _ store.WorkspaceAgentSource = (*TrustedWorkspaceAgentSource)(nil)
