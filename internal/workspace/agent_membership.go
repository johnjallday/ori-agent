package workspace

import (
	"sort"
	"strings"
)

// WorkspaceRef is a lightweight reference to a workspace that uses an agent
// definition. It is serialized into the agents API membership annotation so the
// Agents page can show which workspaces a definition is attached to.
type WorkspaceRef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	EntryPoint bool   `json:"entry_point"`
}

// AgentMembership records the workspaces that reference a single agent
// definition (by name): the count plus a stable, name-sorted list of refs.
type AgentMembership struct {
	Count      int            `json:"workspace_count"`
	Workspaces []WorkspaceRef `json:"workspaces"`
}

// AgentWorkspaceMemberships returns, keyed by lowercase agent name, the set of
// non-trashed / non-missing workspaces that reference each agent definition —
// via the workspace entry agent or any AgentInstance.
//
// It is computed from the metadata-only workspace cache when the store exposes
// one (see eachWorkspaceMeta), so it never hydrates full workspaces:
// /api/agents and /api/agents/dashboard/list are hot paths and an
// O(workspaces) full-hydrate walk per request is prohibited (PRD FR2).
//
// A workspace is counted once per agent even when the agent is both the entry
// agent and an instance (referencedAgentNames dedups names within a workspace,
// and refs are deduped by workspace ID here). A group workspace and its nested
// members are distinct workspaces, so members never double-count a shared
// definition (PRD FR1).
func AgentWorkspaceMemberships(workspaces Store) map[string]AgentMembership {
	out := make(map[string]AgentMembership)
	if workspaces == nil {
		return out
	}

	// Per agent name → (workspace ID → ref), so duplicate references collapse.
	refsByAgent := make(map[string]map[string]WorkspaceRef)

	eachWorkspaceMeta(workspaces, func(ws *Workspace) {
		if ws == nil {
			return
		}
		if ws.Status == StatusTrashed || ws.Status == StatusMissing {
			return
		}
		entry := strings.ToLower(strings.TrimSpace(ws.EntryAgentName()))
		for _, name := range referencedAgentNames(ws) {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			refs := refsByAgent[key]
			if refs == nil {
				refs = make(map[string]WorkspaceRef)
				refsByAgent[key] = refs
			}
			isEntry := key == entry
			if existing, ok := refs[ws.ID]; ok {
				// Keep entry_point sticky if any reference within the workspace
				// is the entry agent.
				isEntry = isEntry || existing.EntryPoint
			}
			refs[ws.ID] = WorkspaceRef{ID: ws.ID, Name: ws.Name, EntryPoint: isEntry}
		}
	})

	for key, refs := range refsByAgent {
		out[key] = AgentMembership{Count: len(refs), Workspaces: sortWorkspaceRefs(refs)}
	}
	return out
}

// WorkspaceMembershipFor returns the membership for a single agent name,
// computed from the metadata-only cache. It is the mutation-path counterpart to
// AgentWorkspaceMemberships, used by the shared-edit / rename / delete guards
// that only need one definition's attachment set (PRD FR9–FR11).
func WorkspaceMembershipFor(workspaces Store, agentName string) AgentMembership {
	key := strings.ToLower(strings.TrimSpace(agentName))
	if workspaces == nil || key == "" {
		return AgentMembership{}
	}
	refs := make(map[string]WorkspaceRef)
	eachWorkspaceMeta(workspaces, func(ws *Workspace) {
		if ws == nil || ws.Status == StatusTrashed || ws.Status == StatusMissing {
			return
		}
		entry := strings.ToLower(strings.TrimSpace(ws.EntryAgentName()))
		for _, name := range referencedAgentNames(ws) {
			if strings.ToLower(strings.TrimSpace(name)) != key {
				continue
			}
			isEntry := key == entry
			if existing, ok := refs[ws.ID]; ok {
				isEntry = isEntry || existing.EntryPoint
			}
			refs[ws.ID] = WorkspaceRef{ID: ws.ID, Name: ws.Name, EntryPoint: isEntry}
			break
		}
	})
	return AgentMembership{Count: len(refs), Workspaces: sortWorkspaceRefs(refs)}
}

// sortWorkspaceRefs flattens a workspace-ID→ref map into a stable, name-sorted
// slice.
func sortWorkspaceRefs(refs map[string]WorkspaceRef) []WorkspaceRef {
	list := make([]WorkspaceRef, 0, len(refs))
	for _, r := range refs {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].ID < list[j].ID
	})
	return list
}
