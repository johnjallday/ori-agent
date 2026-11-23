package sessionhttp

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// GetWorkspaceSourceFolders serves display labels, not access to a Home's
// library. It works with just the canonical ID of a directly opened group.
func (h *Handler) GetWorkspaceSourceFolders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h == nil || h.workspaceTaskStore == nil || h.currentUserID == nil {
		_ = orihttp.RespondNotFound(w, "Workspace source folders are unavailable")
		return
	}
	owner, err := h.currentUserID(r.Context())
	if err != nil || strings.TrimSpace(owner) == "" {
		_ = orihttp.RespondNotFound(w, "Workspace source folders are unavailable")
		return
	}
	view, err := h.assistantLibraryStore().ReadWorkspaceFolders(owner, r.PathValue("workspaceID"))
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}
