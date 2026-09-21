package personalassistanthttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type assistantSetupAcceptRequest struct {
	ProposalRevision  string `json:"proposal_revision"`
	TargetWorkspaceID string `json:"target_workspace_id,omitempty"`
}

type assistantSetupProposalRequest struct {
	ProposalRevision string `json:"proposal_revision"`
}

type assistantSetupRunRequest struct {
	IfVersion int64 `json:"if_version"`
}

type assistantSetupPrepareReviewRequest struct {
	IfVersion      int64  `json:"if_version"`
	ReviewRevision string `json:"review_revision"`
}

// GetAssistantSetup handles the read-only File Janitor recommendation/status.
func (h *Handler) GetAssistantSetup(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.assistantSetup == nil || h.provider == nil {
		writeAssistantSetupError(w, http.StatusServiceUnavailable, "assistant_setup_unavailable", "Assistant setup is temporarily unavailable.", false, nil)
		return
	}
	if err := validateAssistantSetupQuery(r); err != nil {
		writeAssistantSetupError(w, http.StatusBadRequest, "invalid_request", "Check the setup request and try again.", false, nil)
		return
	}
	owner, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	projection, err := h.assistantSetup.Get(r.Context(), owner, r.URL.Query().Get("target_workspace_id"))
	if err != nil {
		writeAssistantSetupServiceError(w, err, projection)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"setup": projection})
}

func (h *Handler) AcceptAssistantSetup(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.assistantSetup == nil || h.provider == nil {
		writeAssistantSetupError(w, http.StatusServiceUnavailable, "assistant_setup_unavailable", "Assistant setup is temporarily unavailable.", false, nil)
		return
	}
	var request assistantSetupAcceptRequest
	if decodeBoundedRequest(w, r, &request) != nil || !validAssistantSetupOpaque(request.ProposalRevision) || len(request.TargetWorkspaceID) > 128 {
		writeAssistantSetupError(w, http.StatusBadRequest, "invalid_request", "Check the reviewed setup and try again.", false, nil)
		return
	}
	owner, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	projection, created, err := h.assistantSetup.Accept(r.Context(), owner, request.ProposalRevision, request.TargetWorkspaceID)
	if err != nil {
		writeAssistantSetupServiceError(w, err, projection)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	_ = orihttp.RespondJSON(w, status, map[string]any{"setup": projection})
}

func (h *Handler) DeferAssistantSetupRecommendation(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.assistantSetup == nil || h.provider == nil {
		writeAssistantSetupError(w, http.StatusServiceUnavailable, "assistant_setup_unavailable", "Assistant setup is temporarily unavailable.", false, nil)
		return
	}
	var request assistantSetupProposalRequest
	if decodeBoundedRequest(w, r, &request) != nil || !validAssistantSetupOpaque(request.ProposalRevision) {
		writeAssistantSetupError(w, http.StatusBadRequest, "invalid_request", "Check the recommendation and try again.", false, nil)
		return
	}
	owner, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	projection, err := h.assistantSetup.DeferRecommendation(r.Context(), owner, request.ProposalRevision)
	if err != nil {
		writeAssistantSetupServiceError(w, err, projection)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"setup": projection})
}

func (h *Handler) BeginAssistantSetupFolderIntent(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.assistantSetup == nil || h.provider == nil {
		writeAssistantSetupError(w, http.StatusServiceUnavailable, "assistant_setup_unavailable", "Assistant setup is temporarily unavailable.", false, nil)
		return
	}
	runID := strings.TrimSpace(r.PathValue("runID"))
	var request assistantSetupRunRequest
	if decodeBoundedRequest(w, r, &request) != nil || len(runID) > 128 || runID == "" || request.IfVersion < 1 {
		writeAssistantSetupError(w, http.StatusBadRequest, "invalid_request", "Check the saved setup version and try again.", false, nil)
		return
	}
	owner, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	projection, token, err := h.assistantSetup.BeginFolderIntent(r.Context(), owner, runID, request.IfVersion)
	if err != nil {
		writeAssistantSetupServiceError(w, err, projection)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"setup": projection, "assistant_setup_token": token})
}

func (h *Handler) PrepareAssistantSetupReview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.assistantSetup == nil || h.provider == nil {
		writeAssistantSetupError(w, http.StatusServiceUnavailable, "assistant_setup_unavailable", "Assistant setup is temporarily unavailable.", false, nil)
		return
	}
	runID := strings.TrimSpace(r.PathValue("runID"))
	var request assistantSetupPrepareReviewRequest
	if decodeBoundedRequest(w, r, &request) != nil || len(runID) > 128 || runID == "" ||
		request.IfVersion < 1 || !validAssistantSetupOpaque(request.ReviewRevision) {
		writeAssistantSetupError(w, http.StatusBadRequest, "invalid_request", "Check the monitoring review and try again.", false, nil)
		return
	}
	owner, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	projection, err := h.assistantSetup.PrepareReview(r.Context(), owner, runID, request.IfVersion, request.ReviewRevision)
	if err != nil {
		writeAssistantSetupServiceError(w, err, projection)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"setup": projection})
}

func (h *Handler) DeferAssistantSetupRun(w http.ResponseWriter, r *http.Request) {
	h.mutateAssistantSetupRun(w, r, false)
}

func (h *Handler) ResumeAssistantSetupRun(w http.ResponseWriter, r *http.Request) {
	h.mutateAssistantSetupRun(w, r, true)
}

