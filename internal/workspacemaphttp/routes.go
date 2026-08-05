package workspacemaphttp

import "net/http"

// Register mounts the current-user map-layout API on mux.
//
// One path, three methods, no identifiers: the layout is the requesting user's
// by construction, and there is no route shape here that could address someone
// else's map (FR-98). The path is deliberately not under /api/workspaces/, so
// it can never be confused with a single workspace's internal Canvas layout
// (FR-104).
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspace-map/layout", h.GetLayout)
	mux.HandleFunc("PATCH /api/workspace-map/layout", h.PatchLayout)
	mux.HandleFunc("DELETE /api/workspace-map/layout", h.ResetLayout)
}
