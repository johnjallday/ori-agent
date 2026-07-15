// Package dailybriefhttp exposes the Daily Brief domain (internal/dailybrief)
// as a user-scoped HTTP API: configuration, current brief, history,
// first-open/manual generation requests, and generation status polling.
// Every endpoint resolves and authorizes against the caller's designated
// Personal HQ workspace on every call (PRD task 7.1) — there is no
// standalone "brief" identity independent of the HQ.
package dailybriefhttp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// generationTimeout bounds a background (first-open/manual) generation
// attempt kicked off from an HTTP request whose own context ends when the
// response is written.
const generationTimeout = 2 * time.Minute

// ErrNoValidHQ is returned by currentHQWorkspace when the caller has no
// valid designated Personal HQ.
var ErrNoValidHQ = errors.New("dailybriefhttp: no valid personal hq designated")

// Handler serves the Daily Brief API.
type Handler struct {
	service    *dailybrief.Service
	personalHQ *personalhq.Service
	provider   userprofile.UserProvider
}

// NewHandler constructs a Daily Brief HTTP handler. provider may be nil, in
// which case requests resolve to the local single-user profile.
func NewHandler(service *dailybrief.Service, personalHQ *personalhq.Service, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{service: service, personalHQ: personalHQ, provider: provider}
}

func (h *Handler) userID(ctx context.Context) (string, error) {
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

// currentHQWorkspace resolves (userID, workspaceID) for the request,
// enforcing user/HQ authorization on every call: the caller must have a
// valid, currently-designated Personal HQ. Returns ErrNoValidHQ otherwise
// (a stale/missing designation is the personalhq repair flow's job, not
// this handler's).
func (h *Handler) currentHQWorkspace(ctx context.Context) (userID, workspaceID string, err error) {
	userID, err = h.userID(ctx)
	if err != nil {
		return "", "", err
	}
	status, err := h.personalHQ.Status(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if !status.Valid {
		return "", "", ErrNoValidHQ
	}
	return userID, status.WorkspaceID, nil
}

func respondHQError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNoValidHQ) {
		orihttp.NotFound(w, "no personal hq is designated")
		return
	}
	orihttp.InternalError(w, "Failed to resolve personal hq: "+err.Error())
}

func (h *Handler) unavailable() bool {
	return h == nil || h.service == nil || h.personalHQ == nil
}

// GetConfig handles GET /api/personal-hq/brief/config.
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h.unavailable() {
		orihttp.ServiceUnavailable(w, "daily brief is unavailable")
		return
	}
	_, workspaceID, err := h.currentHQWorkspace(r.Context())
	if err != nil {
		respondHQError(w, err)
		return
	}
	cfg, err := h.service.GetConfig(r.Context(), workspaceID)
	if errors.Is(err, dailybrief.ErrConfigNotFound) {
		defaults, normErr := dailybrief.NormalizeConfig(dailybrief.Config{WorkspaceID: workspaceID})
		if normErr != nil {
			orihttp.InternalError(w, "Failed to build default daily brief config: "+normErr.Error())
			return
		}
		orihttp.Success(w, map[string]any{"config": defaults, "configured": false})
		return
	}
	if err != nil {
		orihttp.InternalError(w, "Failed to load daily brief config: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"config": cfg, "configured": true})
}

// updateConfigRequest is the PUT /api/personal-hq/brief/config body.
type updateConfigRequest struct {
	Timezone                string   `json:"timezone"`
	ScheduleDays            []string `json:"schedule_days"`
	ScheduleTime            string   `json:"schedule_time"`
	ScheduleEnabled         *bool    `json:"schedule_enabled"`
	Scope                   string   `json:"scope"`
	SelectedWorkspaceIDs    []string `json:"selected_workspace_ids"`
	IncludeFutureWorkspaces bool     `json:"include_future_workspaces"`
	NotifyOnReady           bool     `json:"notify_on_ready"`
}

// UpdateConfig handles PUT /api/personal-hq/brief/config. Source/schedule
// changes affect future generations only — the underlying Service/Store
// never rewrites prior revisions (PRD 5.4).
func (h *Handler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPut) {
		return
	}
	if h.unavailable() {
		orihttp.ServiceUnavailable(w, "daily brief is unavailable")
		return
	}
	userID, workspaceID, err := h.currentHQWorkspace(r.Context())
	if err != nil {
		respondHQError(w, err)
		return
	}
	var req updateConfigRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	scheduleEnabled := true
	if req.ScheduleEnabled != nil {
		scheduleEnabled = *req.ScheduleEnabled
	}
	cfg, err := h.service.UpdateConfig(r.Context(), dailybrief.Config{
		WorkspaceID:             workspaceID,
		UserID:                  userID,
		Timezone:                req.Timezone,
		ScheduleDays:            req.ScheduleDays,
		ScheduleTime:            req.ScheduleTime,
		ScheduleEnabled:         scheduleEnabled,
		Scope:                   dailybrief.Scope(req.Scope),
		SelectedWorkspaceIDs:    req.SelectedWorkspaceIDs,
		IncludeFutureWorkspaces: req.IncludeFutureWorkspaces,
		NotifyOnReady:           req.NotifyOnReady,
	})
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"config": cfg})
}

