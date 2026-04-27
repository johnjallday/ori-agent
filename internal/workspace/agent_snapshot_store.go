package workspace

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
)

// AgentSnapshotStore decorates a workspace Store so that every Save() also
// writes workspace-local snapshots of any referenced agents that exist in the
// global agent store. The snapshots make a workspace folder self-contained for
// export/import: the entry agent (and any other workspace agents) are restored
// from the folder when the global registry doesn't know them.
//
// On read paths the underlying Store's GetWorkspaceAgent is used unchanged, so
// the snapshot wins precedence at resolution time (see AgentRuntimeResolver).
type AgentSnapshotStore struct {
	Store
	agents store.Store
}

// NewAgentSnapshotStore wraps inner with snapshot-on-save behavior.
// If agents is nil the wrapper is a no-op pass-through.
func NewAgentSnapshotStore(inner Store, agents store.Store) *AgentSnapshotStore {
	return &AgentSnapshotStore{Store: inner, agents: agents}
}

// Save persists the workspace, then opportunistically snapshots referenced
// agents. Snapshot failures are logged but do not fail the Save.
func (s *AgentSnapshotStore) Save(ws *Workspace) error {
	if err := s.Store.Save(ws); err != nil {
		return err
	}
	s.snapshotReferencedAgents(ws)
	return nil
}

// SnapshotReferencedAgents writes a snapshot for every agent the workspace
// references, when a matching global agent definition exists. Exported so the
// startup migration can call it directly without re-saving the workspace.
func (s *AgentSnapshotStore) SnapshotReferencedAgents(ws *Workspace) {
	s.snapshotReferencedAgents(ws)
}

func (s *AgentSnapshotStore) snapshotReferencedAgents(ws *Workspace) {
	if s == nil || ws == nil || s.agents == nil {
		return
	}
	for _, name := range referencedAgentNames(ws) {
		globalAgent, ok := s.agents.GetAgent(name)
		if !ok || globalAgent == nil {
			continue
		}
		if err := s.Store.SaveWorkspaceAgent(ws.ID, name, globalAgent); err != nil {
			logger.Warn("Failed to snapshot workspace-local agent", logger.Fields{
				"workspace_id": ws.ID,
				"agent":        name,
				"error":        err.Error(),
			})
		}
	}
}

// referencedAgentNames returns the deduplicated set of agent names a workspace
// references via its entry agent, legacy Agents slice, or AgentInstances.
func referencedAgentNames(ws *Workspace) []string {
	if ws == nil {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	add := func(name string) {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	add(ws.EntryAgentName())
	for _, name := range ws.Agents {
		add(name)
	}
	for _, inst := range ws.AgentInstances {
		add(inst.Name)
	}
	return out
}

// SnapshotAllWorkspaces walks the workspace store once and snapshots referenced
// agents for every workspace. Intended as a one-shot startup migration so
// existing workspaces self-heal even when their entry agent has been removed
// from the global registry.
func SnapshotAllWorkspaces(workspaces Store, agents store.Store) {
	if workspaces == nil || agents == nil {
		return
	}
	snapshotter, ok := workspaces.(*AgentSnapshotStore)
	if !ok {
		snapshotter = NewAgentSnapshotStore(workspaces, agents)
	}
	ids, err := workspaces.List()
	if err != nil {
		logger.Warn("agent snapshot migration: list workspaces failed", logger.Fields{"error": err.Error()})
		return
	}
	migrated := 0
	for _, id := range ids {
		ws, err := workspaces.Get(id)
		if err != nil || ws == nil {
			continue
		}
		before := referencedAgentSnapshotCount(workspaces, ws)
		snapshotter.SnapshotReferencedAgents(ws)
		after := referencedAgentSnapshotCount(workspaces, ws)
		if after > before {
			migrated++
		}
	}
	if migrated > 0 {
		logger.Info("Workspace agent snapshots migrated", logger.Fields{"workspaces": migrated})
	}
}

func referencedAgentSnapshotCount(s Store, ws *Workspace) int {
	if s == nil || ws == nil {
		return 0
	}
	count := 0
	for _, name := range referencedAgentNames(ws) {
		if _, ok, err := s.GetWorkspaceAgent(ws.ID, name); err == nil && ok {
			count++
		}
	}
	return count
}

// RestoreWorkspaceAgents reads workspace-local agent snapshots for every agent
// the workspace references and registers them into the global agent store when
// no agent of that name exists there. Existing global agents are preserved
// (the local copy still wins at resolution time via the workspace store).
//
// Returns the names of agents that were newly registered into the global
// store, suitable for surfacing in import UX.
func RestoreWorkspaceAgents(workspaces Store, ws *Workspace, agents store.Store) ([]string, error) {
	if workspaces == nil || ws == nil || agents == nil {
		return nil, nil
	}
	registered := make([]string, 0)
	for _, name := range referencedAgentNames(ws) {
		if existing, ok := agents.GetAgent(name); ok && existing != nil {
			continue
		}
		snapshot, ok, err := workspaces.GetWorkspaceAgent(ws.ID, name)
		if err != nil {
			logger.Warn("RestoreWorkspaceAgents: read snapshot failed", logger.Fields{
				"workspace_id": ws.ID,
				"agent":        name,
				"error":        err.Error(),
			})
			continue
		}
		if !ok || snapshot == nil {
			continue
		}
		if err := agents.SetAgent(name, snapshot); err != nil {
			logger.Warn("RestoreWorkspaceAgents: register into global store failed", logger.Fields{
				"workspace_id": ws.ID,
				"agent":        name,
				"error":        err.Error(),
			})
			continue
		}
		registered = append(registered, name)
	}
	if len(registered) > 0 {
		if err := agents.Save(); err != nil {
			logger.Warn("RestoreWorkspaceAgents: persist global agent store failed", logger.Fields{
				"workspace_id": ws.ID,
				"error":        err.Error(),
			})
		}
	}
	return registered, nil
}

// Compile-time check.
var _ Store = (*AgentSnapshotStore)(nil)

// Ensure imported agent type is referenced (the *agent.Agent value is read
// from the global store inside snapshotReferencedAgents).
var _ = (*agent.Agent)(nil)
