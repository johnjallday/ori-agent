// Package personalhqhttp exposes the Personal HQ domain service
// (internal/personalhq) as a user-scoped HTTP API: status, onboarding-state
// transitions, designate, replace, and clear.
package personalhqhttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// Handler serves the Personal HQ API.
type Handler struct {
	service       *personalhq.Service
	setup         *personalhq.SetupCoordinator
	upgrade       *personalhq.UpgradeCoordinator
	mailboxLinker MailboxLinker
	replies       ReplyService
	followups     FollowUpAPI
	journal       JournalAPI
	provider      userprofile.UserProvider
	// watchtowerSources is intentionally a factory: workspace storage is wired
	// after this handler during server startup, so source access must be lazy.
	watchtowerSources func() dailybrief.SnapshotSources
}

// NewHandler constructs a Personal HQ HTTP handler. provider may be nil, in
// which case requests resolve to the local single-user profile. setup and
// upgrade may be nil (e.g. in tests exercising only status/designate/clear);
// the corresponding endpoints then report 503 rather than panicking.
func NewHandler(service *personalhq.Service, setup *personalhq.SetupCoordinator, upgrade *personalhq.UpgradeCoordinator, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{service: service, setup: setup, upgrade: upgrade, provider: provider}
}

type designateRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

type onboardingStateRequest struct {
	State string `json:"state"`
}

// Status handles GET /api/personal-hq/status.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.service == nil {
		orihttp.ServiceUnavailable(w, "personal hq service is unavailable")
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return
	}
	status, err := h.service.Status(r.Context(), userID)
	if err != nil {
		orihttp.InternalError(w, "Failed to load personal hq status: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"status": status})
}

// SetOnboardingState handles POST /api/personal-hq/onboarding-state.
func (h *Handler) SetOnboardingState(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.service == nil {
		orihttp.ServiceUnavailable(w, "personal hq service is unavailable")
		return
	}
	var req onboardingStateRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	state, ok := userprofile.ParseHQOnboardingState(req.State)
	if !ok {
		orihttp.BadRequest(w, "state must be one of unseen, in_progress, completed, or skipped")
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return
	}
	status, err := h.service.SetOnboardingState(r.Context(), userID, state)
	if err != nil {
		orihttp.InternalError(w, "Failed to update onboarding state: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"status": status})
}

// Designate handles POST /api/personal-hq/designate.
func (h *Handler) Designate(w http.ResponseWriter, r *http.Request) {
	h.handleDesignation(w, r, false)
}

// Replace handles POST /api/personal-hq/replace.
func (h *Handler) Replace(w http.ResponseWriter, r *http.Request) {
	h.handleDesignation(w, r, true)
}

func (h *Handler) handleDesignation(w http.ResponseWriter, r *http.Request, replace bool) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.service == nil {
		orihttp.ServiceUnavailable(w, "personal hq service is unavailable")
		return
	}
	var req designateRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return
	}

	var status *personalhq.Status
	if replace {
		status, err = h.service.Replace(r.Context(), userID, req.WorkspaceID)
	} else {
		status, err = h.service.Designate(r.Context(), userID, req.WorkspaceID)
	}
	if err != nil {
		respondDesignationError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"status": status})
}

// setupRequest is the Build My HQ submission body.
type setupRequest struct {
	Name                    string   `json:"name"`
	Timezone                string   `json:"timezone"`
	ScheduleDays            []string `json:"schedule_days"`
	ScheduleTime            string   `json:"schedule_time"`
	Scope                   string   `json:"scope"`
	SelectedWorkspaceIDs    []string `json:"selected_workspace_ids"`
	IncludeFutureWorkspaces bool     `json:"include_future_workspaces"`
	NotifyOnReady           bool     `json:"notify_on_ready"`
}

// Setup handles POST /api/personal-hq/setup: the Build My HQ flow. Creates
// the HQ workspace, designates it, and captures the brief configuration, all
// as one atomic-from-the-user's-perspective operation.
func (h *Handler) Setup(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.setup == nil {
		orihttp.ServiceUnavailable(w, "personal hq setup is unavailable")
		return
	}
	var req setupRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return
	}
	result, err := h.setup.Setup(r.Context(), userID, personalhq.SetupRequest{
		Name:                    req.Name,
		Timezone:                req.Timezone,
		ScheduleDays:            req.ScheduleDays,
		ScheduleTime:            req.ScheduleTime,
		Scope:                   req.Scope,
		SelectedWorkspaceIDs:    req.SelectedWorkspaceIDs,
		IncludeFutureWorkspaces: req.IncludeFutureWorkspaces,
		NotifyOnReady:           req.NotifyOnReady,
	})
	if err != nil {
		respondSetupError(w, err)
		return
	}
	orihttp.Success(w, result)
}

func respondSetupError(w http.ResponseWriter, err error) {
	var partial *personalhq.SetupPartialFailureError
	switch {
	case errors.Is(err, personalhq.ErrInvalidTimezone):
		orihttp.BadRequest(w, err.Error())
	case errors.As(err, &partial):
		// The workspace exists but a later step failed: give the client
		// enough to offer retry or "keep as a normal workspace" instead of
		// a bare error (PRD FR30).
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error":        err.Error(),
			"workspace_id": partial.WorkspaceID,
			"step":         partial.Step,
		})
	default:
		orihttp.InternalError(w, "Failed to set up personal hq: "+err.Error())
	}
}

// Clear handles POST /api/personal-hq/clear.
func (h *Handler) Clear(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.service == nil {
		orihttp.ServiceUnavailable(w, "personal hq service is unavailable")
		return
	}
	userID, err := h.currentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user: "+err.Error())
		return
	}
	status, err := h.service.Clear(r.Context(), userID)
	if err != nil {
		orihttp.InternalError(w, "Failed to clear personal hq: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"status": status})
}

// respondDesignationError maps domain errors from Designate/Replace to
// actionable HTTP responses instead of a generic 500, per FR42 (missing,
// inaccessible, trashed/missing, group, wrong-owner workspaces must produce
// actionable errors).
func respondDesignationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, personalhq.ErrWorkspaceIDRequired):
		orihttp.BadRequest(w, err.Error())
	case errors.Is(err, personalhq.ErrWorkspaceNotFound):
		orihttp.NotFound(w, err.Error())
	case errors.Is(err, personalhq.ErrGroupNotEligible),
		errors.Is(err, personalhq.ErrWorkspaceUnavailable),
		errors.Is(err, personalhq.ErrUnauthorized):
		orihttp.BadRequest(w, err.Error())
	case errors.Is(err, personalhq.ErrAlreadyDesignated):
		orihttp.Conflict(w, err.Error())
	default:
		orihttp.InternalError(w, "Failed to update personal hq designation: "+err.Error())
	}
}

func (h *Handler) currentUserID(ctx context.Context) (string, error) {
	if h.provider == nil {
		return userprofile.LocalUserID, nil
	}
	userID, err := h.provider.CurrentUserID(ctx)
	if err != nil {
		return "", err
	}
	if userID == "" {
		return userprofile.LocalUserID, nil
	}
	return userID, nil
}
