// Package runtimecapabilityhttp serves the workspace runtime-capability API.
// Requests may select only records already present in the workspace snapshot:
// a mode, requirement, action token, and stable agent instance. There is no
// request field for an adapter, path, endpoint, port, command, script, or probe.
package runtimecapabilityhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	maxBodySize      = 4 << 10
	maxTokenLength   = workspace.MaxRuntimeIdentifierLength
	maxAgentIDLength = 128
)

type WorkspaceLookup interface {
	Get(id string) (*workspace.Workspace, error)
}

type GrantDelegator interface {
	Grant(context.Context, string, string, string) (runtimecapability.Status, error)
	Revoke(context.Context, string, string, string) (runtimecapability.Status, error)
}

type Handler struct {
	service  *runtimecapability.Service
	lookup   WorkspaceLookup
	provider userprofile.UserProvider
	grants   GrantDelegator
}

func NewHandler(service *runtimecapability.Service, lookup WorkspaceLookup, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{service: service, lookup: lookup, provider: provider}
}

func (h *Handler) SetGrantDelegator(grants GrantDelegator) {
	if h != nil {
		h.grants = grants
	}
}

func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	h.run(w, r, func(ctx context.Context, workspaceID string) (runtimecapability.Status, error) {
		return h.service.Status(ctx, workspaceID)
	})
}

func (h *Handler) SelectMode(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPut) {
		return
	}
	var body struct {
		ModeID string `json:"mode_id"`
	}
	if !decodeClosedBody(w, r, &body, false) {
		return
	}
	modeID := workspace.NormalizeRuntimeIdentifier(body.ModeID)
	if modeID == "" {
		_ = orihttp.RespondBadRequest(w, "mode_id is invalid")
		return
	}
	h.run(w, r, func(ctx context.Context, workspaceID string) (runtimecapability.Status, error) {
		return h.service.SelectMode(ctx, workspaceID, modeID)
	})
}

func (h *Handler) Recheck(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !decodeClosedBody(w, r, &struct{}{}, true) {
		return
	}
	h.run(w, r, func(ctx context.Context, workspaceID string) (runtimecapability.Status, error) {
		return h.service.Recheck(ctx, workspaceID)
	})
}

func (h *Handler) ConfirmAction(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !decodeClosedBody(w, r, &struct{}{}, true) {
		return
	}
	requirementKey, actionToken, ok := requirementAndAction(w, r)
	if !ok {
		return
	}
	h.run(w, r, func(ctx context.Context, workspaceID string) (runtimecapability.Status, error) {
		return h.service.ConfirmAction(ctx, workspaceID, requirementKey, actionToken)
	})
}

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !decodeClosedBody(w, r, &struct{}{}, true) {
		return
	}
	requirementKey, ok := requirementKey(w, r)
	if !ok {
		return
	}
	h.run(w, r, func(ctx context.Context, workspaceID string) (runtimecapability.Status, error) {
		return h.service.Verify(ctx, workspaceID, requirementKey)
	})
}

func (h *Handler) Grant(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	h.grantTransition(w, r, false)
}

func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	h.grantTransition(w, r, true)
}

func (h *Handler) grantTransition(w http.ResponseWriter, r *http.Request, revoke bool) {
	var body struct {
		AgentInstanceID string `json:"agent_instance_id"`
	}
	if !decodeClosedBody(w, r, &body, false) {
		return
	}
	agentID := strings.TrimSpace(body.AgentInstanceID)
	if agentID == "" || len(agentID) > maxAgentIDLength {
		_ = orihttp.RespondBadRequest(w, "agent_instance_id is invalid")
		return
	}
	requirementKey, ok := requirementKey(w, r)
	if !ok {
		return
	}
	if h.grants == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("grant_unavailable", "Runtime capability grants are not available in this build."))
		return
	}
	h.run(w, r, func(ctx context.Context, workspaceID string) (runtimecapability.Status, error) {
		if revoke {
			return h.grants.Revoke(ctx, workspaceID, requirementKey, agentID)
		}
		return h.grants.Grant(ctx, workspaceID, requirementKey, agentID)
	})
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request, transition func(context.Context, string) (runtimecapability.Status, error)) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	status, err := transition(r.Context(), workspaceID)
	if err != nil {
		h.respondError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "runtime": status})
}

