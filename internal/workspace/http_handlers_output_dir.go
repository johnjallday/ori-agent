package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
)

// GetWorkspaceOutputDir handles GET /api/workspaces/:id/output-dir
// Returns the default output directory for a workspace
func (h *HTTPHandler) GetWorkspaceOutputDir(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Default output directory: the workspace's own folder (<workspace>/outputs/).
	outputDir := h.store.GetOutputsPath(workspaceID)
	if outputDir == "" {
		// Fallback to the global output directory if the workspace folder
		// can't be resolved.
		baseOutputDir, err := platform.GetDefaultOutputDir()
		if err != nil {
			baseOutputDir = "outputs"
			logger.Warn("Failed to get default output dir, using fallback", logger.Fields{"error": err})
		}
		outputDir = filepath.Join(baseOutputDir, workspace.Name)
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"output_dir":   outputDir,
		"workspace_id": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// OpenWorkspaceOutputDir handles POST /api/workspaces/:id/output-dir/open
//
// Opens the workspace's default outputs folder in the OS file manager
// (Finder on macOS, Explorer on Windows). Creates the directory if it
// doesn't exist yet so the user can drop files in immediately.
func (h *HTTPHandler) OpenWorkspaceOutputDir(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	outputDir := h.store.GetOutputsPath(workspaceID)
	if outputDir == "" {
		baseOutputDir, derr := platform.GetDefaultOutputDir()
		if derr != nil {
			baseOutputDir = "outputs"
			logger.Warn("Failed to get default output dir, using fallback", logger.Fields{"error": derr})
		}
		outputDir = filepath.Join(baseOutputDir, workspace.Name)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil { // #nosec G301 G703 -- outputDir comes from the workspace store's own outputs path (or the platform default output dir joined with the workspace's stored name), never from the request; 0755 matches the user-facing output folders elsewhere
		orihttp.InternalError(w, fmt.Sprintf("Failed to create output directory: %v", err))
		return
	}

	if err := callDesktopOpener(h.openFolder, outputDir); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to open output directory: %v", err))
		return
	}

	logger.Info("Opened workspace output directory", logger.Fields{
		"workspace_id": workspaceID,
		"path":         outputDir,
	})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"output_dir":   outputDir,
		"workspace_id": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
