// Package downloadsjanitorhttp serves the Downloads Janitor API: reading a
// workspace's setup/readiness state and confirming a folder selection.
//
// Two invariants hold at this boundary and are enforced here, not deeper:
// every request is scoped to a workspace the current user owns, and the only
// filesystem path a client may ever submit is the folder the user explicitly
// confirms during setup.
package downloadsjanitorhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/downloadsjanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceLookup resolves a workspace for existence and ownership checks.
type WorkspaceLookup interface {
	Get(id string) (*workspace.Workspace, error)
}

// Handler serves the Downloads Janitor endpoints.
type Handler struct {
	service  *downloadsjanitor.Service
	lookup   WorkspaceLookup
	provider userprofile.UserProvider
}

// NewHandler builds the Downloads Janitor handler. A nil service makes every
// endpoint report 503 rather than panicking, matching the other workspace
// handlers' behavior when their storage is unavailable.
func NewHandler(service *downloadsjanitor.Service, lookup WorkspaceLookup, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{service: service, lookup: lookup, provider: provider}
}

// GetStatus handles GET /api/workspaces/{workspaceID}/downloads-janitor.
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	status, err := h.service.Status(workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to read Downloads Janitor status")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "status": status})
}

// GetReadiness handles GET /api/workspaces/{workspaceID}/downloads-janitor/readiness.
// It is the same evaluation as GetStatus, returned on its own so a status
// widget can poll it without re-reading settings it already has.
func (h *Handler) GetReadiness(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	status, err := h.service.Status(workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to check Downloads Janitor readiness")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "readiness": status.Readiness})
}

// ConfirmSetup handles POST /api/workspaces/{workspaceID}/downloads-janitor/setup.
//
// The request body's path is the user's explicit folder confirmation — the one
// place in this feature where a client-supplied path is accepted. Everything
// afterwards derives paths from the stored root instead.
func (h *Handler) ConfirmSetup(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Path               string `json:"path"`
		DailyScanLocalTime string `json:"daily_scan_local_time"`
		Timezone           string `json:"timezone"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	status, err := h.service.ConfirmSetup(downloadsjanitor.SetupRequest{
		WorkspaceID:        workspaceID,
		Path:               req.Path,
		DailyScanLocalTime: req.DailyScanLocalTime,
		Timezone:           req.Timezone,
	})
	if err != nil {
		h.respondError(w, err, "Failed to set up Downloads Janitor")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "status": status})
}

// resolveWorkspace enforces the boundary shared by every endpoint: the service
// is wired, the workspace exists, and it belongs to the current user. A
// workspace owned by someone else is reported as not found rather than
// forbidden, so the API does not confirm that another user's workspace exists.
func (h *Handler) resolveWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.service == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "Downloads Janitor is not available."))
		return "", false
	}
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if workspaceID == "" {
		_ = orihttp.RespondBadRequest(w, "workspace id is required")
		return "", false
	}
	if h.lookup == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "Workspace storage is not available."))
		return "", false
	}
	ws, err := h.lookup.Get(workspaceID)
	if err != nil || ws == nil {
		_ = orihttp.RespondNotFound(w, "workspace not found")
		return "", false
	}
	if !h.ownedByCurrentUser(r.Context(), ws) {
		_ = orihttp.RespondNotFound(w, "workspace not found")
		return "", false
	}
	return workspaceID, true
}

// ownedByCurrentUser reports whether the workspace belongs to the requesting
// user. A workspace with no recorded owner is the local single user's, matching
// how the rest of Ori treats unowned workspaces.
func (h *Handler) ownedByCurrentUser(ctx context.Context, ws *workspace.Workspace) bool {
	owner := strings.TrimSpace(ws.OwnerUserID)
	if owner == "" {
		owner = userprofile.LocalUserID
	}
	userID, err := h.provider.CurrentUserID(ctx)
	if err != nil {
		logger.Warn("Failed to resolve current user for Downloads Janitor", logger.Fields{"error": err})
		return false
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	return strings.EqualFold(owner, userID)
}

// respondError maps a domain error onto a stable HTTP response. Setup failures
// carry a curated code and message; everything else is logged server-side and
// reported generically, so raw filesystem errors never reach the client.
func (h *Handler) respondError(w http.ResponseWriter, err error, fallback string) {
	var setupError *downloadsjanitor.SetupError
	if errors.As(err, &setupError) {
		status := http.StatusBadRequest
		switch setupError.Code {
		case downloadsjanitor.CodePermissionDenied:
			status = http.StatusForbidden
		case downloadsjanitor.CodeWorkspaceMissing:
			status = http.StatusNotFound
		case downloadsjanitor.CodePersistenceFailed, downloadsjanitor.CodeBindingFailed:
			status = http.StatusInternalServerError
		}
		logger.Warn("Downloads Janitor setup failed", logger.Fields{"code": setupError.Code, "error": setupError.Error()})
		_ = orihttp.RespondAPIError(w, status, &orihttp.APIError{
			Code:    setupError.Code,
			Message: setupError.Message,
			Details: map[string]any{"repair": setupError.Repair},
		})
		return
	}
	if errors.Is(err, downloadsjanitor.ErrInvalidSettings) {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	logger.Error(fallback, logger.Fields{"error": err})
	_ = orihttp.RespondInternalError(w, fallback)
}
