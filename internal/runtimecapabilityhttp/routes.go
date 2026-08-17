package runtimecapabilityhttp

import "net/http"

// Register mounts only workspace-scoped, method-specific runtime routes.
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	const root = "/api/workspaces/{workspaceID}/runtime-capabilities"
	mux.HandleFunc("GET "+root, h.GetStatus)
	mux.HandleFunc("PUT "+root+"/mode", h.SelectMode)
	mux.HandleFunc("POST "+root+"/recheck", h.Recheck)
	mux.HandleFunc("POST "+root+"/requirements/{requirementKey}/actions/{actionToken}", h.ConfirmAction)
	mux.HandleFunc("POST "+root+"/requirements/{requirementKey}/verify", h.Verify)
	mux.HandleFunc("POST "+root+"/requirements/{requirementKey}/grants", h.Grant)
	mux.HandleFunc("DELETE "+root+"/requirements/{requirementKey}/grants", h.Revoke)
}
