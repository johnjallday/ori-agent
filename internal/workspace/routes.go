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

	// Tasks (G2b). output-spec/draft accepts POST+PATCH and output-spec/discard
	// accepts POST+DELETE, each registered as separate patterns to one handler.
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/tasks", h.CreateTask)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/tasks/{taskId}", h.UpdateTask)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/tasks/{taskId}", h.DeleteTask)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/tasks/{taskId}/execute", h.ExecuteTaskManually)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/tasks/{taskId}/results/append-csv", h.AppendResultToCSV)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/tasks/{taskId}/results/export-csv", h.ExportResultCSV)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/tasks/{taskId}/output-spec/draft", h.SaveTaskOutputSpecDraft)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/tasks/{taskId}/output-spec/draft", h.SaveTaskOutputSpecDraft)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/tasks/{taskId}/output-spec/approve", h.ApproveTaskOutputSpecDraft)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/tasks/{taskId}/output-spec/discard", h.DiscardTaskOutputSpecDraft)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/tasks/{taskId}/output-spec/discard", h.DiscardTaskOutputSpecDraft)

	// Store nodes (G2c). Available under both /store-nodes and the historical
	// /canvas/store-nodes alias. PUT updates are accepted on the plain surface
	// only (the alias was always 405 for PUT), preserved by omitting the canvas
	// PUT pattern. Reading {nodeId} via PathValue is identical for both aliases,
	// which also fixes the old canvas node-id off-by-one (parts[2] vs parts[3]).
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/store-nodes", h.CreateStoreNode)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/store-nodes", h.GetStoreNodes)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/store-nodes/{nodeId}/status", h.GetStoreNodeStatus)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/store-nodes/{nodeId}", h.UpdateStoreNode)
	mux.HandleFunc("PUT /api/workspaces/{workspaceID}/store-nodes/{nodeId}", h.UpdateStoreNode)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/store-nodes/{nodeId}", h.DeleteStoreNode)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/canvas/store-nodes", h.CreateStoreNode)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/canvas/store-nodes", h.GetStoreNodes)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/canvas/store-nodes/{nodeId}/status", h.GetStoreNodeStatus)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/canvas/store-nodes/{nodeId}", h.UpdateStoreNode)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/canvas/store-nodes/{nodeId}", h.DeleteStoreNode)
}
