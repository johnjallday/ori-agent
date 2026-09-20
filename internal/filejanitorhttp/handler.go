// Package filejanitorhttp serves the File Janitor API: reading a
// workspace's setup/readiness state and confirming a folder selection.
//
// Two invariants hold at this boundary and are enforced here, not deeper:
// every request is scoped to a workspace the current user owns, and the only
// filesystem path a client may ever submit is the folder the user explicitly
// confirms during setup.
package filejanitorhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceLookup resolves a workspace for existence and ownership checks.
type WorkspaceLookup interface {
	Get(id string) (*workspace.Workspace, error)
}

// Automation is the slice of the automation service the API needs: pausing
// must actually stop the watcher, not merely record an intention to.
type Automation interface {
	EnsureWatcher(workspaceID string) error
}

// Handler serves the File Janitor endpoints.
type PathSelectionResolver interface {
	Resolve(token string) (string, error)
}

type AssistantSetupCoordinator interface {
	CommitFolderGrant(
		ctx context.Context,
		ownerUserID, workspaceID, token string,
		commit func(assistantsetup.FolderGrantAuthorization) (assistantsetup.FolderGrantResult, error),
	) (*assistantsetup.Projection, error)
}

type Handler struct {
	service        *filejanitor.Service
	lookup         WorkspaceLookup
	provider       userprofile.UserProvider
	automation     Automation
	pathSelections PathSelectionResolver
	assistantSetup AssistantSetupCoordinator
}

// SetAutomation wires the watcher lifecycle so setup and pause/resume take
// effect immediately.
func (h *Handler) SetPathSelectionResolver(resolver PathSelectionResolver) {
	if h != nil {
		h.pathSelections = resolver
	}
}

func (h *Handler) SetAssistantSetupCoordinator(coordinator AssistantSetupCoordinator) {
	if h != nil {
		h.assistantSetup = coordinator
	}
}

func (h *Handler) SetAutomation(automation Automation) {
	if h != nil {
		h.automation = automation
	}
}

// syncAutomation brings the workspace's watcher in line with its settings.
// Failures are not fatal to the request: readiness reports the watcher as not
// running, which is visible and repairable, rather than failing a setting the
// user did successfully change.
func (h *Handler) syncAutomation(workspaceID string) {
	if h == nil || h.automation == nil {
		return
	}
	if err := h.automation.EnsureWatcher(workspaceID); err != nil {
		logger.Warn("File Janitor watcher could not be updated", logger.Fields{
			"workspace_id": workspaceID, "error": err,
		})
	}
}

// syncAutomationAndRefresh brings the watcher in line with the new settings and
// re-reads status, so the response describes the state after the change rather
// than the moment before it.
func (h *Handler) syncAutomationAndRefresh(workspaceID string, fallback filejanitor.Status) filejanitor.Status {
	if h == nil || h.automation == nil {
		return fallback
	}
	h.syncAutomation(workspaceID)
	refreshed, err := h.service.Status(workspaceID)
	if err != nil {
		return fallback
	}
	return refreshed
}

