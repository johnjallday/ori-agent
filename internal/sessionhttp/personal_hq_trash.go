package sessionhttp

import (
	"context"
	"errors"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SetPersonalHQTrashLookup authorizes the Settings shortcut against the
// request's current user and the canonical Personal HQ designation.
func (h *Handler) SetPersonalHQTrashLookup(lookup func(context.Context) (*personalhq.Status, error)) {
	h.personalHQTrashLookup = lookup
}

// trashPersonalHQ is a trash-only mode of the existing workspace lifecycle.
// Neither an ineligible target nor an unavailable system Trash can fall
// through to the generic delete path.
func (h *Handler) trashPersonalHQ(w http.ResponseWriter, r *http.Request, ws *session.Workspace) {
	if h.personalHQTrashLookup == nil || h.workspaceStore == nil {
		orihttp.ServiceUnavailable(w, "Personal HQ folder removal is unavailable.")
		return
	}
	ctx := r.Context()
	status, err := h.personalHQTrashLookup(ctx)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Could not check the current Personal HQ.")
		return
	}
	if status == nil || !status.Valid || status.WorkspaceID != ws.ID || status.UserID == "" || ws.OwnerUserID != status.UserID {
		_ = orihttp.RespondConflict(w, "The workspace is no longer your current Personal HQ.")
		return
	}
	if ws.Kind == session.WorkspaceKindGroup || ws.ParentID != "" || workspace.ShapeFromSharedData(ws.SharedData) != workspace.ShapeManaged ||
		r.URL.Query().Get("delete_sessions") == "true" {
		_ = orihttp.RespondConflict(w, "Only a standalone, Ori-managed Personal HQ folder can be moved to Trash here.")
		return
	}
	if !h.allowWorkspaceRemoval(w, r, ws) {
		return
	}
	path, err := h.workspaceStore.ReviewManagedTrash(ws.ID)
	if err != nil {
		_ = orihttp.RespondConflict(w, "Ori could not verify a managed Personal HQ folder that can be moved to Trash.")
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		orihttp.WriteJSON(w, map[string]string{"folder_path": path, "name": ws.Name})
		return
	}
	var req struct {
		FolderPath string `json:"folder_path"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.FolderPath != path {
		_ = orihttp.RespondConflict(w, "The Personal HQ folder changed. Review it again before moving it to Trash.")
		return
	}
	original, trashed, err := h.workspaceStore.TrashReviewedManaged(ws.ID, path)
	if errors.Is(err, workspace.ErrManagedTrashUnavailable) {
		_ = orihttp.RespondConflict(w, "The Personal HQ folder changed. Review it again before moving it to Trash.")
		return
	}
	if err == nil {
		err = h.recordWorkspaceTrash(context.WithoutCancel(ctx), ws, original, trashed)
	}
	if err != nil {
		logger.Error("Failed to move Personal HQ to Trash", logger.Fields{"workspace_id": ws.ID, "error": err})
		_ = orihttp.RespondInternalError(w, "Could not finish moving Personal HQ to Trash. Check its folder and the system Trash before retrying.")
		return
	}
	orihttp.WriteJSON(w, map[string]any{"id": ws.ID, "trashed": true})
}
