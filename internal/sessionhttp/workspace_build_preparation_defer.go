package sessionhttp

import (
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// deferWorkspaceBuildPreparation keeps the accepted identity as inert bounded
// evidence, but lets the owner start a separate build. It never drops a claim
// to make an uncertain create retryable, recreates a resource, or deletes one.
func (h *Handler) deferWorkspaceBuildPreparation(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	var request buildPreparationRequest
	if !decodeBuildBody(w, r, &request) {
		return
	}
	owner, err := h.buildPreparationOwner(r.Context())
	if err != nil || h.workspaceBuild == nil {
		respondBuildPreparationError(w, "build_unavailable", "This build cannot be deferred right now.", http.StatusServiceUnavailable, nil)
		return
	}
	h.buildPreparationMu.Lock()
	defer h.buildPreparationMu.Unlock()
	doc, err := h.workspaceBuild.Store.Read(r.Context(), owner)
	if err != nil {
		respondBuildPreparationError(w, "build_unavailable", "The saved build cannot be read. Nothing was changed.", http.StatusServiceUnavailable, nil)
		return
	}
	build := doc.Session(id)
	if build == nil || build.Preparation == nil {
		respondBuildPreparationError(w, "not_found", "Accepted preparation not found.", http.StatusNotFound, nil)
		return
	}
	claim := build.Preparation
	if request.Version != claim.BuildVersion || request.ReviewRevision != claim.ReviewRevision || request.Monitoring != claim.Monitoring {
		respondBuildPreparationError(w, "review_changed", "This accepted preparation changed. Open its current review.", http.StatusConflict, nil)
		return
	}
	if build.Status != personalassistant.WorkspaceBuildAbandoned && claim.LinkedReviewRevision != "" && h.workspaceTaskStore != nil {
		ws, workspaceErr := h.workspaceTaskStore.Get(claim.WorkspaceID)
		if workspaceErr == nil && ws != nil {
			workspaceOwner := strings.TrimSpace(ws.OwnerUserID)
			if workspaceOwner == "" {
				workspaceOwner = userprofile.LocalUserID
			}
			// A transferred/removed workspace cannot be modified through the old
			// build. Closing the owner's inert claim remains safe in that case.
			if workspaceOwner == owner {
				if h.workspaceBuild.FileJanitor == nil {
					respondBuildPreparationError(w, "preparation_unavailable", "Open the workspace to pause its preparation before deferring this build.", http.StatusServiceUnavailable, nil)
					return
				}
				janitor, automation := h.workspaceBuild.FileJanitor()
				if janitor == nil {
					respondBuildPreparationError(w, "preparation_unavailable", "Open the workspace to pause its preparation before deferring this build.", http.StatusServiceUnavailable, nil)
					return
				}
				status, err := janitor.Status(claim.WorkspaceID)
				if err != nil {
					respondBuildPreparationError(w, "setup_incomplete", "The saved setup cannot be read. Open the workspace to repair it before deferring.", http.StatusConflict, nil)
					return
				}
				if preparation := status.Settings.SetupPreparation; preparation != nil && preparation.OwnerUserID == owner && preparation.ReviewRevision == claim.LinkedReviewRevision {
					if status, err = janitor.StopLinkedPreparation(ws.ID, owner, preparation.ID); err != nil {
						respondBuildPreparationError(w, "setup_incomplete", "The accepted preparation could not be stopped. Open the workspace's current settings.", http.StatusConflict, nil)
						return
					}
				}
				if status.Settings.Paused && automation != nil {
					if err := automation.EnsureWatcher(ws.ID); err != nil {
						respondBuildPreparationError(w, "setup_incomplete", "The saved pause could not be applied to monitoring. Retry this defer action or open current settings.", http.StatusConflict, nil)
						return
					}
				}
			}
		}
	}
	_, err = h.workspaceBuild.Store.Mutate(r.Context(), owner, func(doc *personalassistant.WorkspaceBuildDocument) error {
		current := doc.Session(id)
		if current == nil || current.Preparation == nil || current.Preparation.ID != claim.ID {
			return errors.New("accepted identity changed")
		}
		current.Status = personalassistant.WorkspaceBuildAbandoned
		current.CreateNow, current.Ready = false, false
		current.Settle() // Keeps Preparation and exact reserved workspace identity.
		current.Touch(h.workspaceBuild.Store.Now())
		return nil
	})
	if err != nil {
		respondBuildPreparationError(w, "build_unavailable", "Preparation stopped, but its deferred receipt could not be saved. Retry this same defer action.", http.StatusServiceUnavailable, nil)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "deferred": true, "workspace_id": claim.WorkspaceID})
}
