package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

func (h *Handler) handleWorkspaceSettings(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getWorkspaceSettings(w, r, id)
	case http.MethodPatch, http.MethodPut:
		h.updateWorkspaceSettings(w, r, id)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// handleWorkspacePlanningPolicy serves the effective planning policy: the
// guidance/enforcement split, with per-control availability (FR-124, FR-127).
//
// A `preset` query parameter previews a preset the user is considering without
// saving it. The preview runs the SAME computation against the same workspace
// capabilities, so what the screen shows before saving is what will hold after
// — rather than a separate description that can drift from the real one
// (FR-142).
func (h *Handler) handleWorkspacePlanningPolicy(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	workspace, err := h.requireWorkspace(r.Context(), id)
	if err != nil || workspace == nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}

	settings := workspacesettings.Extract(workspace.SharedData)
	caps := h.planningCapabilities(workspace.ID)

	preview := strings.TrimSpace(r.URL.Query().Get("preset"))
	if preview != "" {
		settings = workspacesettings.PresetDefaultsForProfile(settings.Profile, preview)
	}

	orihttp.WriteJSON(w, map[string]any{
		"workspace_id": workspace.ID,
		"policy":       workspacesettings.BuildEffectivePolicy(settings, caps),
		// The capabilities are returned alongside so the UI can explain an
		// unavailable control with the same facts the server used, rather than
		// re-deriving them and possibly disagreeing.
		"capabilities": map[string]any{
			"has_folder":     caps.HasFolder,
			"is_repository":  caps.IsRepository,
			"current_branch": caps.CurrentBranch,
		},
		"previewed_preset": preview,
	})
}

// planningCapabilities reports what the workspace's folder supports. Without a
// resolver wired, nothing filesystem-backed is enforceable, which is the safe
// reading: controls report unavailable rather than claiming enforcement that
// nothing performs.
func (h *Handler) planningCapabilities(workspaceID string) workspacesettings.WorkspaceCapabilities {
	if h.planningPolicy == nil {
		return workspacesettings.WorkspaceCapabilities{}
	}
	return h.planningPolicy.Capabilities(context.Background(), workspaceID)
}

func (h *Handler) getWorkspaceSettings(w http.ResponseWriter, r *http.Request, id string) {
	workspace, err := h.requireWorkspace(r.Context(), id)
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}

	settings := workspacesettings.Extract(workspace.SharedData)
	effective := workspacesettings.BuildEffectiveBehavior(settings)
	orihttp.WriteJSON(w, map[string]any{
		"workspace_id":         workspace.ID,
		"settings":             settings,
		"effective_behavior":   effective,
		"task_markdown_status": h.taskMarkdownSyncStatus(workspace.ID, settings),
	})
}

func (h *Handler) updateWorkspaceSettings(w http.ResponseWriter, r *http.Request, id string) {
	workspace, err := h.requireWorkspace(r.Context(), id)
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}

	var patch map[string]any
	if !orihttp.ParseJSONBody(w, r, &patch) {
		return
	}

	sharedData, settings := workspacesettings.ApplyPatch(workspace.SharedData, patch)
	if err := workspacesettings.ValidateTaskMarkdownPath(settings.TaskMarkdown.Path); err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	workspace.SharedData = sharedData
	workspace.UpdatedAt = settings.UpdatedAt

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace settings", logger.Fields{
			"workspace_id": id,
			"error":        err,
		})
		_ = orihttp.RespondInternalError(w, "Failed to update workspace settings")
		return
	}
	if err := h.syncWorkspacePortableStateToFileStore(workspace); err != nil {
		logger.Warn("Failed to sync workspace.json after workspace settings update", logger.Fields{
			"workspace_id": id,
			"error":        err,
		})
	}

	effective := workspacesettings.BuildEffectiveBehavior(settings)
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"success":              true,
		"workspace_id":         workspace.ID,
		"settings":             settings,
		"effective_behavior":   effective,
		"task_markdown_status": h.taskMarkdownSyncStatus(workspace.ID, settings),
	}); encErr != nil {
		logger.Error("Failed to encode workspace settings response", logger.Fields{"error": encErr})
	}
}

func (h *Handler) taskMarkdownSyncStatus(workspaceID string, settings workspacesettings.Settings) map[string]any {
	return agentworkspace.TaskMarkdownStatusForSettings(h.workspaceStore, workspaceID, settings.TaskMarkdown)
}
