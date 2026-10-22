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

// HireLookActioner is Start this, Not now and Retry on the report of an agent
// hired from the Agents page (personalassistant.HireLookActions).
type HireLookActioner interface {
	StartSuggestion(ctx context.Context, userID, hireID string) (personalassistant.FirstLookStarted, error)
	Dismiss(ctx context.Context, userID, hireID string) error
	Retry(ctx context.Context, userID, hireID string) (personalassistant.HireRecord, error)
}

// SetHireLookActions wires the hire report's actions.
func (h *Handler) SetHireLookActions(actions HireLookActioner) {
	if h != nil {
		h.hireLook = actions
	}
}

// hireLookRequest reads the hire's id from the path and refuses any body but
// an empty object: the suggestion is read again from the look's own run, so a
// request has nothing else to say.
func (h *Handler) hireLookRequest(w http.ResponseWriter, r *http.Request) (userID, hireID string, ok bool) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return "", "", false
	}
	if h == nil || h.hireLook == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "The hire's report is unavailable")
		return "", "", false
	}
	hireID = strings.TrimSpace(r.PathValue("hireID"))
	if hireID == "" || len(hireID) > 128 || strings.ContainsAny(hireID, "/\\\x00") {
		orihttp.BadRequest(w, "A hire is required")
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
	return userID, hireID, ok
}

// StartHireLookSuggestion serves POST
// /api/personal-assistant/hires/{hireID}/start-suggestion: Start this.
func (h *Handler) StartHireLookSuggestion(w http.ResponseWriter, r *http.Request) {
	userID, hireID, ok := h.hireLookRequest(w, r)
	if !ok {
		return
	}
	started, err := h.hireLook.StartSuggestion(r.Context(), userID, hireID)
	if err != nil && started.TaskID != "" && !errors.Is(err, personalassistant.ErrFirstLookActedOn) {
		// Ready and assigned, but the start was refused: the task waits in its
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

// DismissHireLook serves POST /api/personal-assistant/hires/{hireID}/dismiss:
// Not now.
func (h *Handler) DismissHireLook(w http.ResponseWriter, r *http.Request) {
	userID, hireID, ok := h.hireLookRequest(w, r)
	if !ok {
		return
	}
	if err := h.hireLook.Dismiss(r.Context(), userID, hireID); err != nil {
		writeFirstLookError(w, err, personalassistant.FirstLookStarted{})
		return
	}
	orihttp.Success(w, map[string]any{"dismissed": true})
}

// RetryHireLook serves POST /api/personal-assistant/hires/{hireID}/retry:
// one more first look for a hire whose look did not finish.
func (h *Handler) RetryHireLook(w http.ResponseWriter, r *http.Request) {
	userID, hireID, ok := h.hireLookRequest(w, r)
	if !ok {
		return
	}
	record, err := h.hireLook.Retry(r.Context(), userID, hireID)
	if errors.Is(err, personalassistant.ErrHireLookNotFailed) {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": "This first look has not failed", "code": "not_failed"})
		return
	}
	if err != nil && record.ID == "" {
		writeFirstLookError(w, err, personalassistant.FirstLookStarted{})
		return
	}
	if err != nil {
		orihttp.ServiceUnavailable(w, "The first look could not be retried just now")
		return
	}
	body := map[string]any{"retried": record.State == personalassistant.HireStateLooking, "look_state": string(record.State)}
	if record.Failure != "" {
		body["look_failure"] = record.Failure
	}
	orihttp.Success(w, body)
}
