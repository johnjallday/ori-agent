package reaperhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/reaper"
)

const maxActionRunBody = 4 << 10

type ActionRunner interface {
	RunAction(context.Context, string, reaper.ProjectSource) (reaper.State, error)
}

type actionRunRequest struct {
	Confirmed bool `json:"confirmed"`
}

// ActionRunResponse embeds the same live state shape returned by GET state and
// adds the outcome the console must surface. ErrorReason is intentionally a
// stable, non-sensitive phrase; transport endpoints and ports never enter it.
type ActionRunResponse struct {
	reaper.State
	ActionID    string `json:"action_id"`
	Outcome     string `json:"outcome"`
	Code        string `json:"code,omitempty"`
	ErrorReason string `json:"error_reason,omitempty"`
}

func (h *Handler) GetActions(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	_, applies, err := h.projectSource(ws.ID)
	if err != nil {
		h.respondUnavailable(w)
		return
	}
	if !applies {
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("reaper_not_selected", "Live REAPER control is not selected for this workspace."))
		return
	}
	actions, err := h.listActions()
	if err != nil {
		_ = orihttp.RespondAPIError(w, http.StatusBadGateway,
			orihttp.NewAPIError("reaper_catalog_unavailable", "REAPER actions could not be read."))
		return
	}
	_ = orihttp.RespondSuccess(w, actions)
}

func (h *Handler) RunAction(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	request, ok := decodeActionRunRequest(w, r)
	if !ok {
		return
	}
	response, status := h.runAction(r.Context(), ws.ID, r.PathValue("actionID"), request.Confirmed)
	_ = orihttp.RespondJSON(w, status, response)
}

// runAction is the one validation, confirmation, and execution path shared by
// the HTTP console and agent tools. Callers may choose how to render the
// returned status, but none can bypass its policy.
func (h *Handler) runAction(ctx context.Context, workspaceID, requestedID string, confirmed bool) (ActionRunResponse, int) {
	actionID := strings.TrimSpace(requestedID)
	response := ActionRunResponse{ActionID: actionID, Outcome: "error"}
	project, applies, err := h.projectSource(workspaceID)
	if err != nil {
		response.Code = "reaper_unavailable"
		response.ErrorReason = "Live REAPER control is not available for this workspace."
		return response, http.StatusServiceUnavailable
	}
	if !applies {
		response.Code = "reaper_not_selected"
		response.ErrorReason = "Live REAPER control is not selected for this workspace."
		return response, http.StatusConflict
	}
	action, found, catalogErr := h.findAction(actionID)
	if catalogErr != nil {
		response.Code = "reaper_catalog_unavailable"
		response.ErrorReason = "REAPER actions could not be read."
		return response, http.StatusBadGateway
	}
	if !found {
		if !reaper.ValidRawCommandID(actionID) {
			response.Code = "invalid_reaper_command_id"
			response.ErrorReason = "Use a decimal command ID or _RS followed by hexadecimal characters."
			return response, http.StatusBadRequest
		}
		action = reaper.Action{
			ID: actionID, Label: "Raw command " + actionID, Source: "raw",
			Mutates: true, NeedsConfirmation: true,
		}
	}
	response.ActionID = action.ID
	if action.NeedsConfirmation && !confirmed {
		response.Outcome = "confirmation_required"
		response.Code = "reaper_confirmation_required"
		response.ErrorReason = "Confirm this project change before running it."
		return response, http.StatusConflict
	}
	if action.Source == reaper.ActionSourceCustom {
		return h.runCustomScript(ctx, project, action)
	}
	runner, ok := h.client.(ActionRunner)
	if !ok {
		response.Code = "reaper_unavailable"
		response.ErrorReason = "Live REAPER control is not available for this workspace."
		return response, http.StatusServiceUnavailable
	}
	state, runErr := runner.RunAction(ctx, action.ID, project)
	state.Applies = true
	response.State = state
	response.Outcome = "ok"
	if runErr == nil {
		return response, http.StatusOK
	}
	response.Outcome = "error"
	status := http.StatusBadGateway
	switch {
	case errors.Is(runErr, reaper.ErrActionDisconnected):
		status = http.StatusConflict
		response.Code = "reaper_disconnected"
		response.ErrorReason = "REAPER is not connected. Nothing was run."
	case errors.Is(runErr, reaper.ErrInvalidCommandID):
		status = http.StatusBadRequest
		response.Code = "invalid_reaper_command_id"
		response.ErrorReason = "The REAPER command ID is invalid."
	default:
		response.Code = "reaper_action_failed"
		response.ErrorReason = "REAPER did not run the action."
	}
	logger.Warn("Live REAPER action failed", logger.Fields{"category": "reaper_action_failed"})
	return response, status
}

func (h *Handler) runCustomScript(ctx context.Context, project reaper.ProjectSource, action reaper.Action) (ActionRunResponse, int) {
	response := ActionRunResponse{ActionID: action.ID, Outcome: "error"}
	if h == nil || h.scriptLibrary == nil || h.scriptRunner == nil || h.client == nil {
		response.Code = "reaper_runner_unavailable"
		response.ErrorReason = "The REAPER script runner is unavailable."
		return response, http.StatusServiceUnavailable
	}
	script, err := h.scriptLibrary.Read(action.ID)
	if err != nil {
		response.Code = "reaper_script_not_found"
		response.ErrorReason = "The custom REAPER script was not found."
		return response, http.StatusNotFound
	}
	runResult, runErr := h.scriptRunner.RunScript(ctx, script.Code)
	state, stateErr := h.client.ReadState(ctx, project)
	state.Applies = true
	response.State = state
	if runErr == nil && stateErr == nil && runResult.Outcome == "ok" {
		response.Outcome = "ok"
		return response, http.StatusOK
	}
	response.Code = "reaper_script_failed"
	response.ErrorReason = strings.TrimSpace(runResult.ErrorText)
	if response.ErrorReason == "" {
		response.ErrorReason = "The custom REAPER script failed."
	}
	status := http.StatusBadGateway
	if errors.Is(runErr, reaper.ErrActionDisconnected) {
		status = http.StatusConflict
		response.Code = "reaper_disconnected"
	}
	return response, status
}

func (h *Handler) listActions() ([]reaper.Action, error) {
	if h == nil || h.catalog == nil {
		return reaper.BuiltinActions(), nil
	}
	return h.catalog.List()
}

func (h *Handler) findAction(id string) (reaper.Action, bool, error) {
	if h == nil || h.catalog == nil {
		action, found := reaper.BuiltinAction(id)
		return action, found, nil
	}
	return h.catalog.Find(id)
}

func decodeActionRunRequest(w http.ResponseWriter, r *http.Request) (actionRunRequest, bool) {
	var request actionRunRequest
	if r.Body == nil || r.ContentLength == 0 {
		return request, true
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxActionRunBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		_ = orihttp.RespondBadRequest(w, "invalid action request")
		return actionRunRequest{}, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		_ = orihttp.RespondBadRequest(w, "invalid action request")
		return actionRunRequest{}, false
	}
	return request, true
}
