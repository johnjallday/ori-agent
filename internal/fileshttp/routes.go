package fileshttp

import "net/http"

// RegisterRoutes registers all session-file HTTP routes on the given mux using
// Go 1.22 method+pattern routing. The {id} (session) and {fileId} path
// parameters are read by the handlers via r.PathValue.
//
// These patterns are strictly more specific than the "/api/sessions/" subtree
// that the session handler owns, so ServeMux dispatches file requests here and
// leaves everything else (session CRUD, tags, tasks) to the session handler.
//
// Routes registered:
//   - POST   /api/sessions/{id}/files/upload             - Upload a file
//   - POST   /api/sessions/{id}/files/link               - Link to an external file
//   - POST   /api/sessions/{id}/files/validate           - Validate all links
//   - GET    /api/sessions/{id}/files/events             - SSE stream for file changes
//   - POST   /api/sessions/{id}/files/watch              - Start watching folder
//   - DELETE /api/sessions/{id}/files/watch              - Stop watching folder
//   - POST   /api/sessions/{id}/folder/open              - Open folder in file manager
//   - GET    /api/sessions/{id}/files                    - List all files
//   - GET    /api/sessions/{id}/files/{fileId}           - Get file metadata
//   - DELETE /api/sessions/{id}/files/{fileId}           - Delete a file
//   - GET    /api/sessions/{id}/files/{fileId}/download  - Download file
//   - POST   /api/sessions/{id}/files/{fileId}/relink    - Re-link a broken symlink
func RegisterRoutes(mux *http.ServeMux, h *Handler) {
	mux.HandleFunc("POST /api/sessions/{id}/files/upload", h.UploadFile)
	mux.HandleFunc("POST /api/sessions/{id}/files/link", h.LinkFile)
	mux.HandleFunc("POST /api/sessions/{id}/files/validate", h.ValidateLinks)
	mux.HandleFunc("GET /api/sessions/{id}/files/events", h.FileEvents)
	mux.HandleFunc("POST /api/sessions/{id}/files/watch", h.StartWatching)
	mux.HandleFunc("DELETE /api/sessions/{id}/files/watch", h.StopWatching)
	mux.HandleFunc("POST /api/sessions/{id}/folder/open", h.OpenFolder)
	mux.HandleFunc("GET /api/sessions/{id}/files", h.ListFiles)
	mux.HandleFunc("GET /api/sessions/{id}/files/{fileId}", h.GetFile)
	mux.HandleFunc("DELETE /api/sessions/{id}/files/{fileId}", h.DeleteFile)
	mux.HandleFunc("GET /api/sessions/{id}/files/{fileId}/download", h.DownloadFile)
	mux.HandleFunc("POST /api/sessions/{id}/files/{fileId}/relink", h.RelinkFile)
}
