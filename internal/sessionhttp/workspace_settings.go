package sessionhttp

import (
	"encoding/json"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
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
	workspace, err := h.requireConcreteWorkspace(r.Context(), id)
	if err != nil {
		switch err {
		case errWorkspaceDisallowsDirectUse:
			_ = orihttp.RespondBadRequest(w, err.Error())
		default:
			_ = orihttp.RespondNotFound(w, "Workspace not found")
		}
		return
	}

	settings := workspacesettings.Extract(workspace.SharedData)
	effective := workspacesettings.BuildEffectiveBehavior(settings)
	orihttp.WriteJSON(w, map[string]interface{}{
		"workspace_id":       workspace.ID,
		"settings":           settings,
		"effective_behavior": effective,
	})
}

func (h *Handler) updateWorkspaceSettings(w http.ResponseWriter, r *http.Request, id string) {
	workspace, err := h.requireConcreteWorkspace(r.Context(), id)
	if err != nil {
		switch err {
		case errWorkspaceDisallowsDirectUse:
			_ = orihttp.RespondBadRequest(w, err.Error())
		default:
			_ = orihttp.RespondNotFound(w, "Workspace not found")
		}
		return
	}

	var patch map[string]interface{}
	if !orihttp.ParseJSONBody(w, r, &patch) {
		return
	}

	sharedData, settings := workspacesettings.ApplyPatch(workspace.SharedData, patch)
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

	effective := workspacesettings.BuildEffectiveBehavior(settings)
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"success":            true,
		"workspace_id":       workspace.ID,
		"settings":           settings,
		"effective_behavior": effective,
	}); encErr != nil {
		logger.Error("Failed to encode workspace settings response", logger.Fields{"error": encErr})
	}
}
