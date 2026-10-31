package sessionhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// anchorPathError is also the read-only chooser check contract. A duplicate
// names the existing workspace so clients can offer Open it instead of Create.
type anchorPathError struct {
	Code      string                         `json:"code"`
	Message   string                         `json:"error"`
	Duplicate *projectconnection.FolderOwner `json:"duplicate,omitempty"`
	status    int
}

func (e *anchorPathError) Error() string { return e.Message }

func anchorError(code, message string, status int) *anchorPathError {
	return &anchorPathError{Code: code, Message: message, status: status}
}

func respondAnchorError(w http.ResponseWriter, err *anchorPathError) {
	_ = orihttp.RespondJSON(w, err.status, err)
}

func (h *Handler) handleWorkspaceAnchorCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	path, err := h.validateAnchorPath(r.URL.Query().Get("path"), "")
	if err != nil {
		respondAnchorError(w, err)
		return
	}
	orihttp.WriteJSON(w, map[string]any{"success": true, "normalized_path": path})
}

func (h *Handler) validateAnchorPath(input, ignoreID string) (string, *anchorPathError) {
	input = strings.TrimSpace(input)
	if input == "~" || strings.HasPrefix(input, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", anchorError("anchor_invalid_path", "The home folder could not be resolved.", http.StatusBadRequest)
		}
		input = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(input, "~"), "/"))
	}
	abs, err := filepath.Abs(input)
	if err != nil || strings.TrimSpace(input) == "" {
		return "", anchorError("anchor_invalid_path", "Choose an existing directory for the anchor.", http.StatusBadRequest)
	}
	info, err := os.Stat(abs) // #nosec G703 -- user-selected local folder is inspected, never written here.
	if err != nil || !info.IsDir() {
		return "", anchorError("anchor_not_directory", "The anchor folder does not exist, is inaccessible, or is not a directory.", http.StatusBadRequest)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(abs) {
		return "", anchorError("anchor_symlink", "Choose the actual folder, not a symlink, as the anchor.", http.StatusBadRequest)
	}
	path, err := normalizeImportPath(abs)
	if err != nil {
		return "", anchorError("anchor_invalid_path", "The anchor path could not be normalized.", http.StatusBadRequest)
	}
	if h.workspaceStore == nil {
		return "", anchorError("anchor_unavailable", "Workspace storage is unavailable; no anchored workspace was created.", http.StatusServiceUnavailable)
	}
	roots := []string{h.workspaceStore.BasePath()}
	if h.workspaceRootResolver != nil {
		roots = append(roots, h.workspaceRootResolver())
	}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		normalized, normalizeErr := normalizeImportPath(root)
		if normalizeErr != nil {
			return "", anchorError("anchor_unavailable", "The Workspace Directory could not be checked.", http.StatusServiceUnavailable)
		}
		if _, inside := rewriteWorkspaceContentPath(path, normalized, normalized); inside {
			return "", anchorError("anchor_inside_workspace_root", "Choose a folder outside the Workspace Directory; folders inside it are managed workspaces.", http.StatusBadRequest)
		}
	}
	configPath := filepath.Join(path, agentworkspace.WorkspaceConfigFile)
	if _, statErr := os.Lstat(configPath); statErr == nil {
		var existing struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		// Read identity only, never import or execute portable state.
		if data, readErr := os.ReadFile(configPath); readErr == nil { // #nosec G304 G703 -- selected local workspace.json, identity only.
			_ = json.Unmarshal(data, &existing)
		}
		message := "This folder already contains workspace.json. Use Import Folder instead."
		if existing.Name != "" {
			message = fmt.Sprintf("This folder contains workspace %q. Use Import Folder instead.", existing.Name)
		}
		conflict := anchorError("anchor_contains_workspace", message, http.StatusConflict)
		if existing.ID != "" {
			conflict.Duplicate = &projectconnection.FolderOwner{WorkspaceID: existing.ID, Name: existing.Name}
		}
		return "", conflict
	} else if !os.IsNotExist(statErr) {
		return "", anchorError("anchor_unavailable", "The folder's workspace identity could not be checked.", http.StatusServiceUnavailable)
	}
	owner, err := projectconnection.FindFolderOwner(h.workspaceStore, path, ignoreID)
	if err != nil {
		return "", anchorError("anchor_unavailable", "Folder ownership could not be checked. Try again before creating.", http.StatusServiceUnavailable)
	}
	if owner != nil {
		conflict := anchorError("anchor_already_owned", fmt.Sprintf("This folder already belongs to the workspace %q.", owner.Name), http.StatusConflict)
		conflict.Duplicate = owner
		return "", conflict
	}
	return path, nil
}

// plannedAnchorSidecarPath is disclosure only; creation still resolves and
// validates the real destination through FileStore before any folder write.
func (h *Handler) plannedAnchorSidecarPath(req createWorkspaceRequest) string {
	if h.workspaceStore == nil {
		return ""
	}
	root := h.workspaceStore.BasePath()
	if h.workspaceRootResolver != nil && strings.TrimSpace(h.workspaceRootResolver()) != "" {
		root = h.workspaceRootResolver()
	}
	if req.ParentID != "" {
		parent, err := h.workspaceStore.GetFolderPath(req.ParentID)
		if err != nil {
			return ""
		}
		root = filepath.Join(parent, agentworkspace.SubWorkspacesDir)
	}
	return filepath.Join(root, agentworkspace.Slugify(firstNonEmptyString(req.FolderSlug, req.Name)))
}

// Serialize the final owner check and durable anchor write with all existing
// project attaches. A second concurrent create must see the first one's anchor.
func (h *Handler) saveAnchoredWorkspaceFolder(path string, ws *agentworkspace.Workspace, location string) *anchorPathError {
	unlock := projectconnection.LockAttachCommit()
	defer unlock()
	if _, err := h.validateAnchorPath(path, ws.ID); err != nil {
		return err
	}
	var err error
	if location != "" && !workspacePathsEqual(location, h.workspaceStore.BasePath()) {
		err = h.workspaceStore.SaveAt(ws, location)
	} else {
		err = h.workspaceStore.Save(ws)
	}
	if err != nil {
		return anchorError("anchor_sidecar_failed", "Ori could not create the sidecar folder: "+err.Error(), http.StatusConflict)
	}
	return nil
}

func workspaceAnchorDirectoryID(ws *session.Workspace) string {
	if ws == nil {
		return ""
	}
	return agentworkspace.AnchorIDFromSharedData(ws.SharedData)
}

// setWorkspaceAnchorDirectoryID is used only when explicitly creating an
// anchored workspace. Legacy workspaces must never acquire an anchor by inference.
func setWorkspaceAnchorDirectoryID(ws *session.Workspace, id string) {
	if ws == nil || strings.TrimSpace(id) == "" {
		return
	}
	if ws.SharedData == nil {
		ws.SharedData = make(map[string]any)
	}
	ws.SharedData[agentworkspace.AnchorDirectoryReferenceIDKey] = strings.TrimSpace(id)
	setWorkspacePrimaryDirectoryID(ws, id)
}
