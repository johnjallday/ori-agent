package agentmaphttp

import "net/http"

// Register mounts the current-user agent-map layout API on mux.
//
// One path, three methods, no identifiers: the layout is the requesting user's
// by construction, and there is no route shape here that could address someone
// else's map. The path sits outside /api/agents/ so it can never be confused
// with an agent record — nothing under this prefix can change one.
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/agent-map/layout", h.GetLayout)
	mux.HandleFunc("PATCH /api/agent-map/layout", h.PatchLayout)
	mux.HandleFunc("DELETE /api/agent-map/layout", h.ResetLayout)
}
