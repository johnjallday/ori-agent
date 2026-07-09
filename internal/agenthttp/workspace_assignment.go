package agenthttp

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// assignWorkspacesRequest is the desired-state body for PUT
// /api/agents/{name}/workspaces: the complete set of workspace IDs the agent
// should belong to. The server reconciles current membership to this set.
type assignWorkspacesRequest struct {
	WorkspaceIDs []string `json:"workspace_ids"`
}

// AssignWorkspaces reconciles an agent's workspace membership to the desired set
// in one call (Agents page redesign, PRD FR9 / §7.2). It is the agent-centric
// counterpart to the workspace-centric POST/DELETE
// /api/workspaces/{id}/agents endpoints: the Workspaces tab edits membership
// from the agent's perspective, so a single atomic-validated call is cleaner
// than a client-side fan-out.
//
// Semantics:
//   - Body carries the full desired workspace-ID set; the diff against current
//     membership yields adds and removes.
//   - Pre-flight validates the whole request (agent exists and is editable,
//     every desired workspace resolves, no removal strips a workspace's entry
//     agent) BEFORE any mutation, so a bad request fails without partial writes.
//   - Removing an agent from a workspace drops every instance of it there.
func (h *Handler) AssignWorkspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		orihttp.MethodNotAllowed(w)
		return
	}

	agentName := agentNameFromWorkspacesPath(r.URL.Path)
	if agentName == "" {
		orihttp.BadRequest(w, "Agent name is required")
		return
	}
	if h.workspaceStore == nil {
		orihttp.InternalError(w, "workspace store unavailable")
		return
	}

	// CLI agents are built-in and cannot be attached/detached this way.
	if h.isCLIAgent(agentName) {
		orihttp.BadRequest(w, "CLI agents are built-in and cannot be assigned to workspaces")
		return
	}
	if _, ok := h.State.GetAgent(agentName); !ok {
		orihttp.NotFound(w, "Agent not found")
		return
	}

	var req assignWorkspacesRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	desired := normalizeIDSet(req.WorkspaceIDs)

	// Current membership (metadata-only cache; no full hydrate).
	membership := workspace.WorkspaceMembershipFor(h.workspaceStore, agentName)
	current := make(map[string]workspace.WorkspaceRef, len(membership.Workspaces))
	for _, ref := range membership.Workspaces {
		current[ref.ID] = ref
	}

	// Diff into add / remove sets.
	var toAdd, toRemove []string
	for id := range desired {
		if _, in := current[id]; !in {
			toAdd = append(toAdd, id)
		}
	}
	for id, ref := range current {
		if _, keep := desired[id]; keep {
			continue
		}
		// Blocking the entry agent's removal mirrors the workspace store's
		// ErrWorkspaceEntryAgentRequired guard; the UI must reassign the entry
		// agent first.
		if ref.EntryPoint {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error":        "entry_agent_removal_blocked",
				"message":      fmt.Sprintf("%q is the entry agent of %q and cannot be unassigned. Set a different entry agent first.", agentName, ref.Name),
				"workspace_id": id,
			})
			return
		}
		toRemove = append(toRemove, id)
	}

	// Pre-flight: every desired workspace must resolve. Fail before mutating.
	for _, id := range toAdd {
		if _, err := h.workspaceStore.Get(id); err != nil {
			orihttp.BadRequest(w, fmt.Sprintf("Workspace not found: %s", id))
			return
		}
	}

	// Apply removals, then additions. Stable order keeps behavior deterministic
	// and test-friendly.
	sort.Strings(toRemove)
	sort.Strings(toAdd)

	for _, id := range toRemove {
		if err := h.removeAgentFromWorkspace(id, agentName); err != nil {
			logger.Error("assign workspaces: remove failed", logger.Fields{"agent": agentName, "workspace": id, "error": err})
			orihttp.InternalError(w, fmt.Sprintf("Failed to unassign from workspace %s: %v", id, err))
			return
		}
	}
	for _, id := range toAdd {
		ws, err := h.workspaceStore.Get(id)
		if err != nil {
			orihttp.BadRequest(w, fmt.Sprintf("Workspace not found: %s", id))
			return
		}
		if err := ws.AddAgent(agentName); err != nil {
			// Already a member is a no-op success (idempotent reconcile).
			if errors.Is(err, workspace.ErrAgentAlreadyInWorkspace) {
				continue
			}
			logger.Error("assign workspaces: add failed", logger.Fields{"agent": agentName, "workspace": id, "error": err})
			orihttp.InternalError(w, fmt.Sprintf("Failed to assign to workspace %s: %v", id, err))
			return
		}
		if err := h.workspaceStore.Save(ws); err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace %s: %v", id, err))
			return
		}
	}

	// Return the reconciled membership so the client can refresh without a
	// second round trip.
	updated := workspace.WorkspaceMembershipFor(h.workspaceStore, agentName)
	orihttp.Success(w, map[string]any{
		"success":         true,
		"agent":           agentName,
		"workspace_count": updated.Count,
		"workspaces":      updated.Workspaces,
	})
}

// removeAgentFromWorkspace drops every instance of agentName from the workspace,
// then persists it. A workspace where the agent is not present is a no-op.
func (h *Handler) removeAgentFromWorkspace(workspaceID, agentName string) error {
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil {
		return err
	}
	// Collect instance IDs first; RemoveAgentInstance mutates the slice.
	var instanceIDs []string
	for _, inst := range ws.AgentInstances {
		if strings.EqualFold(inst.Name, agentName) {
			instanceIDs = append(instanceIDs, inst.ID)
		}
	}
	if len(instanceIDs) == 0 {
		return nil
	}
	for _, id := range instanceIDs {
		if err := ws.RemoveAgentInstance(id); err != nil {
			return err
		}
	}
	return h.workspaceStore.Save(ws)
}

// normalizeIDSet trims, drops blanks, and dedups a workspace-ID slice into a set.
func normalizeIDSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		set[id] = struct{}{}
	}
	return set
}

// agentNameFromWorkspacesPath extracts {name} from
// /api/agents/{name}/workspaces, URL-decoding the segment.
func agentNameFromWorkspacesPath(path string) string {
	rest := strings.TrimPrefix(path, "/api/agents/")
	rest = strings.TrimSuffix(rest, "/workspaces")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return ""
	}
	if decoded, err := url.PathUnescape(rest); err == nil {
		return decoded
	}
	return rest
}
