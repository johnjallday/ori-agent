package sessionhttp

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// Optional specialization, not an expansion of folder transfer or File Janitor
// preparation. The software owner still owns evidence and final setup.
type workspaceBuildSoftwareReviewer interface {
	ReviewSoftware(ctx context.Context, draft foldercontext.Target, sessionID, observationID, candidateID, workspaceName, requestID string, inspect bool) (personalassistant.FolderOfferView, error)
}

func (h *Handler) postWorkspaceBuildSoftwareReview(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	deps := h.workspaceBuild
	if deps == nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	reviewer, ok := deps.Folders.(workspaceBuildSoftwareReviewer)
	if !ok {
		respondBuildSetupRefusal(w, http.StatusConflict, "software_review_unavailable", "The software review owner is unavailable; nothing was accepted.")
		return
	}
	var req struct {
		Version int64  `json:"version"`
		Mode    string `json:"mode"`
	}
	if !decodeBuildBody(w, r, &req) {
		return
	}
	if req.Mode != "review" && req.Mode != "adjust" {
		respondBuildSetupRefusal(w, http.StatusBadRequest, "software_review_mode", "Choose explicit evidence review or a names-only adjustment.")
		return
	}
	userID := h.workspaceBuildUserID(ctx)
	readiness := h.workspaceBuildReadiness(ctx, userID)
	if !readiness.available {
		respondBuildUnavailable(w, readiness.reason)
		return
	}
	doc, err := deps.Store.Read(ctx, userID)
	if err != nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	current := doc.Session(id)
	if current == nil || current.Status != personalassistant.WorkspaceBuildOpen || current.Setup != nil || current.Folder == nil || !current.Folder.Recognition.Recognized() || current.Folder.Recognition.Shape != string(folderdigest.ShapeCode) || current.Preparation != nil || current.PlacementSource != nil || current.FolderRequestID != "" || current.LinkRequestID != "" || current.InferenceID != "" || (current.Proposal != nil && (current.Proposal.Status == "pending" || current.Proposal.Status == "running")) {
		respondBuildSetupRefusal(w, http.StatusConflict, "software_review_changed", "This build cannot review that software source. Nothing was accepted.")
		return
	}
	if req.Mode == "adjust" && current.Folder.Recognition.OfferID == "" {
		respondBuildSetupRefusal(w, http.StatusConflict, "software_review_required", "Review the software evidence before adjusting this plan.")
		return
	}
	if current.Version != req.Version {
		respondBuildSetupRefusal(w, http.StatusConflict, "version", buildErrorText("version"))
		return
	}
	view, err := reviewer.ReviewSoftware(ctx, buildFolderTarget(userID, readiness.projection, id), id, current.Folder.Observation.ID, current.Folder.Recognition.CandidateID, current.Draft.Name, uuid.NewString(), req.Mode == "review")
	if err != nil {
		logger.Info("Workspace build software review refused", logger.Fields{"session_id": id, "error": err})
		respondBuildSetupRefusal(w, http.StatusConflict, "software_review_refused", "The software owner could not review this selection. Re-pick or review its current evidence; nothing was accepted.")
		return
	}
	updated, err := deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		build := doc.Session(id)
		if build == nil || build.Status != personalassistant.WorkspaceBuildOpen || build.Version != req.Version || build.Setup != nil || build.Folder == nil || build.Folder.Observation.ID != current.Folder.Observation.ID {
			return errCreationBuildChanged
		}
		// This records only the pending review's owner reference. It does not
		// accept a Setup claim or grant permission to start its work.
		build.Folder.Recognition.OfferID = view.ID
		build.Touch(deps.Store.Now())
		return nil
	})
	if err != nil {
		respondBuildSetupRefusal(w, http.StatusConflict, "software_review_changed", "The build changed during review. Reopen its current review; nothing was accepted.")
		return
	}
	h.respondBuildSession(ctx, w, http.StatusOK, updated.Session(id), nil)
}
