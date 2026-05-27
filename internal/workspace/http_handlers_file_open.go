package workspace

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
)

type openWorkspaceFileRequest struct {
	RelativePath string `json:"relative_path"`
}

// OpenWorkspaceFile handles POST /api/workspaces/:id/files/open
//
// It opens a workspace-owned file in the OS default application. This only does
// something useful when the server runs on the same machine as the user
// (local-first); browsers should keep "Open raw" as the remote-safe fallback.
func (h *HTTPHandler) OpenWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	h.osOpenWorkspaceFile(w, r, false)
}

// RevealWorkspaceFile handles POST /api/workspaces/:id/files/reveal
//
// It reveals a workspace-owned file in the OS file manager (Finder/Explorer).
// When the file itself is missing, the nearest existing ancestor folder is
// opened instead, so the user can drop the file back in (the "missing →
// locate" flow).
func (h *HTTPHandler) RevealWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	h.osOpenWorkspaceFile(w, r, true)
}

func (h *HTTPHandler) osOpenWorkspaceFile(w http.ResponseWriter, r *http.Request, reveal bool) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	workspaceID, ok := workspaceIDFromWorkspacePath(w, r)
	if !ok {
		return
	}
	var req openWorkspaceFileRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if _, err := h.store.Get(workspaceID); err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	filesPath := h.store.GetFilesPath(workspaceID)
	absPath, cleanRel, err := workspaceFilePathWithinRoot(filesPath, req.RelativePath)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	info, statErr := os.Stat(absPath)
	switch {
	case statErr == nil && info.IsDir():
		err = platform.OpenFolder(absPath)
	case statErr == nil:
		if reveal {
			err = platform.RevealInFileManager(absPath)
		} else {
			err = platform.OpenFile(absPath)
		}
	case os.IsNotExist(statErr) && reveal:
		// Missing file: fall back to revealing the nearest existing ancestor
		// folder within the workspace so the user can restore the file.
		ancestor := nearestExistingDir(filepath.Dir(absPath), filesPath)
		if ancestor == "" {
			orihttp.NotFound(w, "File not found")
			return
		}
		err = platform.OpenFolder(ancestor)
	case os.IsNotExist(statErr):
		orihttp.NotFound(w, "File not found")
		return
	default:
		orihttp.InternalError(w, fmt.Sprintf("Failed to inspect file: %v", statErr))
		return
	}

	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to open file: %v", err))
		return
	}

	logger.Info("Opened workspace file via OS", logger.Fields{
		"workspace_id": workspaceID,
		"path":         cleanRel,
		"reveal":       reveal,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"message":   "ok",
		"workspace": workspaceID,
		"path":      cleanRel,
	})
}

// nearestExistingDir walks up from dir until it finds an existing directory,
// bounded by root. Returns "" if neither dir's ancestors nor root exist.
func nearestExistingDir(dir, root string) string {
	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		if dir == root || !isPathWithin(dir, root) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}