func (h *Handler) mutateAssistantSetupRun(w http.ResponseWriter, r *http.Request, resume bool) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.assistantSetup == nil || h.provider == nil {
		writeAssistantSetupError(w, http.StatusServiceUnavailable, "assistant_setup_unavailable", "Assistant setup is temporarily unavailable.", false, nil)
		return
	}
	runID := strings.TrimSpace(r.PathValue("runID"))
	var request assistantSetupRunRequest
	if decodeBoundedRequest(w, r, &request) != nil || len(runID) > 128 || runID == "" || request.IfVersion < 1 {
		writeAssistantSetupError(w, http.StatusBadRequest, "invalid_request", "Check the saved setup version and try again.", false, nil)
		return
	}
	owner, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	var projection *assistantsetup.Projection
	var err error
	if resume {
		projection, err = h.assistantSetup.ResumeRun(r.Context(), owner, runID, request.IfVersion)
	} else {
		projection, err = h.assistantSetup.DeferRun(r.Context(), owner, runID, request.IfVersion)
	}
	if err != nil {
		writeAssistantSetupServiceError(w, err, projection)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"setup": projection})
}

func validateAssistantSetupQuery(r *http.Request) error {
	if r == nil {
		return errors.New("missing request")
	}
	for key, values := range r.URL.Query() {
		if key != "target_workspace_id" || len(values) != 1 || len(values[0]) > 128 {
			return assistantsetup.ErrInvalid
		}
	}
	return nil
}

func validAssistantSetupOpaque(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

type assistantSetupErrorResponse struct {
	Error     string                     `json:"error"`
	Code      string                     `json:"code"`
	Retryable bool                       `json:"retryable,omitempty"`
	Setup     *assistantsetup.Projection `json:"setup,omitempty"`
}

func writeAssistantSetupServiceError(w http.ResponseWriter, err error, projection *assistantsetup.Projection) {
	switch {
	case errors.Is(err, assistantsetup.ErrInvalid):
		writeAssistantSetupError(w, http.StatusBadRequest, "invalid_request", "Check the setup request and try again.", false, projection)
	case errors.Is(err, assistantsetup.ErrNotFound):
		writeAssistantSetupError(w, http.StatusNotFound, "not_found", "This setup is not available.", false, nil)
	case errors.Is(err, assistantsetup.ErrAssistantNotReady):
		writeAssistantSetupError(w, http.StatusConflict, "assistant_not_ready", "Hire your personal assistant before starting this setup.", false, projection)
	case errors.Is(err, assistantsetup.ErrAssistantRepair):
		writeAssistantSetupError(w, http.StatusConflict, "assistant_repair_required", "Repair the assistant relationship before starting this setup.", false, projection)
	case errors.Is(err, assistantsetup.ErrAmbiguousTarget):
		writeAssistantSetupError(w, http.StatusConflict, "ambiguous_target", "Choose which File Janitor workspace to continue.", false, projection)
	case errors.Is(err, assistantsetup.ErrUnsupportedTarget):
		writeAssistantSetupError(w, http.StatusConflict, "unsupported_target", "This File Janitor workspace needs manual review.", false, projection)
	case errors.Is(err, assistantsetup.ErrStaleProposal):
		writeAssistantSetupError(w, http.StatusConflict, "stale_proposal", "The setup plan changed. Review it again before continuing.", false, projection)
	case errors.Is(err, assistantsetup.ErrInvalidAction):
		writeAssistantSetupError(w, http.StatusConflict, "invalid_action", "That setup action is not available at the current step.", false, projection)
	case errors.Is(err, assistantsetup.ErrStaleRun), errors.Is(err, assistantsetup.ErrConflict):
		writeAssistantSetupError(w, http.StatusConflict, "stale_run", "Setup changed in another window. Review the current state.", false, projection)
	case errors.Is(err, assistantsetup.ErrPrivacyReview):
		writeAssistantSetupError(w, http.StatusConflict, "privacy_review_required", "Review File Janitor privacy settings before continuing.", false, projection)
	case errors.Is(err, assistantsetup.ErrFolderChanged), errors.Is(err, assistantsetup.ErrFolderConflict):
		writeAssistantSetupError(w, http.StatusConflict, "folder_changed", "The File Janitor folder changed. Review it before continuing.", false, projection)
	case errors.Is(err, assistantsetup.ErrAgentRootUnavailable):
		writeAssistantSetupError(w, http.StatusServiceUnavailable, "agent_root_unavailable", "Ori could not reach your agents folder, so nothing was created.", true, projection)
	case errors.Is(err, assistantsetup.ErrTeamConflict):
		writeAssistantSetupError(w, http.StatusConflict, "team_conflict", "The reviewed File Curator setup is no longer available.", false, projection)
	case errors.Is(err, assistantsetup.ErrReconcileRequired):
		writeAssistantSetupError(w, http.StatusConflict, "reconcile_required", "Ori cannot safely prove the last setup result. Review the saved workspace before retrying.", false, projection)
	case errors.Is(err, assistantsetup.ErrNoLongerAvailable):
		writeAssistantSetupError(w, http.StatusConflict, "no_longer_available", "The reviewed File Janitor workspace is no longer available.", false, projection)
	case errors.Is(err, resetstate.ErrWorkFenced), errors.Is(err, resetstate.ErrWorkUntracked):
		writeAssistantSetupError(w, http.StatusServiceUnavailable, "work_fenced", "Setup cannot start while Ori is resetting or shutting down.", true, projection)
	default:
		writeAssistantSetupError(w, http.StatusServiceUnavailable, "assistant_setup_unavailable", "Assistant setup is temporarily unavailable.", true, projection)
	}
}

func writeAssistantSetupError(w http.ResponseWriter, status int, code, message string, retryable bool, projection *assistantsetup.Projection) {
	_ = orihttp.RespondJSON(w, status, assistantSetupErrorResponse{Error: message, Code: code, Retryable: retryable, Setup: projection})
}
