package sessionhttp

import (
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Relink changes an existing anchor, never the shape of a managed workspace.
// Root validation and ownership serialization are identical to creation.
func (h *Handler) handleWorkspaceAnchorRelink(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPut {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	var req struct {
		Path         string `json:"path"`
		ReferenceID  string `json:"directory_reference_id"`
		ExpectedPath string `json:"expected_path"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	store := h.workspaceTaskStore
	if store == nil {
		store = h.workspaceStore
	}
	if store == nil || h.workspaceStore == nil {
		_ = orihttp.RespondInternalError(w, "Workspace storage is unavailable")
		return
	}
	unlock := projectconnection.LockAttachCommit()
	defer unlock()
	ws, err := store.Get(id)
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	ref, err := ws.GetDirectoryReference(req.ReferenceID)
	if err != nil || req.ReferenceID != ws.AnchorDirectoryReferenceID() || ref.Path != req.ExpectedPath {
		respondAnchorError(w, anchorError("anchor_changed", "The anchored folder changed. Reload before relinking.", http.StatusConflict))
		return
	}
	path, pathErr := h.validateAnchorPath(req.Path, id)
	if pathErr != nil {
		respondAnchorError(w, pathErr)
		return
	}
	if path == ref.Path {
		h.getWorkspace(w, r, id)
		return
	}
	owned := false
	if installed, ok := ws.GetInstalledCapability(workspace.CapabilityFileJanitor); ok {
		_, owned = installed.Owns(workspace.ResourceDirectoryReference, ref.ID)
	}
	if owned {
		// Use the capability's real lifecycle: stop watching, invalidate pending
		// approvals, retain history and require a fresh monitoring decision.
		if h.workspaceBuild == nil || h.workspaceBuild.FileJanitor == nil {
			_ = orihttp.RespondConflict(w, "File Janitor is unavailable; the anchor was not changed.")
			return
		}
		service, automation := h.workspaceBuild.FileJanitor()
		lifecycle, ok := automation.(filejanitor.WatcherLifecycle)
		if service == nil || (automation != nil && !ok) {
			_ = orihttp.RespondConflict(w, "File Janitor could not safely stop watching; the anchor was not changed.")
			return
		}
		_, err = service.Relink(lifecycle, filejanitor.RelinkRequest{WorkspaceID: id, Path: path, AnchorDirectoryReferenceID: ref.ID})
	} else {
		err = store.Update(id, func(current *workspace.Workspace) error {
			old, getErr := current.GetDirectoryReference(req.ReferenceID)
			if getErr != nil || current.AnchorDirectoryReferenceID() != req.ReferenceID || old.Path != req.ExpectedPath {
				return anchorError("anchor_changed", "The anchored folder changed. Reload before relinking.", http.StatusConflict)
			}
			next := *old
			next.Path, next.Name = path, filepath.Base(path)
			if err := current.UpdateDirectoryReference(next); err != nil {
				return err
			}
			for i := range current.MCPBindings {
				binding := &current.MCPBindings[i]
				if binding.ServerName != "filesystem" || binding.Alias != workspace.AnchorFolderMCPAlias {
					continue
				}
				// Only the creation-owned default follows the anchor. User-added
				// bindings keep their explicit roots, tools and enabled state.
				roots := workspaceMCPBindingRoots(*binding)
				if len(roots) != 1 || roots[0] != old.Path {
					continue
				}
				binding.Config["roots"] = []string{path}
				binding.UpdatedAt = time.Now()
			}
			return nil
		})
	}
	if err != nil {
		var anchorErr *anchorPathError
		if errors.As(err, &anchorErr) {
			respondAnchorError(w, anchorErr)
		} else {
			_ = orihttp.RespondConflict(w, err.Error())
		}
		return
	}
	h.getWorkspace(w, r, id)
}

func workspaceMCPBindingRoots(binding workspace.MCPBinding) []string {
	switch roots := binding.Config["roots"].(type) {
	case []string:
		return roots
	case []any:
		out := make([]string, 0, len(roots))
		for _, root := range roots {
			path, ok := root.(string)
			if !ok {
				return nil
			}
			out = append(out, path)
		}
		return out
	}
	return nil
}
