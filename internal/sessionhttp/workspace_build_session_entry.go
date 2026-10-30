package sessionhttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// postWorkspaceBuildEntry edits the current draft's canonical file choice. The
// creation lease is held by the router; the folder service checks its separate
// review owner. Neither choosing nor refreshing the returned plan starts a run.
func (h *Handler) postWorkspaceBuildEntry(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Version    int64  `json:"version"`
		PlanDigest string `json:"plan_digest"`
		EntryName  string `json:"entry_name"`
	}
	if !decodeStrictAction(w, r, &req, "File choice requires a version, plan_digest and entry_name") {
		return
	}
	deps := h.workspaceBuild
	if deps == nil || deps.Folders == nil || deps.Assistant == nil {
		respondBuildFolderError(w, errBuildFoldersOff)
		return
	}
	chooser, ok := deps.Folders.(interface {
		ChooseSetupEntry(context.Context, foldercontext.Target, string, string, string, string, personalassistant.FolderSetupEntryInput) (personalassistant.FolderOfferView, error)
	})
	if !ok {
		respondBuildFolderError(w, errBuildFoldersOff)
		return
	}
	ctx := r.Context()
	userID := h.workspaceBuildUserID(ctx)
	doc, err := deps.Store.Read(ctx, userID)
	if err != nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	current := doc.Session(id)
	if current == nil {
		respondBuildSetupRefusal(w, http.StatusNotFound, "not_found", buildErrorText("not_found"))
		return
	}
	if current.Status != personalassistant.WorkspaceBuildOpen || current.Setup != nil || current.PlacementSource != nil {
		respondBuildSetupRefusal(w, http.StatusConflict, "closed", buildErrorText("closed"))
		return
	}
	if current.Version != req.Version {
		respondBuildSetupRefusal(w, http.StatusConflict, "version", buildErrorText("version"))
		return
	}
	if current.Folder == nil || !current.Folder.Recognition.Recognized() {
		respondBuildSetupRefusal(w, http.StatusConflict, "no_plan", buildCopySetupUnavailable)
		return
	}
	projection, err := deps.Assistant(ctx, userID)
	if err != nil || projection == nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	plan, err := h.readBuildPlan(ctx, userID, projection, current)
	if err != nil || plan == nil || !plan.Ready || plan.Digest != req.PlanDigest {
		h.respondBuildSession(ctx, w, http.StatusConflict, current, map[string]any{"code": "plan_changed", "error": "Review the current file choice before continuing."})
		return
	}
	_, err = chooser.ChooseSetupEntry(ctx, buildFolderTarget(userID, projection, id), id, current.Folder.Observation.ID, current.Folder.Recognition.CandidateID, current.Draft.Name,
		personalassistant.FolderSetupEntryInput{PlanDigest: req.PlanDigest, EntryName: req.EntryName})
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, personalassistant.ErrValidation) {
			status = http.StatusBadRequest
		}
		h.respondBuildSession(ctx, w, status, current, map[string]any{"code": "plan_changed", "error": "That file choice could not be saved. Review the current plan."})
		return
	}
	updated, err := deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		session := doc.Session(id)
		if session == nil || session.Version != req.Version || session.Status != personalassistant.WorkspaceBuildOpen || session.Setup != nil ||
			session.Folder == nil || session.Folder.Observation.ID != current.Folder.Observation.ID {
			return personalassistant.ErrConflict
		}
		session.InvalidateBuildInference(true)
		session.Touch(deps.Store.Now())
		return nil
	})
	if err != nil {
		respondBuildSetupRefusal(w, http.StatusConflict, "version", buildErrorText("version"))
		return
	}
	h.respondBuildSession(ctx, w, http.StatusOK, updated.Session(id), nil)
}