func (h *Handler) resolveWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.service == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "Runtime capabilities are not available."))
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
	if err != nil || ws == nil || !h.ownedByCurrentUser(r.Context(), ws) {
		_ = orihttp.RespondNotFound(w, "workspace not found")
		return "", false
	}
	return workspaceID, true
}

func (h *Handler) ownedByCurrentUser(ctx context.Context, ws *workspace.Workspace) bool {
	owner := strings.TrimSpace(ws.OwnerUserID)
	if owner == "" {
		owner = userprofile.LocalUserID
	}
	userID, err := h.provider.CurrentUserID(ctx)
	if err != nil {
		logger.Warn("Failed to resolve current user for runtime capability", logger.Fields{"category": "user_lookup_failed"})
		return false
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	return strings.EqualFold(owner, userID)
}

func requirementKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := workspace.NormalizeRuntimeIdentifier(r.PathValue("requirementKey"))
	if key == "" || len(key) > maxTokenLength {
		_ = orihttp.RespondBadRequest(w, "runtime requirement key is invalid")
		return "", false
	}
	return key, true
}

func requirementAndAction(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	key, ok := requirementKey(w, r)
	if !ok {
		return "", "", false
	}
	token := workspace.NormalizeRuntimeIdentifier(r.PathValue("actionToken"))
	if token == "" || len(token) > maxTokenLength {
		_ = orihttp.RespondBadRequest(w, "runtime action token is invalid")
		return "", "", false
	}
	return key, token, true
}

func (h *Handler) respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runtimecapability.ErrNoRuntimeContract):
		_ = orihttp.RespondNotFound(w, "This workspace declares no runtime requirements.")
	case errors.Is(err, runtimecapability.ErrUnknownMode), errors.Is(err, runtimecapability.ErrModeRequired):
		_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
			orihttp.NewAPIError("invalid_mode", "That operating mode is not available for this workspace."))
	case errors.Is(err, runtimecapability.ErrUnknownRequirement):
		_ = orihttp.RespondNotFound(w, "That runtime requirement does not exist for the selected mode.")
	case errors.Is(err, runtimecapability.ErrUnsupportedSnapshot):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("unsupported_runtime_contract", "This workspace's recorded runtime contract is not supported by this version of Ori."))
	case errors.Is(err, runtimecapability.ErrUnknownAdapter):
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("adapter_unavailable", "This runtime requirement is unavailable in this build."))
	case errors.Is(err, runtimecapability.ErrUnknownAction):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("action_unavailable", "That repair action is no longer available. Check the requirement again."))
	case errors.Is(err, runtimecapability.ErrVerificationFailed):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("verification_failed", "Runtime verification did not complete. Check the current requirement and try its next action."))
	default:
		logger.Warn("Runtime capability request failed", logger.Fields{"category": "runtime_request_failed"})
		_ = orihttp.RespondAPIError(w, http.StatusInternalServerError,
			orihttp.NewAPIError("runtime_failed", "The runtime capability request could not be completed."))
	}
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	_ = orihttp.RespondMethodNotAllowed(w)
	return false
}

func decodeClosedBody(w http.ResponseWriter, r *http.Request, target any, optional bool) bool {
	if r.Body == nil {
		if optional {
			return true
		}
		_ = orihttp.RespondBadRequest(w, "request body is required")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "request body could not be read")
		return false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		if optional {
			return true
		}
		_ = orihttp.RespondBadRequest(w, "request body is required")
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		_ = orihttp.RespondBadRequest(w, "invalid request body")
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		_ = orihttp.RespondBadRequest(w, "invalid request body")
		return false
	}
	return true
}
