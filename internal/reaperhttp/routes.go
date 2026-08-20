package reaperhttp

import "net/http"

func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/reaper/state", h.GetState)
}
