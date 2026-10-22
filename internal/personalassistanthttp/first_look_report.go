package personalassistanthttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// FirstLookActioner is Start this and Not now on a hired agent's first-look
// report (personalassistant.FirstLookActions).
type FirstLookActioner interface {
	StartSuggestion(ctx context.Context, userID, lookTaskID string) (personalassistant.FirstLookStarted, error)
	Dismiss(ctx context.Context, userID, lookTaskID string) error
}

// SetFirstLookActions wires the report's two actions.
func (h *Handler) SetFirstLookActions(actions FirstLookActioner) {
	if h != nil {
		h.firstLook = actions
	}
}

// firstLookRequest reads the look's task id from the path and refuses any body
// but an empty object: the suggestion is read again from the look itself, so a
// request has nothing else to say.
func (h *Handler) firstLookRequest(w http.ResponseWriter, r *http.Request) (userID, lookTaskID string, ok bool) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return "", "", false
	}
	if h == nil || h.firstLook == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "The first look's report is unavailable")
		return "", "", false
	}
	lookTaskID = strings.TrimSpace(r.PathValue("taskID"))
	if lookTaskID == "" || len(lookTaskID) > 200 || strings.ContainsAny(lookTaskID, "/\\\x00") {
		orihttp.BadRequest(w, "A first look is required")
		return "", "", false
	}
	if r.Body != nil {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32))
		if trimmed := strings.TrimSpace(string(body)); err != nil || trimmed != "" && trimmed != "{}" {
			orihttp.BadRequest(w, "This action does not accept request data")
			return "", "", false
		}
	}
	userID, ok = h.currentUserID(w, r)
	return userID, lookTaskID, ok
}

// writeFirstLookError answers a refused action with a code the card can word.
func writeFirstLookError(w http.ResponseWriter, err error, started personalassistant.FirstLookStarted) {
	switch {
	case errors.Is(err, personalassistant.ErrFirstLookNotFound):
		orihttp.NotFound(w, "That first look is not one of yours")
	case errors.Is(err, personalassistant.ErrFirstLookActedOn):
		body := map[string]any{"error": "This suggestion was already started or put aside", "code": "already_acted"}
		if started.TaskID != "" {
			body["task"] = started
		}
		_ = orihttp.RespondJSON(w, http.StatusConflict, body)
	case errors.Is(err, personalassistant.ErrFirstLookNoSuggestion):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": "This first look suggested no task", "code": "no_suggestion"})
	case errors.Is(err, personalassistant.ErrFirstLookNotFinished):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": "This first look has not finished", "code": "not_finished"})
	default:
		orihttp.ServiceUnavailable(w, "The first look's report could not be updated just now")
	}
}

// StartFirstLookSuggestion serves POST
// /api/personal-assistant/first-look/{taskID}/start-suggestion: Start this.
// The click is the confirmation; the task is created in the look's workspace,
// assigned to the agent that made the look, and started.
func (h *Handler) StartFirstLookSuggestion(w http.ResponseWriter, r *http.Request) {
	userID, lookTaskID, ok := h.firstLookRequest(w, r)
	if !ok {
		return
	}
	started, err := h.firstLook.StartSuggestion(r.Context(), userID, lookTaskID)
	if err != nil && started.TaskID != "" && !errors.Is(err, personalassistant.ErrFirstLookActedOn) {
		// Created, but the start was refused: the task waits, Ready, in its
		// workspace. The report has done its part.
		orihttp.Success(w, map[string]any{"started": false, "task": started})
		return
	}
	if err != nil {
		writeFirstLookError(w, err, started)
		return
	}
	orihttp.Success(w, map[string]any{"started": true, "task": started})
}

// DismissFirstLookSuggestion serves POST
// /api/personal-assistant/first-look/{taskID}/dismiss: Not now.
func (h *Handler) DismissFirstLookSuggestion(w http.ResponseWriter, r *http.Request) {
	userID, lookTaskID, ok := h.firstLookRequest(w, r)
	if !ok {
		return
	}
	if err := h.firstLook.Dismiss(r.Context(), userID, lookTaskID); err != nil {
		writeFirstLookError(w, err, personalassistant.FirstLookStarted{})
		return
	}
	orihttp.Success(w, map[string]any{"dismissed": true})
}
