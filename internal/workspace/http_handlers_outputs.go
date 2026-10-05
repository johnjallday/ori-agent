package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// workspaceOutputSource marks a listed entry as living under the workspace's
// outputs/ folder, as workspaceFileSource does for files/.
const workspaceOutputSource = "workspace_output"

// workspaceOutputPolicy is sent with every output file. A file a task run
// wrote is not Ori's own page: opened in a browser tab it may load nothing and
// run nothing. An <img> pointing at it still draws, since a policy on the
// image's own response does not bind the page that embeds it.
const workspaceOutputPolicy = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// GetWorkspaceOutputsTree handles GET /api/workspaces/{workspaceID}/outputs/tree.
//
// It lists what task runs have saved under the workspace's outputs/ folder, in
// the flat shape /files/tree answers with. Outputs are plain files, so there
// is no attachment metadata to reconcile and nothing is written: a workspace
// that has never produced an output answers with an empty list, and the folder
// is not created.
func (h *HTTPHandler) GetWorkspaceOutputsTree(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")

	if _, err := h.store.Get(workspaceID); err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	files, err := buildWorkspaceOutputsTree(workspaceID, h.store.GetOutputsPath(workspaceID))
	if err != nil {
		logger.Warn("Failed to list workspace outputs", logger.Fields{
			"workspace_id": workspaceID,
			"error":        err,
		})
		orihttp.InternalError(w, "Failed to list outputs")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"files":     files,
		"workspace": workspaceID,
	})
}

// buildWorkspaceOutputsTree walks outputsPath the way buildWorkspaceFileTree
// walks files/: symbolic links are never followed or listed, and anything
// isHiddenWorkspacePath hides stays hidden. A missing folder is an empty list.
func buildWorkspaceOutputsTree(workspaceID, outputsPath string) ([]FileInfo, error) {
	files := []FileInfo{}
	if strings.TrimSpace(outputsPath) == "" {
		return files, nil
	}

	// outputsPath is the store's own folder for a workspace id it has already
	// resolved; no part of it is taken from the request.
	err := filepath.WalkDir(outputsPath, func(path string, entry fs.DirEntry, walkErr error) error { // #nosec G703
		if walkErr != nil {
			// The folder itself failing is the listing failing. A sub-folder
			// that cannot be read is listed without its contents.
			if path == outputsPath {
				return walkErr
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(outputsPath, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		clean := sanitizeWorkspaceRelativePath(rel)
		if clean == "" || isHiddenWorkspacePath(clean) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			// Removed between the directory read and now.
			return nil
		}
		item := FileInfo{
			ID:           "file:" + clean,
			Source:       workspaceOutputSource,
			Name:         info.Name(),
			RelativePath: clean,
			Size:         info.Size(),
			IsDir:        info.IsDir(),
			ModTime:      info.ModTime(),
		}
		if item.IsDir {
			item.ID = "folder:" + clean
		} else {
			item.URL = workspaceOutputURL(workspaceID, clean)
		}
		files = append(files, item)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	sortWorkspaceFileInfos(files)
	return files, nil
}

func workspaceOutputURL(workspaceID string, relativePath string) string {
	return fmt.Sprintf("/api/workspaces/%s/outputs/%s", workspaceID, escapeWorkspaceRelativeURLPath(relativePath))
}

// ServeWorkspaceOutputFile handles GET /api/workspaces/{workspaceID}/outputs/{relativePath...}.
//
// It serves one regular file from the workspace's outputs/ folder. Everything
// else is refused before any file is opened: a path that is absolute or has a
// ".." step, one a symbolic link carries out of the folder, a hidden file, and
// a directory.
func (h *HTTPHandler) ServeWorkspaceOutputFile(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	// ServeMux has already decoded the wildcard. Decoding it a second time
	// would turn a literal "%2e" in a file name into a dot.
	relativePath := r.PathValue("relativePath")

	if _, err := h.store.Get(workspaceID); err != nil {
		orihttp.NotFound(w, "Workspace not found")
		return
	}
	if !isPlainRelativeOutputPath(relativePath) {
		orihttp.BadRequest(w, "Invalid file path")
		return
	}
	// A hidden file answers exactly as a missing one does, so the endpoint
	// never says whether one exists.
	if isHiddenWorkspacePath(relativePath) {
		orihttp.NotFound(w, "File not found")
		return
	}

	outputsPath := h.store.GetOutputsPath(workspaceID)
	root, err := os.OpenRoot(outputsPath)
	if err != nil {
		// No outputs folder yet: no file in it either.
		orihttp.NotFound(w, "File not found")
		return
	}
	defer func() { _ = root.Close() }()

	// The same containment check ServeFile makes for files/: the path must
	// stay inside the folder once symbolic links are resolved.
	_, clean, err := workspaceFilePathWithinRoot(outputsPath, relativePath)
	if err != nil {
		orihttp.BadRequest(w, "Invalid file path")
		return
	}

	// Opened through the root, so a link swapped in after the check above
	// still cannot lead outside the folder.
	file, err := root.Open(clean)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			orihttp.NotFound(w, "File not found")
		case errors.Is(err, fs.ErrPermission):
			orihttp.Forbidden(w, "File cannot be read")
		default:
			orihttp.BadRequest(w, "Invalid file path")
		}
		return
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		orihttp.NotFound(w, "File not found")
		return
	}

	w.Header().Set("Content-Type", workspaceOutputContentType(clean))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", workspaceOutputPolicy)
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

// isPlainRelativeOutputPath reports whether a requested path names something
// under the folder in the plain way: not empty, not absolute, and with no ".."
// step anywhere — not even one that would cancel out, as in "a/../b".
func isPlainRelativeOutputPath(relativePath string) bool {
	if strings.TrimSpace(relativePath) == "" || strings.ContainsRune(relativePath, 0) {
		return false
	}
	slashed := filepath.ToSlash(relativePath)
	if strings.HasPrefix(slashed, "/") || filepath.IsAbs(relativePath) || filepath.VolumeName(relativePath) != "" {
		return false
	}
	for _, part := range strings.Split(slashed, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// workspaceOutputContentType picks the type an output file is served with:
// ServeFile's detection by extension, except that nothing a browser would run
// keeps its type. HTML, XML and scripts go out as plain text, which is how the
// Home pane shows them. An SVG keeps its image type so an <img> can draw it;
// workspaceOutputPolicy stops its scripts when it is opened directly.
func workspaceOutputContentType(name string) string {
	contentType := detectMimeType(name)
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "application/octet-stream"
	}
	if mediaType == "image/svg+xml" {
		return mediaType
	}
	if strings.HasPrefix(mediaType, "text/") ||
		strings.HasSuffix(mediaType, "/xml") ||
		strings.HasSuffix(mediaType, "+xml") ||
		strings.Contains(mediaType, "javascript") ||
		strings.Contains(mediaType, "ecmascript") {
		return "text/plain; charset=utf-8"
	}
	return contentType
}
