package downloadsjanitorhttp

import "net/http"

// Register mounts the Downloads Janitor API on mux. Every route is scoped to a
// workspace path segment; there is no global or cross-workspace endpoint, which
// is what keeps one workspace's folder access from being reachable through
// another's (FR-118).
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/downloads-janitor", h.GetStatus)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/downloads-janitor/readiness", h.GetReadiness)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/downloads-janitor/setup", h.ConfirmSetup)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/downloads-janitor/pause", h.SetPaused)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/downloads-janitor/categories", h.Categories)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/downloads-janitor/batches", h.ListBatches)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/downloads-janitor/batches/{batchID}", h.GetBatch)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/downloads-janitor/test-scan", h.TestScan)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/downloads-janitor/scan", h.ScanNow)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/downloads-janitor/decisions", h.UpdateDecisions)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/downloads-janitor/skipped/reset", h.ResetSkipped)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/downloads-janitor/preview", h.PreviewMoves)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/downloads-janitor/apply", h.ConfirmMoves)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/downloads-janitor/history", h.History)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/downloads-janitor/history/{actionID}/undo", h.Undo)
}
