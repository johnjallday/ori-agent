package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

var errProjectEntryTargetMissing = errors.New("project entry target is missing")

// OpenWorkspaceProject handles POST /api/workspaces/:id/project/open.
//
// The endpoint accepts no path. It opens only the entry already persisted in
// workspace.json and is restricted to local peers because it causes a desktop
// side effect on the server machine.
func (h *HTTPHandler) OpenWorkspaceProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	if !projectOpenRequestIsLoopback(r) {
		orihttp.Forbidden(w, "Opening a desktop project requires a local request")
		return
	}
	if requestBodyHasContent(r) {
		orihttp.BadRequest(w, "Project open does not accept a request body or file path")
		return
	}

	workspaceID, ok := workspaceIDFromWorkspacePath(w, r)
	if !ok {
		return
	}
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	projectPath, err := validatePersistedProjectPath(ws.ProjectPath)
	if err != nil {
		if strings.TrimSpace(ws.ProjectPath) == "" {
			orihttp.NotFound(w, "Workspace has no project to open")
		} else {
			orihttp.BadRequest(w, fmt.Sprintf("Workspace project metadata is invalid: %v", err))
		}
		return
	}
	entryPath, err := GetProjectEntryPath(ws.SharedData)
	if err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Workspace project entry metadata is invalid: %v", err))
		return
	}
	if entryPath == "" {
		orihttp.NotFound(w, "Workspace has no project entry to open")
		return
	}
	if h.folderResolver == nil {
		orihttp.ServiceUnavailable(w, "Workspace folder storage is unavailable")
		return
	}

	workspaceRoot, err := h.folderResolver.GetFolderPath(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace folder is unavailable: %v", err))
		return
	}
	target, err := verifiedProjectEntryTarget(workspaceRoot, projectPath, entryPath)
	if err != nil {
		if errors.Is(err, errProjectEntryTargetMissing) {
			orihttp.NotFound(w, "Project entry file was not found")
		} else {
			orihttp.BadRequest(w, fmt.Sprintf("Project entry cannot be opened safely: %v", err))
		}
		return
	}
	if h.openFile == nil {
		orihttp.ServiceUnavailable(w, "Operating-system file opening is unavailable")
		return
	}
	if err := h.openFile(target); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to open project entry: %v", err))
		return
	}

	logger.Info("Opened workspace project entry via OS", logger.Fields{
		"workspace_id": workspaceID,
		"project_path": projectPath,
		"entry_path":   entryPath,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"message":   "Project open request accepted",
		"workspace": workspaceID,
		"path":      entryPath,
	})
}

func requestBodyHasContent(r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1025))
	if err != nil {
		return true
	}
	return len(bytes.TrimSpace(data)) > 0
}

func projectOpenRequestIsLoopback(r *http.Request) bool {
	peer, ok := parseProjectOpenIP(r.RemoteAddr)
	if !ok || !peer.IsLoopback() {
		return false
	}

	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		for _, value := range r.Header.Values(header) {
			for _, candidate := range strings.Split(value, ",") {
				ip, ok := parseProjectOpenIP(candidate)
				if !ok || !ip.IsLoopback() {
					return false
				}
			}
		}
	}
	for _, forwarded := range r.Header.Values("Forwarded") {
		for _, element := range strings.Split(forwarded, ",") {
			for _, directive := range strings.Split(element, ";") {
				key, value, found := strings.Cut(strings.TrimSpace(directive), "=")
				if !found || !strings.EqualFold(key, "for") {
					continue
				}
				ip, ok := parseProjectOpenIP(strings.Trim(value, `"`))
				if !ok || !ip.IsLoopback() {
					return false
				}
			}
		}
	}
	return true
}

func parseProjectOpenIP(value string) (net.IP, bool) {
	value = strings.TrimSpace(strings.Trim(value, `"`))
	if value == "" || strings.EqualFold(value, "unknown") || strings.HasPrefix(value, "_") {
		return nil, false
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.TrimPrefix(strings.TrimSuffix(value, "]"), "[")
	ip := net.ParseIP(value)
	return ip, ip != nil
}

func validatePersistedProjectPath(value string) (string, error) {
	return validateResolvedProjectEntryPath(value)
}

func verifiedProjectEntryTarget(workspaceRoot, projectPath, entryPath string) (string, error) {
	root, err := filepath.Abs(filepath.Clean(workspaceRoot))
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace root: %w", err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errProjectEntryTargetMissing
		}
		return "", fmt.Errorf("failed to inspect workspace root: %w", err)
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("workspace root is not a directory")
	}

	projectRoot, err := inspectProjectRelativePath(root, projectPath, true)
	if err != nil {
		return "", err
	}
	target, err := inspectProjectRelativePath(projectRoot, entryPath, false)
	if err != nil {
		return "", err
	}
	if !pathWithinRootAfterSymlinks(projectRoot, root) ||
		!pathWithinRootAfterSymlinks(target, projectRoot) {
		return "", fmt.Errorf("resolved path escapes the workspace project")
	}
	return target, nil
}

func inspectProjectRelativePath(root, portablePath string, wantDirectory bool) (string, error) {
	current := filepath.Clean(root)
	segments := strings.Split(portablePath, "/")
	for index, segment := range segments {
		current = filepath.Join(current, filepath.FromSlash(segment))
		if !isPathWithin(current, root) || current == root {
			return "", fmt.Errorf("path escapes its allowed root")
		}
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return "", errProjectEntryTargetMissing
			}
			return "", fmt.Errorf("failed to inspect path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path contains a symlink")
		}
		last := index == len(segments)-1
		if !last && !info.IsDir() {
			return "", fmt.Errorf("path has a non-directory parent")
		}
		if last && wantDirectory && !info.IsDir() {
			return "", fmt.Errorf("project_path is not a directory")
		}
		if last && !wantDirectory && !info.Mode().IsRegular() {
			return "", fmt.Errorf("project entry is not a regular file")
		}
	}
	return current, nil
}
