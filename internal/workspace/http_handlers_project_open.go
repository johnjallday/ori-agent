package workspace

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// OpenWorkspaceProject handles POST /api/workspaces/:id/project/open.
//
// The endpoint accepts no path. It opens only the entry already persisted in
// workspace.json and is restricted to local peers because it causes a desktop
// side effect on the server machine.
func (h *HTTPHandler) OpenWorkspaceProject(w http.ResponseWriter, r *http.Request) {
	if !projectOpenRequestIsLoopback(r) {
		orihttp.Forbidden(w, "Opening a desktop project requires a local request")
		return
	}
	if requestBodyHasContent(r) {
		orihttp.BadRequest(w, "Project open does not accept a request body or file path")
		return
	}

	workspaceID := r.PathValue("workspaceID")
	if h.folderResolver == nil || h.folderWorkspaceResolver == nil {
		orihttp.ServiceUnavailable(w, "Workspace folder storage is unavailable")
		return
	}
	ws, err := h.folderWorkspaceResolver.GetFolderWorkspace(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	workspaceRoot, err := h.folderResolver.GetFolderPath(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace folder is unavailable: %v", err))
		return
	}
	resolved, err := ResolveProjectEntry(ws, workspaceRoot)
	if err != nil {
		if errors.Is(err, ErrProjectEntryUnavailable) {
			orihttp.NotFound(w, "Workspace has no available project entry to open")
		} else {
			orihttp.BadRequest(w, "Workspace project entry metadata is unsafe")
		}
		return
	}
	if h.openFile == nil {
		orihttp.ServiceUnavailable(w, "Operating-system file opening is unavailable")
		return
	}
	if err := h.openFile(resolved.AbsolutePath); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to open project entry: %v", err))
		return
	}

	logger.Info("Opened workspace project entry via OS", logger.Fields{
		"workspace_id": workspaceID,
		"entry_kind":   resolved.Locator.Kind,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"message":       "Project open request accepted",
		"workspace":     workspaceID,
		"path":          resolved.Locator.RelativePath,
		"relative_path": resolved.Locator.RelativePath,
	})
}

func requestBodyHasContent(r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil {
		return true
	}
	return len(data) > 0
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
