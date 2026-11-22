package filejanitorhttp

import (
	"net/http"
	"runtime"

	"github.com/johnjallday/ori-agent/internal/backuprecoveryhttp"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/platform"
)

func localFinderAvailable(r *http.Request) bool {
	return runtime.GOOS == "darwin" && !platform.DesktopOpenDisabled() &&
		backuprecoveryhttp.LocalRequestAllowed(r, false) &&
		r.Header.Get("Forwarded") == "" && r.Header.Get("X-Forwarded-For") == ""
}

func (h *Handler) RevealReviewFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	h.inspectReviewFile(w, r, workspaceID, "reveal")
}

func (h *Handler) OpenReviewFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	h.inspectReviewFile(w, r, workspaceID, "open")
}

func (h *Handler) inspectReviewFile(w http.ResponseWriter, r *http.Request, workspaceID, action string) {
	// LocalRequestAllowed checks the exact origin when present. Require it
	// and an explicit non-simple request header for the desktop action.
	if !localFinderAvailable(r) || r.Header.Get("Origin") == "" || r.Header.Get("X-Ori-Filing-Action") != action {
		orihttp.Forbidden(w, "Inspect files only from the local Ori window.")
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	path, err := h.service.ReviewInspectionPath(r.Context(), owner, workspaceID, r.PathValue("batchID"), r.URL.Query().Get("revision"), r.PathValue("candidateID"))
	if err != nil {
		h.respondPictureError(w, err)
		return
	}
	launch := h.revealFile
	if action == "open" {
		if platform.ReviewDocumentViewer(path) == "" {
			orihttp.BadRequest(w, "This format can only be shown in Finder.")
			return
		}
		launch = h.openDocument
	}
	if err := launch(path); err != nil {
		_ = orihttp.RespondInternalError(w, "The file could not be shown.")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]bool{"success": true})
}
