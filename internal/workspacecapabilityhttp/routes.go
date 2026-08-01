package workspacecapabilityhttp

import "net/http"

// Register mounts the capability lifecycle API on mux. Every route is scoped to
// a workspace path segment; there is no global or cross-workspace endpoint,
// which is what keeps one workspace's capability state from being reachable
// through another's (FR-140).
//
// Removal is two endpoints on purpose. The summary is a GET with no side
// effects, so the confirmation a user reads is derived from the same resolution
// the removal will perform — not from a description written alongside it that
// could drift (FR-24, FR-25).
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/capabilities", h.ListCapabilities)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/capabilities/{capabilityID}/install", h.InstallCapability)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/capabilities/{capabilityID}/companion", h.AddCompanion)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/capabilities/{capabilityID}/removal", h.RemovalSummary)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/capabilities/{capabilityID}", h.RemoveCapability)
}
