package sessionhttp

import (
	"context"
	"errors"
	"mime"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/continuityprep"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityStatusProvider reports and refreshes a workspace's portable
// checkpoint. continuityprep.Worker satisfies it.
type ContinuityStatusProvider interface {
	Status(ctx context.Context, workspaceID string) (continuityprep.Status, error)
	PrepareNow(ctx context.Context, workspaceID string) (continuityprep.Status, error)
}

// continuityJSONRequest requires a JSON body type on the continuity consent
// endpoints (import, retry, prepare, turning routines on or off). A page on
// another site can send a "simple" POST — text/plain or a form — without
// asking first, but not a JSON one: that needs a CORS preflight, which only
// the configured origins pass.
func continuityJSONRequest(w http.ResponseWriter, r *http.Request) bool {
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil && mediaType == "application/json" {
		return true
	}
	_ = orihttp.RespondJSON(w, http.StatusUnsupportedMediaType, map[string]any{"success": false,
		"error": "This request must be sent as JSON."})
	return false
}

// SetContinuityStatus enables the per-workspace continuity endpoints.
func (h *Handler) SetContinuityStatus(provider ContinuityStatusProvider) {
	h.continuityStatus = provider
}

// handleWorkspaceContinuity serves:
//
//	GET  /api/workspaces/{id}/continuity           checkpoint + background status
//	POST /api/workspaces/{id}/continuity/prepare   refresh the checkpoint now
//	POST /api/workspaces/{id}/continuity/activate  {"enable": bool, "version": n}
//
// Activation is the explicit local choice that lets an imported workspace's
// routines run here; it never follows from anything in the copied folder.
func (h *Handler) handleWorkspaceContinuity(w http.ResponseWriter, r *http.Request, workspaceID, action string) {
	if h.continuityStatus == nil || h.store == nil || h.store.DB() == nil {
		_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Workspace portability is not available in this build."})
		return
	}
	if !workspacecontinuity.ValidID(workspaceID) {
		_ = orihttp.RespondNotFound(w, "workspace not found")
		return
	}
	if _, err := h.store.GetWorkspace(r.Context(), workspaceID); err != nil {
		_ = orihttp.RespondNotFound(w, "workspace not found")
		return
	}
	if r.Method == http.MethodPost && !continuityJSONRequest(w, r) {
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		status, err := h.continuityStatus.Status(r.Context(), workspaceID)
		if err != nil {
			_ = orihttp.RespondInternalError(w, "Could not read this workspace's portability status")
			return
		}
		orihttp.WriteJSON(w, map[string]any{"success": true, "continuity": status})
	case action == "prepare" && r.Method == http.MethodPost:
		status, err := h.continuityStatus.PrepareNow(r.Context(), workspaceID)
		if err != nil {
			_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false,
				"error": "Ori is busy (for example resetting local data). Try again in a moment."})
			return
		}
		orihttp.WriteJSON(w, map[string]any{"success": true, "continuity": status})
	case action == "activate" && r.Method == http.MethodPost:
		var req struct {
			Enable  bool  `json:"enable"`
			Version int64 `json:"version"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
		local := workspacecontinuity.NewLocalStore(h.store.DB())
		err := local.SetImportedActive(r.Context(), workspaceID, req.Version, req.Enable)
		switch {
		case errors.Is(err, workspacecontinuity.ErrConflict), errors.Is(err, workspacecontinuity.ErrInvalid):
			current, _ := h.continuityStatus.Status(r.Context(), workspaceID)
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"success": false,
				"error": "This workspace's state changed. Review it again before changing background routines.", "continuity": current})
			return
		case err != nil:
			_ = orihttp.RespondInternalError(w, "Could not change background routines for this workspace")
			return
		}
		h.notifyContinuityAdmission(workspaceID)
		status, _ := h.continuityStatus.Status(r.Context(), workspaceID)
		orihttp.WriteJSON(w, map[string]any{"success": true, "continuity": status})
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}
