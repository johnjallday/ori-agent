// Package workspacecapabilityhttp serves the workspace capability lifecycle
// API: listing the built-in capabilities available to a workspace and
// installing one.
//
// Two invariants hold at this boundary and are enforced here, not deeper
// (matching downloadsjanitorhttp, whose convention this follows):
//
//   - Every request is scoped to a workspace the current user owns (FR-140).
//   - A client-supplied capability ID may only select a definition compiled
//     into this build; it can never introduce one (FR-14).
//
// The catalog reports each installed capability's health as derived by its
// service at request time. A status persisted on the workspace record is never
// treated as authoritative (FR-6).
package workspacecapabilityhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// WorkspaceLookup resolves a workspace for existence and ownership checks.
type WorkspaceLookup interface {
	Get(id string) (*workspace.Workspace, error)
}

// Handler serves the capability lifecycle endpoints.
type Handler struct {
	service  *workspacecapability.Service
	lookup   WorkspaceLookup
	provider userprofile.UserProvider
}

// NewHandler builds the capability handler. A nil service makes every endpoint
// report 503 rather than panicking, matching the other workspace handlers'
// behavior when their dependencies are unavailable — a capability that failed
// to wire must not take the rest of the workspace API down (FR-145).
func NewHandler(service *workspacecapability.Service, lookup WorkspaceLookup, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{service: service, lookup: lookup, provider: provider}
}

// ListCapabilities handles GET /api/workspaces/{workspaceID}/capabilities.
//
// It returns every built-in capability with this workspace's installed state
// and, for installed ones, freshly derived health.
func (h *Handler) ListCapabilities(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}

	items, err := h.service.Catalog(workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to list workspace capabilities")
		return
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"success":      true,
		"capabilities": items,
	})
}

// InstallCapability handles
// POST /api/workspaces/{workspaceID}/capabilities/{capabilityID}/install.
//
// Installing records that the capability belongs to this workspace. It does not
// request folder access, create a second workspace, or start any automation —
// those need the user's explicit approval during setup (FR-20, FR-23).
//
// Repeating the request is success, not an error: the response reports
// already_installed and the unchanged record (FR-9).
func (h *Handler) InstallCapability(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}

	capabilityID := strings.TrimSpace(r.PathValue("capabilityID"))
	if capabilityID == "" {
		_ = orihttp.RespondBadRequest(w, "capability id is required")
		return
	}

	// The body is optional: only install provenance may be supplied, and even
	// that is normalized by the service. There is deliberately no field here
	// that could name a folder, a path, or anything executable.
	var req struct {
		Source string `json:"source,omitempty"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
	}

	result, err := h.service.Install(workspacecapability.InstallRequest{
		WorkspaceID:  workspaceID,
		CapabilityID: capabilityID,
		Source:       req.Source,
	})
	if err != nil {
		h.respondError(w, err, "Failed to install workspace capability")
		return
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"success":           true,
		"capability":        result.Definition,
		"record":            result.Record,
		"status":            result.Status,
		"already_installed": result.AlreadyInstalled,
	})
}

// resolveWorkspace enforces the boundary shared by every endpoint: the service
// is wired, the workspace exists, and it belongs to the current user. A
// workspace owned by someone else is reported as not found rather than
// forbidden, so the API does not confirm that another user's workspace exists.
func (h *Handler) resolveWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.service == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "Workspace capabilities are not available."))
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
		logger.Warn("Failed to resolve current user for workspace capabilities", logger.Fields{"error": err})
		return false
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	return strings.EqualFold(owner, userID)
}

// respondError maps a lifecycle error onto a stable HTTP response. Lifecycle
// failures carry a curated code and message; everything else is logged
// server-side and reported generically, so internal detail never reaches the
// client.
func (h *Handler) respondError(w http.ResponseWriter, err error, fallback string) {
	var lifecycleErr *workspacecapability.Error
	if errors.As(err, &lifecycleErr) {
		status := http.StatusBadRequest
		switch lifecycleErr.Code {
		case workspacecapability.CodeWorkspaceMissing:
			status = http.StatusNotFound
		case workspacecapability.CodeCapabilityUnavailable:
			status = http.StatusNotFound
		case workspacecapability.CodeInstallLimit:
			status = http.StatusConflict
		case workspacecapability.CodeInstallFailed, workspacecapability.CodeInstallIncomplete:
			status = http.StatusInternalServerError
		}
		logger.Warn("Workspace capability request failed", logger.Fields{
			"code":  lifecycleErr.Code,
			"error": lifecycleErr.Error(),
		})
		details := map[string]any{}
		if lifecycleErr.Repair != "" {
			details["repair"] = lifecycleErr.Repair
		}
		apiErr := &orihttp.APIError{Code: lifecycleErr.Code, Message: lifecycleErr.Message}
		if len(details) > 0 {
			apiErr.Details = details
		}
		_ = orihttp.RespondAPIError(w, status, apiErr)
		return
	}

	logger.Error(fallback, logger.Fields{"error": err})
	_ = orihttp.RespondInternalError(w, fallback)
}
