// Gateway resolution: the CalendarMCPGateway's ownership/readiness checkpoint
// (FR24/FR25). Every read and mutation route in this package resolves a
// *gatewayContext through resolveGateway before touching the connector, so
// the browser never talks to an MCP server directly and never sees connector
// credentials -- only the sanitized, mapped data this package returns.
package calendarhttp

import (
	"context"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"

	"github.com/johnjallday/ori-agent/internal/calendar"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// gatewayError is a stable, machine-readable failure from gateway resolution
// or a gateway route. Code reuses calendar.SetupState values whenever the
// failure corresponds to an unfinished/broken setup state, so the frontend
// can drive the same "finish setup" UI it already has for the setup flow
// (task 4.2: "stable degraded/error codes").
type gatewayError struct {
	status  int
	code    string
	message string
}

func (e *gatewayError) Error() string { return e.message }

func writeGatewayError(w http.ResponseWriter, err *gatewayError) {
	if err == nil {
		orihttp.InternalError(w, "unknown gateway error")
		return
	}
	if werr := orihttp.RespondAPIError(w, err.status, orihttp.NewAPIError(err.code, err.message)); werr != nil {
		orihttp.InternalError(w, "failed to write error response")
	}
}

func gwBadRequest(message string) *gatewayError {
	return &gatewayError{status: http.StatusBadRequest, code: orihttp.ErrCodeBadRequest, message: message}
}

func gwForbidden(message string) *gatewayError {
	return &gatewayError{status: http.StatusForbidden, code: orihttp.ErrCodeForbidden, message: message}
}

func gwNotFound(message string) *gatewayError {
	return &gatewayError{status: http.StatusNotFound, code: orihttp.ErrCodeNotFound, message: message}
}

func gwInternal(message string) *gatewayError {
	return &gatewayError{status: http.StatusInternalServerError, code: orihttp.ErrCodeInternal, message: message}
}

// gwSetupState reports an unfinished/degraded setup state using the exact
// wire values the setup flow already exposes (see calendar.SetupState).
// state==SetupDegraded maps to 503 (retryable); every other non-ready state
// maps to 409 (the client must finish setup before this route is usable).
func gwSetupState(state calendar.SetupState, message string) *gatewayError {
	status := http.StatusConflict
	if state == calendar.SetupDegraded {
		status = http.StatusServiceUnavailable
	}
	return &gatewayError{status: status, code: string(state), message: message}
}

// gatewayContext is the resolved, readiness-checked target of a gateway
// request.
type gatewayContext struct {
	UserID    string
	Workspace *agentworkspace.Workspace
	Binding   *agentworkspace.MCPBinding
	Mapping   agentworkspace.CapabilityMapping
}

// resolveGateway performs the FR25 checkpoint: the workspace exists and is
// owned by the current user, it carries an enabled calendar connector binding
// mapped to the calendar contract, and the bound MCP server is currently
// connected (authenticated, not degraded). It never returns connector
// credentials -- callers get back only the binding/mapping needed to invoke
// mapped operations through h.toolCallerFor.
func (h *Handler) resolveGateway(ctx context.Context, workspaceID string) (*gatewayContext, *gatewayError) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, gwBadRequest("workspace_id is required")
	}
	if h.folders == nil {
		return nil, gwInternal("workspace storage is unavailable")
	}

	userID, err := h.currentUserID(ctx)
	if err != nil {
		return nil, gwInternal("failed to resolve current user: " + err.Error())
	}

	ws, err := h.folders.GetFolderWorkspace(workspaceID)
	if err != nil || ws == nil {
		return nil, gwNotFound("workspace not found")
	}

	owner := strings.TrimSpace(ws.OwnerUserID)
	if owner == "" {
		owner = userprofile.LocalUserID
	}
	if !strings.EqualFold(owner, userID) {
		return nil, gwForbidden("you do not have access to this workspace")
	}

	binding, hasBinding := findCalendarBinding(ws)
	if !hasBinding || binding == nil || !binding.Enabled {
		return nil, gwSetupState(calendar.SetupConnectorMissing, "no calendar connector is configured for this workspace")
	}

	mapping, hasMapping := binding.FindCapabilityMapping(calendar.CapabilityKey)
	if !hasMapping || calendar.ValidateMapping(mapping) != nil {
		return nil, gwSetupState(calendar.SetupMappingRequired, "calendar setup is not complete")
	}

	status := h.connectorStatusFn(binding.ServerName)
	switch {
	case !status.Present:
		return nil, gwSetupState(calendar.SetupConnectorMissing, "calendar connector is no longer configured")
	case status.Degraded:
		return nil, gwSetupState(calendar.SetupDegraded, "calendar connector is temporarily unavailable")
	case status.AuthRequired, !status.Connected:
		return nil, gwSetupState(calendar.SetupAuthRequired, "calendar connector needs to be reconnected")
	}

	return &gatewayContext{UserID: userID, Workspace: ws, Binding: binding, Mapping: mapping}, nil
}
