package workspace

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
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

// GetFolderWorkspace forwards to the wrapped store when it supports reading
// the canonical workspace.json directly (folderWorkspaceResolver, declared
// in http_handlers.go). AgentSnapshotStore embeds the Store *interface*, not
// the wrapped store's concrete type, so Go's method promotion does not pick
// up GetFolderWorkspace automatically even though every concrete store
// actually wired in production (SyncStore, FileStore) implements it --
// without this forwarding method, any caller needing folder-store-only
// fields (e.g. TemplateProvenance, which has no SQLite column) silently
// loses that capability the moment this decorator wraps the chain.
func (s *AgentSnapshotStore) GetFolderWorkspace(id string) (*Workspace, error) {
	if fw, ok := s.Store.(folderWorkspaceResolver); ok {
		return fw.GetFolderWorkspace(id)
	}
	return nil, fmt.Errorf("wrapped store does not support GetFolderWorkspace")
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

// Update delegates mutation to the wrapped store so a SyncStore can hydrate
// folder-only canonical fields before fn runs. Reimplementing Get → fn → Save at
// this wrapper would start from SQLite's lean projection and make partial
// mutations (for example revoking one runtime grant) silently no-op or erase the
// selected mode. Snapshot the resulting agent references after the inner update
// to retain this decorator's write hook.
func (s *AgentSnapshotStore) Update(wsID string, fn func(*Workspace) error) error {
	var updated *Workspace
	if err := s.Store.Update(wsID, func(ws *Workspace) error {
		if err := fn(ws); err != nil {
			return err
		}
		updated = ws
		return nil
	}); err != nil {
		return err
	}
	s.snapshotReferencedAgents(updated)
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
		if err := s.SaveWorkspaceAgent(ws.ID, name, globalAgent); err != nil {
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
	for _, inst := range ws.AgentInstances {
		add(inst.Name)
	}
	return out
}

// metadataLister is an optional Store capability that returns metadata-only
// snapshots of every workspace (no chat history / tasks). FileStore satisfies it
// via its lean cache. The wrapping stores (SyncStore, AgentSnapshotStore) do NOT,
// so their List()+Get() fallback still covers DB-only workspaces.
type metadataLister interface {
	CachedWorkspaces() map[string]*Workspace
}

// schedulingLister is an optional Store capability returning active workspaces for
// the task scheduler with chat history omitted (the scheduler never reads it). The
// SQLite-backed adapter implements it with a lighter query; the wrapping stores
// forward to it so the scheduler avoids deserializing chat history every tick.
type schedulingLister interface {
	ListActiveForScheduling() ([]*Workspace, error)
}

// GetFolderPath forwards to the wrapped store's folder resolution (FileStore,
// SyncStore) when available. Without this, task_markdown_sync.go/
// backlog_markdown_sync.go's workspaceFolderForTaskMarkdown type-asserts for
// GetFolderPath and silently no-ops (ok=false, err=nil) against a plain
// *AgentSnapshotStore: Go only promotes methods declared on the embedded
// Store INTERFACE, and GetFolderPath is not part of that interface — the
// concrete FileStore/SyncStore wrapped inside never gets promoted on its own.
// That silent no-op previously meant tasks.md/BACKLOG.md never actually
// synchronized through any handler holding an *AgentSnapshotStore, which is
// the store production wiring hands to orchestrationhttp (see
// builder_workflow.go's NewAgentSnapshotStore wrapping).
func (s *AgentSnapshotStore) GetFolderPath(workspaceID string) (string, error) {
	if withFolder, ok := s.Store.(interface {
		GetFolderPath(string) (string, error)
	}); ok {
		return withFolder.GetFolderPath(workspaceID)
	}
	return "", fmt.Errorf("workspace folder storage is unavailable")
}

// FileStore forwards to the wrapped store's FileStore accessor (SyncStore),
// or nil when the wrapped store has no disk-backed FileStore. See
// GetFolderPath above for why this passthrough is required.
func (s *AgentSnapshotStore) FileStore() *FileStore {
	if withFileSync, ok := s.Store.(interface{ FileStore() *FileStore }); ok {
		return withFileSync.FileStore()
	}
	return nil
}

// ListActiveForScheduling forwards to the wrapped store's scheduling-optimized
// listing when available, else falls back to the full ListActive.
func (s *AgentSnapshotStore) ListActiveForScheduling() ([]*Workspace, error) {
	if sl, ok := s.Store.(schedulingLister); ok {
		return sl.ListActiveForScheduling()
	}
	return s.ListActive()
}

// eachWorkspaceMeta invokes fn for every workspace. When the store exposes a cheap
// metadata listing (FileStore's lean cache) it iterates that; otherwise it streams
// via List()+Get() one workspace at a time. The agent-snapshot routines read only
// metadata (status, agent references), so the metadata path is sufficient and, now
// that FileStore.Get reads through to disk, avoids re-reading every workspace.json.
func eachWorkspaceMeta(workspaces Store, fn func(ws *Workspace)) {
	if ml, ok := workspaces.(metadataLister); ok {
		for _, ws := range ml.CachedWorkspaces() {
			if ws != nil {
				fn(ws)
			}
		}
		return
	}
	ids, err := workspaces.List()
	if err != nil {
		logger.Warn("agent snapshot: list workspaces failed", logger.Fields{"error": err.Error()})
		return
	}
	for _, id := range ids {
		ws, err := workspaces.Get(id)
		if err != nil || ws == nil {
			continue
		}
		fn(ws)
	}
}

// SnapshotAllWorkspaces walks the workspace store once and snapshots referenced
// agents for every workspace. Intended as a one-shot startup migration so
// existing workspaces become self-contained after startup.
func SnapshotAllWorkspaces(workspaces Store, agents store.Store) {
	if workspaces == nil || agents == nil {
		return
	}
	snapshotter, ok := workspaces.(*AgentSnapshotStore)
	if !ok {
		snapshotter = NewAgentSnapshotStore(workspaces, agents)
	}
	migrated := 0
	eachWorkspaceMeta(workspaces, func(ws *Workspace) {
		if ws.Status == StatusTrashed {
			return
		}
		before := referencedAgentSnapshotCount(workspaces, ws)
		snapshotter.SnapshotReferencedAgents(ws)
		after := referencedAgentSnapshotCount(workspaces, ws)
		if after > before {
			migrated++
		}
	})
	if migrated > 0 {
		logger.Info("Workspace agent snapshots migrated", logger.Fields{"workspaces": migrated})
	}
}

// BackfillLocalWorkspacesIntoAllowlist adds every non-trashed workspace present
// in the given (local folder) store to the allowlist. It is used at startup to
// treat the local ~/Ori Workspaces tree as owned by this data directory, so
// agents referenced by locally-created workspaces are restored — and protected
// from the non-allowlisted wipe — even though creation predates or bypasses the
// explicit import flow. Foreign workspaces not present in the local tree are
// left out, preserving cross-data-directory isolation.
func BackfillLocalWorkspacesIntoAllowlist(local Store, allowlist *Allowlist) {
	if local == nil || allowlist == nil {
		return
	}
	added := 0
	eachWorkspaceMeta(local, func(ws *Workspace) {
		if ws.Status == StatusTrashed {
			return
		}
		if allowlist.Contains(ws.ID) {
			return
		}
		if err := allowlist.Add(ws.ID); err != nil {
			logger.Warn("backfill allowlist: add failed", logger.Fields{
				"workspace_id": ws.ID,
				"error":        err.Error(),
			})
			return
		}
		added++
	})
	if added > 0 {
		logger.Info("Local workspaces backfilled into allowlist", logger.Fields{"workspaces": added})
	}
}

// RestoreAllWorkspaceAgents walks the workspace store once and restores any
// workspace-local agent snapshots into the global agent registry when the
// importing/running environment does not already have those agents.
//
// Deprecated: prefer RestoreAllowlistedWorkspaceAgents to avoid hydrating
// workspaces that have not been explicitly imported into this data directory.
// Retained for callers that genuinely want the un-gated behavior (e.g. tests).
func RestoreAllWorkspaceAgents(workspaces Store, agents store.Store) {
	restoreWorkspaceAgentsFiltered(workspaces, agents, nil)
}

// RestoreAllowlistedWorkspaceAgents restores agent snapshots only for
// workspaces whose ID is present in the allowlist. A nil allowlist restores
// nothing (strict mode). Use this at server startup so workspaces that live
// in the shared ~/Ori Workspaces/ tree do not auto-hydrate into every data
// directory.
func RestoreAllowlistedWorkspaceAgents(workspaces Store, agents store.Store, allowlist *Allowlist) {
	if allowlist == nil {
		return
	}
	restoreWorkspaceAgentsFiltered(workspaces, agents, allowlist)
}

func restoreWorkspaceAgentsFiltered(workspaces Store, agents store.Store, allowlist *Allowlist) {
	if workspaces == nil || agents == nil {
		return
	}
	restoredWorkspaces := 0
	restoredAgents := 0
	eachWorkspaceMeta(workspaces, func(ws *Workspace) {
		if allowlist != nil && !allowlist.Contains(ws.ID) {
			return
		}
		if ws.Status == StatusTrashed {
			return
		}
		registered, err := RestoreWorkspaceAgents(workspaces, ws, agents)
		if err != nil {
			logger.Warn("workspace agent restore: restore failed", logger.Fields{
				"workspace_id": ws.ID,
				"error":        err.Error(),
			})
			return
		}
		if len(registered) > 0 {
			restoredWorkspaces++
			restoredAgents += len(registered)
		}
	})

	if restoredAgents > 0 {
		logger.Info("Workspace agent snapshots restored", logger.Fields{
			"workspaces": restoredWorkspaces,
			"agents":     restoredAgents,
		})
	}
}

// WipeNonAllowlistedAgentSnapshots removes locally-stored agents that exist
// only because some workspace under workspaces has a snapshot for them and no
// allowlisted workspace currently references them. Use at server startup to
// prevent agents from other data directories' workspaces from lingering after
// the allowlist gate is introduced or tightened.
//
// System agents (e.g. "Ori") are never removed. Agents that are not also
// present as a snapshot in some workspace folder are considered user-owned and
// are left alone.
func WipeNonAllowlistedAgentSnapshots(workspaces Store, agents store.Store, allowlist *Allowlist) {
	if workspaces == nil || agents == nil {
		return
	}
	// Set of agent names protected because at least one allowlisted workspace
	// references them.
	allowedReferencedAgents := make(map[string]struct{})
	// Set of agent names that are workspace-managed (i.e. have a snapshot in
	// some workspace folder under `workspaces`). These are wipe candidates if
	// not in allowedReferencedAgents.
	workspaceManagedAgents := make(map[string]struct{})

	eachWorkspaceMeta(workspaces, func(ws *Workspace) {
		if ws.Status == StatusTrashed {
			return
		}
		referenced := referencedAgentNames(ws)
		allowed := allowlist != nil && allowlist.Contains(ws.ID)
		for _, name := range referenced {
			key := strings.ToLower(name)
			if snap, ok, err := workspaces.GetWorkspaceAgent(ws.ID, name); err == nil && ok {
				// Only a pure mirror of the workspace snapshot is a wipe
				// candidate. A global definition the user has since edited
				// (diverged from every snapshot) is user-owned and must be
				// preserved — the boot-wipe must not do what the attached-delete
				// guard forbids (PRD FR11).
				if global, gok := agents.GetAgent(name); !gok || global == nil || agentDefinitionEquivalent(global, snap) {
					workspaceManagedAgents[key] = struct{}{}
				}
			}
			if allowed {
				allowedReferencedAgents[key] = struct{}{}
			}
		}
	})

	wiped := 0
	for _, name := range agents.ListAgents() {
		if isSystemAgentName(name) {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(name))
		if _, managed := workspaceManagedAgents[key]; !managed {
			continue
		}
		if _, allowed := allowedReferencedAgents[key]; allowed {
			continue
		}
		if err := agents.DeleteAgent(name); err != nil {
			logger.Warn("wipe non-allowlisted agent: delete failed", logger.Fields{
				"agent": name,
				"error": err.Error(),
			})
			continue
		}
		wiped++
	}

	if wiped > 0 {
		if err := agents.Save(); err != nil {
			logger.Warn("wipe non-allowlisted agents: persist agent store failed", logger.Fields{
				"error": err.Error(),
			})
		}
		logger.Info("Workspace agent snapshots wiped (not in allowlist)", logger.Fields{
			"agents": wiped,
		})
	}
}

// isSystemAgentName reports whether a snapshot belongs to the system assistant
// under its canonical name or any supported legacy one.
//
// Both are recognized because a snapshot written before a rename can outlive the
// migration of the agent store itself. This used to be a hand-maintained copy of
// the name list kept "in lockstep" by comment; it now shares the one contract so
// it cannot drift again (FR49).
func isSystemAgentName(name string) bool {
	return systemassistant.IsKnownName(name)
}

// agentDefinitionEquivalent reports whether two agents share the same core
// definition (type, role, model, system prompt). The boot-wipe uses it to tell
// a pristine workspace-snapshot mirror (safe to reconcile away under the
// allowlist gate) from a global definition the user has since edited, which is
// user-owned and must be preserved (PRD FR11).
func agentDefinitionEquivalent(a, b *agent.Agent) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Type == b.Type &&
		a.Role == b.Role &&
		a.Settings.Model == b.Settings.Model &&
		a.Settings.SystemPrompt == b.Settings.SystemPrompt
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
