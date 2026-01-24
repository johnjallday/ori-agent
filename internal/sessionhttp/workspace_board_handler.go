package sessionhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
)

func (h *Handler) handleWorkspaceBoard(w http.ResponseWriter, r *http.Request, workspaceID string) {
	switch r.Method {
	case http.MethodGet:
		h.getWorkspaceBoard(w, r, workspaceID)
	case http.MethodPut:
		h.updateWorkspaceBoard(w, r, workspaceID)
	case http.MethodPatch:
		h.updateWorkspaceBoard(w, r, workspaceID)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

func (h *Handler) getWorkspaceBoard(w http.ResponseWriter, r *http.Request, workspaceID string) {
	ws, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace for board", logger.Fields{"workspace_id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	board, _ := session.GetWorkspaceKanbanBoardConfig(ws)

	orihttp.WriteJSON(w, map[string]interface{}{
		"workspace_id": workspaceID,
		"board":        board,
	})
}

func (h *Handler) updateWorkspaceBoard(w http.ResponseWriter, r *http.Request, workspaceID string) {
	ws, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace for board update", logger.Fields{"workspace_id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	var req struct {
		Version int                         `json:"version,omitempty"`
		Columns []session.KanbanBoardColumn `json:"columns"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	cfg := session.KanbanBoardConfig{Version: req.Version, Columns: req.Columns}
	if err := session.SetWorkspaceKanbanBoardConfig(ws, cfg); err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}

	if err := h.store.UpdateWorkspace(r.Context(), ws); err != nil {
		logger.Error("Failed to update workspace board", logger.Fields{"workspace_id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to update workspace")
		return
	}

	updated, _ := session.GetWorkspaceKanbanBoardConfig(ws)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"board":   updated,
	})
}