// NewHandler builds the File Janitor handler. A nil service makes every
// endpoint report 503 rather than panicking, matching the other workspace
// handlers' behavior when their storage is unavailable.
func NewHandler(service *filejanitor.Service, lookup WorkspaceLookup, provider userprofile.UserProvider) *Handler {
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
		h.respondError(w, err, "Failed to read File Janitor status")
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
		h.respondError(w, err, "Failed to check File Janitor readiness")
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
		Path                string `json:"path"`
		SelectionToken      string `json:"selection_token"`
		AssistantSetupToken string `json:"assistant_setup_token"`
		DailyScanLocalTime  string `json:"daily_scan_local_time"`
		Timezone            string `json:"timezone"`
		// Paused is optional and tri-state: omitted keeps the workspace's
		// current setting. Assisted setup requires true, preserving the separate
		// monitoring decision.
		Paused *bool `json:"paused,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	assisted := strings.TrimSpace(req.SelectionToken) != "" || strings.TrimSpace(req.AssistantSetupToken) != ""
	if !assisted {
		status, err := h.service.ConfirmSetup(filejanitor.SetupRequest{
			WorkspaceID: workspaceID, Path: req.Path,
			DailyScanLocalTime: req.DailyScanLocalTime, Timezone: req.Timezone, Paused: req.Paused,
		})
		if err != nil {
			h.respondError(w, err, "Failed to set up File Janitor")
			return
		}
		status = h.syncAutomationAndRefresh(workspaceID, status)
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "status": status})
		return
	}
	if strings.TrimSpace(req.Path) != "" || strings.TrimSpace(req.SelectionToken) == "" ||
		strings.TrimSpace(req.AssistantSetupToken) == "" || req.Paused == nil || !*req.Paused ||
		strings.TrimSpace(req.DailyScanLocalTime) != "" || strings.TrimSpace(req.Timezone) != "" ||
		len(req.SelectionToken) > 256 || len(req.AssistantSetupToken) > 256 {
		_ = orihttp.RespondBadRequest(w, "assisted folder setup requires only selection_token, assistant_setup_token, and paused:true")
		return
	}
	if h.pathSelections == nil || h.assistantSetup == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable, orihttp.NewAPIError("assistant_setup_unavailable", "Assistant folder setup is temporarily unavailable."))
		return
	}
	scopedResolver, ok := h.pathSelections.(interface {
		ResolveFor(token, scope string) (string, error)
	})
	if !ok {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable, orihttp.NewAPIError("assistant_setup_unavailable", "Scoped folder selection is temporarily unavailable."))
		return
	}
	selectedPath, err := scopedResolver.ResolveFor(req.SelectionToken, workspaceID)
	if err != nil {
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("folder_selection_expired", "The folder selection expired. Choose the folder again."))
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable, orihttp.NewAPIError("assistant_setup_unavailable", "Assistant folder setup is temporarily unavailable."))
		return
	}
	var status filejanitor.Status
	projection, err := h.assistantSetup.CommitFolderGrant(
		r.Context(), owner, workspaceID, req.AssistantSetupToken,
		func(authorization assistantsetup.FolderGrantAuthorization) (assistantsetup.FolderGrantResult, error) {
			var grantErr error
			status, grantErr = h.service.ConfirmSetup(filejanitor.SetupRequest{
				WorkspaceID: workspaceID, Path: selectedPath, Paused: req.Paused,
				Operation: &filejanitor.FolderGrantOperation{RunID: authorization.RunID, OperationID: authorization.OperationID},
			})
			if grantErr != nil {
				safeCode := "folder_grant_failed"
				var setupError *filejanitor.SetupError
				if errors.As(grantErr, &setupError) && strings.TrimSpace(setupError.Code) != "" {
					safeCode = setupError.Code
				}
				return assistantsetup.FolderGrantResult{}, &assistantsetup.FolderGrantCommitError{SafeCode: safeCode, Err: grantErr}
			}
			return assistantsetup.FolderGrantResult{
				RootGenerationID:   status.Settings.RootID,
				DirectoryReference: status.Settings.DirectoryReferenceID,
			}, nil
		},
	)
	if err != nil {
		var setupError *filejanitor.SetupError
		if errors.As(err, &setupError) {
			if setupError.Code == filejanitor.CodeFolderConflict && setupError.ConflictWorkspaceID != "" {
				h.respondAssistedFolderConflict(w, setupError, owner)
				return
			}
			h.respondError(w, err, "Failed to set up File Janitor")
			return
		}
		h.respondAssistantSetupError(w, err)
		return
	}
	status = h.syncAutomationAndRefresh(workspaceID, status)
	// The assisted response returns stable permission identity and readiness, not
	// the raw path resolved from the native picker token.
	status.Settings.RootPath = ""
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "status": status, "setup": projection})
}

// SetPaused handles POST /api/workspaces/{workspaceID}/downloads-janitor/pause.
// Pausing stops unattended scanning; it does not discard settings, pending
// candidates, or history, and the user can still scan on demand.
func (h *Handler) SetPaused(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Paused bool `json:"paused"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	status, err := h.service.SetPaused(workspaceID, req.Paused)
	if err != nil {
		h.respondError(w, err, "Failed to update File Janitor")
		return
	}
	// The flag and the watcher move together: a paused workspace whose watcher
	// kept running would be lying to the user.
	status = h.syncAutomationAndRefresh(workspaceID, status)
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "status": status})
}

// resolveWorkspace enforces the boundary shared by every endpoint: the service
// is wired, the workspace exists, and it belongs to the current user. A
// workspace owned by someone else is reported as not found rather than
// forbidden, so the API does not confirm that another user's workspace exists.
func (h *Handler) resolveWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.service == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "File Janitor is not available."))
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

