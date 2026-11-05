package agenthttp

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// settingsOwnershipSuffix is the read-only inspection of which saved settings
// apply to an agent in one context and where they come from.
const settingsOwnershipSuffix = "/settings-ownership"

// maxOwnershipIDLen bounds the workspace and instance IDs a request may name.
const maxOwnershipIDLen = 200

// CopyTrackingReader reports, read-only, how one workspace copy of an agent
// relates to the record a Home consent keeps for it (projectstaffing).
type CopyTrackingReader interface {
	CopyTracking(agentName, workspaceID string, library, copyAgent *agent.Agent) (string, error)
}

// SetCopyTrackingReader wires the tracking reader for the settings-ownership
// inspection. Without one, tracking is reported as unknown.
func (h *DashboardHandler) SetCopyTrackingReader(reader CopyTrackingReader) {
	h.copyTracking = reader
}

// SetSkillInventory wires the skill-collection read used to report whether a
// selected skill's source still offers it. Without one, that is unknown.
func (h *DashboardHandler) SetSkillInventory(list func(agentName string) ([]string, error)) {
	h.skillInventory = list
}

// IsSettingsOwnershipPath reports whether path is
// /api/agents/{name}/settings-ownership.
func IsSettingsOwnershipPath(path string) bool {
	rest, ok := strings.CutPrefix(path, "/api/agents/")
	if !ok {
		return false
	}
	name, found := strings.CutSuffix(rest, settingsOwnershipSuffix)
	return found && name != "" && !strings.Contains(name, "/")
}

// canonicalOwnershipName maps a request name to the library's own spelling
// when the exact name has no library record: a retired name of the built-in
// assistant, or a different letter case of one of the user's agents (library
// names are unique ignoring case). Any other name is used exactly, so an
// inspection is never silently pointed at a different agent.
func canonicalOwnershipName(st store.Store, name string) string {
	if st == nil || store.LibraryDefinition(st, name).State != store.LibraryAbsent {
		return name
	}
	if _, resolved, ok := store.ResolveAgent(st, name); ok && resolved != name &&
		store.LibraryDefinition(st, resolved).State == store.LibraryPresent {
		return resolved
	}
	for _, candidate := range st.ListAgents() {
		if candidate != name && strings.EqualFold(candidate, name) &&
			store.LibraryDefinition(st, candidate).State == store.LibraryPresent {
			return candidate
		}
	}
	return name
}

// GetSettingsOwnership handles GET /api/agents/{name}/settings-ownership.
//
// Query: studio_id selects a workspace context (absent = the library
// profile); instance_id selects one of the agent's instances there. It answers
// with whitelisted values, their source and comparison only. It never writes,
// migrates, assigns, registers tools or calls a model.
func (h *DashboardHandler) GetSettingsOwnership(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		_ = orihttp.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	escaped := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/api/agents/"), settingsOwnershipSuffix)
	name, err := url.PathUnescape(escaped)
	name = strings.TrimSpace(name)
	if err != nil || name == "" || strings.Contains(name, "/") {
		orihttp.BadRequest(w, "Agent name is required")
		return
	}
	query := r.URL.Query()
	workspaceID := strings.TrimSpace(query.Get("studio_id"))
	instanceID := strings.TrimSpace(query.Get("instance_id"))
	if len(workspaceID) > maxOwnershipIDLen || len(instanceID) > maxOwnershipIDLen || (instanceID != "" && workspaceID == "") ||
		strings.ContainsAny(workspaceID+instanceID, "/\\\x00") {
		orihttp.BadRequest(w, "Invalid workspace or instance")
		return
	}

	src := workspace.SettingsOwnershipSources{
		Workspaces: h.workspaceStore,
		Library:    func(n string) store.LibraryRecord { return store.LibraryDefinition(h.State, n) },
	}
	name = canonicalOwnershipName(h.State, name)
	if h.copyTracking != nil {
		src.Tracking = h.copyTracking.CopyTracking
	}
	src.SkillInventory = h.skillInventory
	out, err := workspace.ProjectSettingsOwnership(src, workspace.SettingsOwnershipRequest{
		Agent: name, WorkspaceID: workspaceID, InstanceID: instanceID,
	})
	if errors.Is(err, workspace.ErrSettingsOwnershipAgentNotFound) {
		orihttp.NotFound(w, "Agent not found")
		return
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to inspect agent settings")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = orihttp.RespondSuccess(w, out)
}
