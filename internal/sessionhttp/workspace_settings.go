package sessionhttp

import (
	"encoding/json"
	"net/http"

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
