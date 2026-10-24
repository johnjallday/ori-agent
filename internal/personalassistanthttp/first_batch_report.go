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

// FirstBatchActioner is Not now and Retry on an inbox hire's report: its first
// batch (personalassistant.FolderDigestService).
type FirstBatchActioner interface {
	DismissFirstBatch(ctx context.Context, userID, offerID string) error
	RetryFirstBatch(ctx context.Context, userID, offerID string) (personalassistant.FolderFirstBatch, error)
}

// SetFirstBatchActions wires an inbox hire report's actions.
func (h *Handler) SetFirstBatchActions(actions FirstBatchActioner) {
	if h != nil {
		h.firstBatch = actions
	}
}

// firstBatchRequest reads the hire's offer id from the path and refuses any
// body but an empty object: the report is found among the user's own setups.
func (h *Handler) firstBatchRequest(w http.ResponseWriter, r *http.Request) (userID, offerID string, ok bool) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return "", "", false
	}
	if h == nil || h.firstBatch == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "The first batch's report is unavailable")
		return "", "", false
	}
	offerID = strings.TrimSpace(r.PathValue("offerID"))
	if offerID == "" || len(offerID) > 128 || strings.ContainsAny(offerID, "/\\\x00") {
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
	return userID, offerID, ok
}

// DismissFirstBatch serves POST
// /api/personal-assistant/first-batch/{offerID}/dismiss: Not now.
func (h *Handler) DismissFirstBatch(w http.ResponseWriter, r *http.Request) {
	userID, offerID, ok := h.firstBatchRequest(w, r)
	if !ok {
		return
	}
	if err := h.firstBatch.DismissFirstBatch(r.Context(), userID, offerID); err != nil {
		writeFirstLookError(w, err, personalassistant.FirstLookStarted{})
		return
	}
	orihttp.Success(w, map[string]any{"dismissed": true})
}

// RetryFirstBatch serves POST /api/personal-assistant/first-batch/{offerID}/retry:
// the first scan again, for an inbox hire whose first scan did not complete.
func (h *Handler) RetryFirstBatch(w http.ResponseWriter, r *http.Request) {
	userID, offerID, ok := h.firstBatchRequest(w, r)
	if !ok {
		return
	}
	batch, err := h.firstBatch.RetryFirstBatch(r.Context(), userID, offerID)
	switch {
	case errors.Is(err, personalassistant.ErrFirstBatchNotFailed):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": "This first scan did not fail", "code": "not_failed"})
		return
	case errors.Is(err, personalassistant.ErrFirstBatchRetryBusy):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": "This first scan is already being retried", "code": "retry_running"})
		return
	case err != nil:
		writeFirstLookError(w, err, personalassistant.FirstLookStarted{})
		return
	}
	orihttp.Success(w, map[string]any{"retried": true, "outcome": batch.Outcome})
}
