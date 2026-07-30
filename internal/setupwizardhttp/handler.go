// Package setupwizardhttp serves the shared Setup Wizard API: reading a
// workspace's setup state and performing the small set of transitions the
// dialog can request.
//
// The boundary enforced here, and not deeper, is twofold. Every request is
// scoped to a workspace the current user owns — a workspace belonging to
// someone else is reported as not found, so the API never confirms it exists.
// And every request body is closed: a client may name a workspace, a step the
// workspace already recorded, and an option that step already offered. There is
// no field through which it could name an adapter, a filesystem path, a
// connector operation, a plugin source, or an endpoint, because no such field
// exists.
package setupwizardhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// maxActionBodySize bounds a step-action body. The only field one carries is a
// short option token, so anything larger is a client sending something this API
// does not accept.
const maxActionBodySize = 4 << 10

// maxOptionLength bounds an option token.
const maxOptionLength = 64

// WorkspaceLookup resolves a workspace for existence and ownership checks.
type WorkspaceLookup interface {
	Get(id string) (*workspace.Workspace, error)
}

// Handler serves the Setup Wizard endpoints.
type Handler struct {
	service  *setupwizard.Service
	lookup   WorkspaceLookup
	provider userprofile.UserProvider
}

// NewHandler builds the Setup Wizard handler. A nil service makes every
// endpoint report 503 rather than panicking, matching the other workspace
// handlers when their dependencies are unavailable.
func NewHandler(service *setupwizard.Service, lookup WorkspaceLookup, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{service: service, lookup: lookup, provider: provider}
}

// GetStatus handles GET /api/workspaces/{workspaceID}/setup-wizard.
//
// The response is always freshly evaluated. There is no cached verdict to go
// stale, which is what lets a workspace that was ready yesterday report that it
// needs attention today.
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	h.run(w, r, func(ctx context.Context, workspaceID string) (setupwizard.Status, error) {
		return h.service.Status(ctx, workspaceID)
	})
}

// Open handles POST /api/workspaces/{workspaceID}/setup-wizard/open. It records
// that the wizard was shown, which is what spends its one auto-open.
func (h *Handler) Open(w http.ResponseWriter, r *http.Request) {
	h.run(w, r, func(ctx context.Context, workspaceID string) (setupwizard.Status, error) {
		return h.service.Open(ctx, workspaceID)
	})
}

// Dismiss handles POST /api/workspaces/{workspaceID}/setup-wizard/dismiss. It
// suppresses auto-open and nothing else: no step is completed and no readiness
// is implied.
func (h *Handler) Dismiss(w http.ResponseWriter, r *http.Request) {
	h.run(w, r, func(ctx context.Context, workspaceID string) (setupwizard.Status, error) {
		return h.service.Dismiss(ctx, workspaceID)
	})
}

// Recheck handles POST /api/workspaces/{workspaceID}/setup-wizard/recheck,
// backing the dialog's "Check again" control.
func (h *Handler) Recheck(w http.ResponseWriter, r *http.Request) {
	h.run(w, r, func(ctx context.Context, workspaceID string) (setupwizard.Status, error) {
		return h.service.Recheck(ctx, workspaceID)
	})
}

// Complete handles POST /api/workspaces/{workspaceID}/setup-wizard/complete.
// The server re-evaluates every required step first; a client cannot finish a
// wizard by asserting that it is finished.
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	h.run(w, r, func(ctx context.Context, workspaceID string) (setupwizard.Status, error) {
		return h.service.Complete(ctx, workspaceID)
	})
}

// ConfirmStep handles
// POST /api/workspaces/{workspaceID}/setup-wizard/steps/{stepID}/confirm: the
// user has read the step's disclosure and approved it.
func (h *Handler) ConfirmStep(w http.ResponseWriter, r *http.Request) {
	stepID, ok := h.stepID(w, r)
	if !ok {
		return
	}
	var body struct {
		Option string `json:"option,omitempty"`
	}
	if !decodeOptionalBody(w, r, &body) {
		return
	}
	option := strings.TrimSpace(body.Option)
	if len(option) > maxOptionLength {
		_ = orihttp.RespondBadRequest(w, "option is too long")
		return
	}
	h.run(w, r, func(ctx context.Context, workspaceID string) (setupwizard.Status, error) {
		return h.service.Confirm(ctx, workspaceID, stepID, setupwizard.StepAction{
			Type:   setupwizard.ActionConfirm,
			Option: option,
		})
	})
}

