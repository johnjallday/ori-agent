package workspacecapabilityhttp

import "net/http"

// Register mounts the capability lifecycle API on mux. Every route is scoped to
// a workspace path segment; there is no global or cross-workspace endpoint,
// which is what keeps one workspace's capability state from being reachable
// through another's (FR-140).
//
// Removal (DELETE .../capabilities/{capabilityID}) is deliberately absent until
// the removal lifecycle exists: an uninstall that could not first stop watchers
// and release folder access would be unsafe (FR-26).
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/capabilities", h.ListCapabilities)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/capabilities/{capabilityID}/install", h.InstallCapability)
}
