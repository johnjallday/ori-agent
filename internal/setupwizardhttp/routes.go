package setupwizardhttp

import "net/http"

// Register mounts the Setup Wizard API on mux.
//
// Every route is scoped to a workspace path segment and every method is stated
// explicitly: there is no global or cross-workspace endpoint, and no route that
// answers to any verb. A step is addressed by an ID the workspace itself
// recorded, so the path can select one of its own steps or nothing at all.
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/setup-wizard", h.GetStatus)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/setup-wizard/open", h.Open)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/setup-wizard/dismiss", h.Dismiss)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/setup-wizard/recheck", h.Recheck)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/setup-wizard/complete", h.Complete)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/setup-wizard/steps/{stepID}/confirm", h.ConfirmStep)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/setup-wizard/steps/{stepID}/skip", h.SkipStep)
}