// currentOwner resolves the authenticated local owner once for assisted
// operation binding.
func (h *Handler) currentOwner(ctx context.Context) (string, error) {
	if h == nil || h.provider == nil {
		return "", errors.New("current user provider is unavailable")
	}
	userID, err := h.provider.CurrentUserID(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	return strings.TrimSpace(userID), nil
}

// ownedByCurrentUser reports whether the workspace belongs to the requesting
// user. A workspace with no recorded owner is the local single user's.
func (h *Handler) ownedByCurrentUser(ctx context.Context, ws *workspace.Workspace) bool {
	owner := strings.TrimSpace(ws.OwnerUserID)
	if owner == "" {
		owner = userprofile.LocalUserID
	}
	userID, err := h.currentOwner(ctx)
	if err != nil {
		logger.Warn("Failed to resolve current user for File Janitor", logger.Fields{"error": err})
		return false
	}
	return strings.EqualFold(owner, userID)
}

func (h *Handler) respondAssistantSetupError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	code := "stale_run"
	message := "Assistant setup changed. Review the current step and try again."
	switch {
	case errors.Is(err, assistantsetup.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "This assistant setup is not available."
	case errors.Is(err, assistantsetup.ErrInvalidAction):
		code, message = "invalid_action", "Folder permission is not available at the current setup step."
	case errors.Is(err, assistantsetup.ErrFolderConflict):
		code, message = "folder_conflict", "Another folder permission is already in progress."
	case errors.Is(err, assistantsetup.ErrPrivacyReview):
		code, message = "privacy_review_required", "Review the workspace privacy settings before continuing."
	case errors.Is(err, assistantsetup.ErrUnavailable):
		status, code, message = http.StatusServiceUnavailable, "assistant_setup_unavailable", "Assistant folder setup is temporarily unavailable."
	}
	_ = orihttp.RespondAPIError(w, status, orihttp.NewAPIError(code, message))
}

func (h *Handler) respondAssistedFolderConflict(w http.ResponseWriter, setupError *filejanitor.SetupError, ownerUserID string) {
	details := map[string]any{"repair": setupError.Repair}
	if h != nil && h.lookup != nil {
		if conflicting, err := h.lookup.Get(setupError.ConflictWorkspaceID); err == nil && conflicting != nil &&
			(conflicting.OwnerUserID == ownerUserID || (conflicting.OwnerUserID == "" && ownerUserID == userprofile.LocalUserID)) &&
			strings.TrimSpace(conflicting.FolderSlug) != "" {
			details["conflict_route"] = "/workspaces/" + url.PathEscape(conflicting.FolderSlug) + "?panel=file-janitor"
		}
	}
	_ = orihttp.RespondAPIError(w, http.StatusConflict, &orihttp.APIError{
		Code: setupError.Code, Message: setupError.Message, Details: details,
	})
}

// respondError maps a domain error onto a stable HTTP response. Setup failures
// carry a curated code and message; everything else is logged server-side and
// reported generically, so raw filesystem errors never reach the client.
func (h *Handler) respondError(w http.ResponseWriter, err error, fallback string) {
	var setupError *filejanitor.SetupError
	if errors.As(err, &setupError) {
		status := http.StatusBadRequest
		switch setupError.Code {
		case filejanitor.CodePermissionDenied:
			status = http.StatusForbidden
		case filejanitor.CodeWorkspaceMissing:
			status = http.StatusNotFound
		case filejanitor.CodeFolderConflict, filejanitor.CodeFolderChanged, filejanitor.CodePrivacyReviewRequired:
			status = http.StatusConflict
		case filejanitor.CodePersistenceFailed, filejanitor.CodeBindingFailed:
			status = http.StatusInternalServerError
		}
		logger.Warn("File Janitor setup failed", logger.Fields{"code": setupError.Code})
		details := map[string]any{"repair": setupError.Repair}
		// A folder conflict names the owning workspace so the UI can offer a
		// route to it rather than leaving the user to find it (FR-49). Only the
		// authorized owner reaches this handler at all.
		if setupError.ConflictWorkspaceID != "" {
			details["conflict_workspace_id"] = setupError.ConflictWorkspaceID
		}
		_ = orihttp.RespondAPIError(w, status, &orihttp.APIError{
			Code:    setupError.Code,
			Message: setupError.Message,
			Details: details,
		})
		return
	}
	// An unavailable folder is a recoverable, explainable state — the user
	// unlinked it, moved it, or lost permission — not an internal fault. It gets
	// the same actionable treatment readiness gives it, rather than a 500 that
	// tells the user nothing they can act on.
	if errors.Is(err, filejanitor.ErrRootUnavailable) {
		_ = orihttp.RespondAPIError(w, http.StatusConflict, &orihttp.APIError{
			Code:    "folder_unavailable",
			Message: "Ori cannot reach the folder it was tidying. Reconnect it in settings to continue.",
			Details: map[string]any{"repair": filejanitor.RepairRelinkFolder},
		})
		return
	}
	if errors.Is(err, filejanitor.ErrInvalidSettings) {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	logger.Error(fallback, logger.Fields{"error": err})
	_ = orihttp.RespondInternalError(w, fallback)
}