// SkipStep handles
// POST /api/workspaces/{workspaceID}/setup-wizard/steps/{stepID}/skip. Only
// optional steps can be skipped; the service refuses the rest.
func (h *Handler) SkipStep(w http.ResponseWriter, r *http.Request) {
	stepID, ok := h.stepID(w, r)
	if !ok {
		return
	}
	h.run(w, r, func(ctx context.Context, workspaceID string) (setupwizard.Status, error) {
		return h.service.Skip(ctx, workspaceID, stepID)
	})
}

// run applies the shared boundary to every endpoint: the service is wired, the
// workspace exists and belongs to the current user, and the response is the
// freshly evaluated status.
func (h *Handler) run(w http.ResponseWriter, r *http.Request, fn func(context.Context, string) (setupwizard.Status, error)) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	status, err := fn(r.Context(), workspaceID)
	if err != nil {
		h.respondError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "setup": status})
}

// stepID reads the step from the path. The value is only ever matched against
// the steps the workspace itself recorded, so it selects a step or selects
// nothing.
func (h *Handler) stepID(w http.ResponseWriter, r *http.Request) (string, bool) {
	stepID := strings.TrimSpace(r.PathValue("stepID"))
	if stepID == "" {
		_ = orihttp.RespondBadRequest(w, "step id is required")
		return "", false
	}
	if len(stepID) > 64 {
		_ = orihttp.RespondBadRequest(w, "step id is too long")
		return "", false
	}
	return stepID, true
}

// resolveWorkspace enforces existence and ownership. A workspace owned by
// someone else is reported as not found rather than forbidden, so the API does
// not confirm that another user's workspace exists.
func (h *Handler) resolveWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.service == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "Setup is not available."))
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
		logger.Warn("Failed to resolve current user for setup wizard", logger.Fields{"error": err})
		return false
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	return strings.EqualFold(owner, userID)
}

// respondError maps a service error onto a stable code and status. The codes
// are the contract the dialog branches on, and they are deliberately
// non-identifying: none of them carries a path, account, or domain detail.
func (h *Handler) respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, setupwizard.ErrNoWizard):
		_ = orihttp.RespondAPIError(w, http.StatusNotFound,
			orihttp.NewAPIError("no_setup_wizard", "This workspace has no setup wizard."))
	case errors.Is(err, setupwizard.ErrUnknownStep):
		_ = orihttp.RespondAPIError(w, http.StatusNotFound,
			orihttp.NewAPIError("unknown_step", "That setup step does not exist for this workspace."))
	case errors.Is(err, setupwizard.ErrUnsupportedSnapshot):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("unsupported_setup_wizard", "This workspace's recorded setup cannot be run by this version of Ori."))
	case errors.Is(err, setupwizard.ErrUnknownAdapter):
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("adapter_unavailable", "This setup step is unavailable in this build."))
	case errors.Is(err, setupwizard.ErrInvalidAction):
		_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
			orihttp.NewAPIError("invalid_action", errorMessage(err)))
	default:
		// Domain failures are logged server-side and reported generically: a raw
		// error can name a folder, a mailbox, or a connector.
		logger.Warn("Setup wizard request failed", logger.Fields{"error": err.Error()})
		_ = orihttp.RespondAPIError(w, http.StatusInternalServerError,
			orihttp.NewAPIError("setup_failed", "That setup step could not be completed."))
	}
}

// errorMessage returns an invalid-action message safe to show a user. These
// come from the service's own vocabulary (step ids and action names), never
// from a domain error.
func errorMessage(err error) string {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "That setup action is not available."
	}
	return message
}

// decodeOptionalBody reads a small, closed JSON body. An absent body is valid —
// most actions carry no data — and an unrecognized field is rejected rather
// than ignored, so a client cannot smuggle one past this boundary and have it
// silently dropped.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil {
		return true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxActionBodySize))
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "request body could not be read")
		return false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return true
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		_ = orihttp.RespondBadRequest(w, "invalid request body")
		return false
	}
	return true
}
