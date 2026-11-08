package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type workspaceBuildHomeStaffingReviewer interface {
	ReviewHomeStaffing(context.Context, foldercontext.Target, string, string, string, string, personalassistant.FolderHomeStaffingInput) (personalassistant.FolderOfferView, error)
}

type buildHomeStaffingFenceKey struct{}
type buildHomeStaffingFence struct {
	id, observation, candidate, name string
	version                          int64
}

func (f buildHomeStaffingFence) matches(build *personalassistant.WorkspaceBuildSession) bool {
	return build != nil && build.ID == f.id && build.Version == f.version && build.Status == personalassistant.WorkspaceBuildOpen && build.Setup == nil && build.Folder != nil && build.Folder.Recognition != nil &&
		build.Folder.Observation.ID == f.observation && build.Folder.Recognition.CandidateID == f.candidate && strings.TrimSpace(build.Draft.Name) == f.name &&
		build.PlacementSource == nil && build.Preparation == nil && build.FolderRequestID == "" && build.LinkRequestID == "" && build.InferenceID == "" &&
		(build.Proposal == nil || (build.Proposal.Status != "pending" && build.Proposal.Status != "running"))
}

// Manual setup/review needs the existing relationship and durable owner, not a
// working ranking model. Actual selected models are checked by the setup host.
func (h *Handler) workspaceBuildSetupReadiness(ctx context.Context, userID string) buildReadiness {
	deps := h.workspaceBuild
	if deps == nil {
		return buildReadiness{reason: buildReasonAssistantNotReady}
	}
	projection, err := deps.Assistant(ctx, userID)
	if err != nil || projection == nil || (projection.State != personalassistant.APIStateActive && projection.State != personalassistant.APIStatePaused) {
		return buildReadiness{reason: buildReasonAssistantNotReady}
	}
	if _, err := deps.Store.Read(ctx, userID); err != nil {
		return buildReadiness{reason: buildReasonAssistantNotReady}
	}
	return buildReadiness{available: true, projection: projection}
}

func (h *Handler) postWorkspaceBuildHomeStaffing(w http.ResponseWriter, r *http.Request, id string, request buildSetupRequest) {
	var input personalassistant.FolderHomeStaffingInput
	decoder := json.NewDecoder(bytes.NewReader(request.HomeStaffing))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || request.PlanDigest != "" || request.EntryName != "" || request.Brief != "" {
		respondBuildSetupRefusal(w, http.StatusBadRequest, "staffing_input", "Choose a public Home staffing action, not setup execution or private source data.")
		return
	}
	switch input.Action {
	case "prepare_staffing", "recommend_staffing", "choose_staffing", "clear_staffing":
	default:
		respondBuildSetupRefusal(w, http.StatusBadRequest, "staffing_input", "Choose a Home staffing review action.")
		return
	}
	ctx, userID := r.Context(), h.workspaceBuildUserID(r.Context())
	readiness := h.workspaceBuildSetupReadiness(ctx, userID)
	if !readiness.available {
		respondBuildUnavailable(w, readiness.reason)
		return
	}
	deps := h.workspaceBuild
	reviewer, ok := deps.Folders.(workspaceBuildHomeStaffingReviewer)
	if !ok {
		respondBuildFolderError(w, errBuildFoldersOff)
		return
	}
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
	if current.Version != request.Version {
		respondBuildSetupRefusal(w, http.StatusConflict, "version", buildErrorText("version"))
		return
	}
	if current.Folder == nil || current.Folder.Recognition == nil || !current.Folder.Recognition.Recognized() {
		respondBuildSetupRefusal(w, http.StatusConflict, "no_plan", buildCopySetupUnavailable)
		return
	}
	fence := buildHomeStaffingFence{id: id, version: request.Version, observation: current.Folder.Observation.ID, candidate: current.Folder.Recognition.CandidateID, name: strings.TrimSpace(current.Draft.Name)}
	if !fence.matches(current) {
		respondBuildSetupRefusal(w, http.StatusConflict, "closed", buildErrorText("closed"))
		return
	}
	// The existing review owner checks this fence before and after unlocked
	// inference. No placement, conversation or build mutation lease spans it.
	ctx = context.WithValue(ctx, buildHomeStaffingFenceKey{}, fence)
	view, err := reviewer.ReviewHomeStaffing(ctx, buildFolderTarget(userID, readiness.projection, id), id, fence.observation, fence.candidate, fence.name, input)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, personalassistant.ErrValidation) {
			status = http.StatusBadRequest
		}
		h.respondBuildSession(r.Context(), w, status, current, map[string]any{"code": "staffing_review_changed", "error": "The Home team could not be reviewed. Refresh the current plan; your saved choices are kept."})
		return
	}
	updated, err := deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		build := doc.Session(id)
		if !fence.matches(build) || view.ID == "" || view.Status != personalassistant.FolderOfferPending || view.Setup != nil {
			return personalassistant.ErrConflict
		}
		build.Folder.Recognition.OfferID = view.ID
		build.InvalidateBuildInference(true)
		build.Touch(deps.Store.Now())
		return nil
	})
	if err != nil {
		respondBuildSetupRefusal(w, http.StatusConflict, "version", buildErrorText("version"))
		return
	}
	// The request fence is no longer current after our own version bump. Reads
	// use ordinary observation authority and never rank or execute.
	h.respondBuildSession(r.Context(), w, http.StatusOK, updated.Session(id), nil)
}
