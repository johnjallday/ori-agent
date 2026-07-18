package workspace

import "net/http"

// RegisterRoutes registers workspace-runtime HTTP routes on the mux using Go
// 1.22 method+pattern routing. These patterns are strictly more specific than
// the "/api/workspaces/" subtree the server's handleWorkspaceAPI owns, so
// ServeMux dispatches them here and leaves workspace CRUD (and any not-yet-
// migrated runtime families still handled by routeWorkspaceRuntimeRequest) to
// that handler.
//
// Handlers read {workspaceID} (workspace) and {attachmentId} via r.PathValue.
//
// This function grows one route family per router-refactor slice; today it
// covers attachments and trash (G2a).
func RegisterRoutes(mux *http.ServeMux, h *HTTPHandler) {
	// Attachments + trash (G2a). The literal /attachments/bulk-trash is more
	// specific than /attachments/{attachmentId}, and each sub-action
	// (trash/restore/relink/locate/move) is more specific than the bare
	// /attachments/{attachmentId}, so ServeMux precedence resolves them
	// unambiguously.
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/attachments", h.CreateAttachment)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/attachments/bulk-trash", h.BulkMoveToTrash)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/attachments/{attachmentId}", h.UpdateAttachment)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/attachments/{attachmentId}", h.DeleteAttachment)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/attachments/{attachmentId}/trash", h.MoveToTrash)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/attachments/{attachmentId}/restore", h.RestoreFromTrash)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/attachments/{attachmentId}/relink", h.RelinkAttachmentFile)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/attachments/{attachmentId}/locate", h.LocateAttachmentFile)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/attachments/{attachmentId}/move", h.MoveAttachmentFile)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/trash", h.ListTrash)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/trash/{attachmentId}", h.EmptyTrash)
}
