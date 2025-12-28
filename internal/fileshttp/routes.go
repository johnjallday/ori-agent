package fileshttp

import (
	"net/http"
	"strings"
)

// RegisterRoutes registers all file management HTTP routes on the given mux.
// This should be called from internal/server/routes.go
//
// Routes registered:
//   - POST   /api/sessions/{id}/files/upload   - Upload a file
//   - POST   /api/sessions/{id}/files/link     - Link to an external file
//   - GET    /api/sessions/{id}/files          - List all files
//   - GET    /api/sessions/{id}/files/{fileId} - Get file metadata
//   - GET    /api/sessions/{id}/files/{fileId}/download - Download file
//   - DELETE /api/sessions/{id}/files/{fileId} - Delete a file
//   - POST   /api/sessions/{id}/files/{fileId}/relink - Re-link a broken symlink
//   - POST   /api/sessions/{id}/files/validate - Validate all links
//   - POST   /api/sessions/{id}/folder/open    - Open folder in file manager
//   - GET    /api/sessions/{id}/files/events   - SSE stream for file changes
//   - POST   /api/sessions/{id}/files/watch    - Start watching folder
//   - DELETE /api/sessions/{id}/files/watch    - Stop watching folder
func RegisterRoutes(mux *http.ServeMux, h *Handler) {
	// Session files pattern - handles all /api/sessions/{id}/files/* routes
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Only handle file-related routes
		if !strings.Contains(path, "/files") && !strings.Contains(path, "/folder") {
			http.NotFound(w, r)
			return
		}

		// Route based on path pattern
		switch {
		// Upload file
		case strings.HasSuffix(path, "/files/upload"):
			h.UploadFile(w, r)

		// Link file
		case strings.HasSuffix(path, "/files/link"):
			h.LinkFile(w, r)

		// Validate links
		case strings.HasSuffix(path, "/files/validate"):
			h.ValidateLinks(w, r)

		// SSE events
		case strings.HasSuffix(path, "/files/events"):
			h.FileEvents(w, r)

		// Watch control
		case strings.HasSuffix(path, "/files/watch"):
			if r.Method == http.MethodPost {
				h.StartWatching(w, r)
			} else if r.Method == http.MethodDelete {
				h.StopWatching(w, r)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}

		// Open folder
		case strings.HasSuffix(path, "/folder/open"):
			h.OpenFolder(w, r)

		// Download file
		case strings.Contains(path, "/files/") && strings.HasSuffix(path, "/download"):
			h.DownloadFile(w, r)

		// Relink file
		case strings.Contains(path, "/files/") && strings.HasSuffix(path, "/relink"):
			h.RelinkFile(w, r)

		// List files (exact match for /files or /files/)
		case strings.HasSuffix(path, "/files") || strings.HasSuffix(path, "/files/"):
			h.ListFiles(w, r)

		// Get or delete specific file
		case strings.Contains(path, "/files/"):
			if r.Method == http.MethodGet {
				h.GetFile(w, r)
			} else if r.Method == http.MethodDelete {
				h.DeleteFile(w, r)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}

		default:
			http.NotFound(w, r)
		}
	})
}