// GetCurrent handles GET /api/personal-hq/brief/current.
func (h *Handler) GetCurrent(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h.unavailable() {
		orihttp.ServiceUnavailable(w, "daily brief is unavailable")
		return
	}
	_, workspaceID, err := h.currentHQWorkspace(r.Context())
	if err != nil {
		respondHQError(w, err)
		return
	}
	rev, err := h.service.GetCurrent(r.Context(), workspaceID)
	if errors.Is(err, dailybrief.ErrRevisionNotFound) {
		orihttp.Success(w, map[string]any{"revision": nil})
		return
	}
	if err != nil {
		orihttp.InternalError(w, "Failed to load current daily brief: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"revision": rev})
}

// GetHistory handles GET /api/personal-hq/brief/history.
func (h *Handler) GetHistory(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h.unavailable() {
		orihttp.ServiceUnavailable(w, "daily brief is unavailable")
		return
	}
	_, workspaceID, err := h.currentHQWorkspace(r.Context())
	if err != nil {
		respondHQError(w, err)
		return
	}
	history, err := h.service.GetHistory(r.Context(), workspaceID, dailybrief.MinRetentionDays)
	if err != nil {
		orihttp.InternalError(w, "Failed to load daily brief history: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"history": history})
}

// GetStatus handles GET /api/personal-hq/brief/status: lets a client poll a
// first-open/scheduled generation without blocking the request that started
// it (PRD FR56/task 7.4).
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h.unavailable() {
		orihttp.ServiceUnavailable(w, "daily brief is unavailable")
		return
	}
	_, workspaceID, err := h.currentHQWorkspace(r.Context())
	if err != nil {
		respondHQError(w, err)
		return
	}
	active, err := h.service.GetActiveGeneration(r.Context(), workspaceID)
	if err != nil || active == nil {
		orihttp.Success(w, map[string]any{"status": "idle"})
		return
	}
	orihttp.Success(w, map[string]any{"status": string(active.Status), "revision_id": active.RevisionID})
}

// RequestFirstOpen handles POST /api/personal-hq/brief/open: the first
// user-visible app open of a local day. Runs in the background so it never
// blocks Home (PRD FR56/task 7.4/7.9); the client polls GetStatus/GetCurrent.
func (h *Handler) RequestFirstOpen(w http.ResponseWriter, r *http.Request) {
	h.requestGeneration(w, r, dailybrief.TriggerFirstOpen)
}

// RequestRefresh handles POST /api/personal-hq/brief/refresh: a manual
// refresh, which always creates a new same-day revision (PRD FR58).
func (h *Handler) RequestRefresh(w http.ResponseWriter, r *http.Request) {
	h.requestGeneration(w, r, dailybrief.TriggerManual)
}

func (h *Handler) requestGeneration(w http.ResponseWriter, r *http.Request, trigger dailybrief.Trigger) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h.unavailable() {
		orihttp.ServiceUnavailable(w, "daily brief is unavailable")
		return
	}
	userID, workspaceID, err := h.currentHQWorkspace(r.Context())
	if err != nil {
		respondHQError(w, err)
		return
	}
	// A brand-new HQ may not have explicit brief settings yet; default them
	// rather than requiring a settings visit before the first brief can
	// generate (PRD FR21: the default path must be completable with no
	// extra steps).
	if _, err := h.service.GetConfig(r.Context(), workspaceID); errors.Is(err, dailybrief.ErrConfigNotFound) {
		if _, err := h.service.UpdateConfig(r.Context(), dailybrief.Config{WorkspaceID: workspaceID, UserID: userID}); err != nil {
			orihttp.InternalError(w, "Failed to initialize daily brief config: "+err.Error())
			return
		}
	}

	// Detached from the request's context (which ends when this handler
	// returns) with its own bound, so generation keeps running in the
	// background rather than blocking this response.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), generationTimeout)
		defer cancel()
		if _, err := h.service.RequestGenerationNow(ctx, workspaceID, userID, trigger); err != nil &&
			!errors.Is(err, dailybrief.ErrGenerationInProgress) {
			logger.Warn("dailybriefhttp: background generation failed", logger.Fields{
				"workspace_id": workspaceID, "trigger": string(trigger), "error": err,
			})
		}
	}()
	_ = orihttp.RespondJSON(w, http.StatusAccepted, map[string]any{"status": "pending"})
}
