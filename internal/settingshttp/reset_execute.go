package settingshttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
)

// HandleReset admits a reviewed operation; it never deletes live files or clears
// owner caches. Local-host deployment still requires the normal origin/security
// middleware, plus the JSON, XMLHttpRequest and exact-confirmation checks here.
func (h *ResetHandler) HandleReset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Header.Get("X-Requested-With") != "XMLHttpRequest" || err != nil || mediaType != "application/json" {
		writeResetError(w, http.StatusBadRequest, "invalid_request", "A JSON XMLHttpRequest is required.", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxResetRequestSize)
	req, legacy, err := decodeResetRequest(r.Body)
	if err != nil {
		writeResetError(w, http.StatusBadRequest, "invalid_request", "Use distinct supported fields and the exact RESET confirmation.", nil)
		return
	}
	if legacy {
		writeResetError(w, http.StatusConflict, "preview_required", "Review a new reset preview before confirming. No data was deleted.", nil)
		return
	}
	if h.coordinator == nil {
		respondResetError(w, settingsreset.ErrLifecycleUnavailable, nil)
		return
	}
	// Once confirmed, a disconnected browser cannot unfreeze admission. Keep a
	// bounded independent context; the preview already supplies its recovery ID.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	op, err := h.coordinator.Stage(ctx, req)
	if err != nil {
		respondResetError(w, err, &op)
		return
	}
	status := http.StatusAccepted
	if op.State == settingsreset.StateBlocked || op.State == settingsreset.StateInterrupted {
		status = http.StatusConflict
	}
	writeResetOperation(w, status, op)
}

// decodeResetRequest rejects duplicate, unknown, case-variant and mixed forms,
// non-boolean legacy fields, trailing data, and null/missing confirmation.
func decodeResetRequest(body io.Reader) (settingsreset.ExecuteRequest, bool, error) {
	invalid := settingsreset.ErrInvalidRequest
	var req settingsreset.ExecuteRequest
	dec := json.NewDecoder(body)
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return req, false, invalid
	}
	seen := make(map[string]bool)
	legacy, selected := false, false
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return req, false, invalid
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return req, false, invalid
		}
		seen[key] = true
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return req, false, invalid
		}
		switch key {
		case "confirmation", "preview_id", "request_id":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil || value == "" {
				return req, false, invalid
			}
			switch key {
			case "confirmation":
				req.Confirmation = value
			case "preview_id":
				req.PreviewID = value
			case "request_id":
				req.RequestID = value
			}
		case "settings", "agents", "sessions", "onboarding":
			if string(raw) != "true" && string(raw) != "false" {
				return req, false, invalid
			}
			legacy = true
			selected = selected || string(raw) == "true"
		default:
			return req, false, invalid
		}
	}
	if _, err := dec.Token(); err != nil {
		return req, false, invalid
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) || req.Confirmation != "RESET" {
		return req, false, invalid
	}
	if legacy {
		if !selected || seen["preview_id"] || seen["request_id"] {
			return req, false, invalid
		}
	} else if !seen["preview_id"] || !seen["request_id"] || settingsreset.ValidateExecuteRequest(req) != nil {
		return req, false, invalid
	}
	return req, legacy, nil
}

func (h *ResetHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	const prefix = "/api/reset/operations/"
	if !strings.HasPrefix(r.URL.Path, prefix) || r.URL.RawQuery != "" {
		respondResetError(w, settingsreset.ErrInvalidRequest, nil)
		return
	}
	if h.coordinator == nil {
		respondResetError(w, settingsreset.ErrLifecycleUnavailable, nil)
		return
	}
	op, err := h.coordinator.Status(r.Context(), strings.TrimPrefix(r.URL.Path, prefix))
	if err != nil {
		respondResetError(w, err, nil)
		return
	}
	writeResetOperation(w, http.StatusOK, op)
}

func operationResponse(op settingsreset.Operation) ResetResponse {
	response := ResetResponse{Success: op.VerifiedComplete(), ResetItems: []string{}, Operation: &op}
	response.RequiresRestart = !response.Success && op.Restart.Mode == settingsreset.RestartProcessRelaunch
	for _, blocker := range op.Blockers {
		response.Errors = append(response.Errors, blocker.Message)
	}
	for _, id := range op.CompletedCategories() {
		switch id {
		case settingsreset.CategorySettings:
			response.ResetItems = append(response.ResetItems, "settings")
		case settingsreset.CategoryAgents:
			response.ResetItems = append(response.ResetItems, "agents")
		case settingsreset.CategoryAppRecords:
			response.ResetItems = append(response.ResetItems, "sessions")
		case settingsreset.CategorySetupSteps:
			response.ResetItems = append(response.ResetItems, "onboarding")
		}
	}
	return response
}

func writeResetOperation(w http.ResponseWriter, status int, op settingsreset.Operation) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	orihttp.WriteJSON(w, operationResponse(op))
}

func writeResetError(w http.ResponseWriter, status int, code, message string, op *settingsreset.Operation) {
	response := ResetResponse{ResetItems: []string{}, Code: code, Message: message, Errors: []string{message}}
	if op != nil && op.ID != "" {
		response = operationResponse(*op)
		response.Code, response.Message, response.Errors = code, message, []string{message}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	orihttp.WriteJSON(w, response)
}

func respondResetError(w http.ResponseWriter, err error, op *settingsreset.Operation) {
	status, code, message := http.StatusServiceUnavailable, "recovery_required", "Reset metadata or ownership is unavailable. Preserve the installation and recover before continuing."
	switch {
	case errors.Is(err, settingsreset.ErrInvalidRequest):
		status, code, message = http.StatusBadRequest, "invalid_request", settingsreset.ErrInvalidRequest.Error()
	case errors.Is(err, settingsreset.ErrOperationNotFound):
		status, code, message = http.StatusNotFound, "operation_unknown", settingsreset.ErrOperationNotFound.Error()
	case errors.Is(err, settingsreset.ErrOperationConflict):
		status, code, message = http.StatusConflict, "operation_conflict", settingsreset.ErrOperationConflict.Error()
	case errors.Is(err, settingsreset.ErrPreviewExpired):
		status, code, message = http.StatusConflict, "preview_expired", settingsreset.ErrPreviewExpired.Error()
	case errors.Is(err, settingsreset.ErrScopeChanged):
		status, code, message = http.StatusConflict, "scope_changed", settingsreset.ErrScopeChanged.Error()
	case errors.Is(err, settingsreset.ErrPreviewBlocked):
		status, code, message = http.StatusConflict, "preview_blocked", settingsreset.ErrPreviewBlocked.Error()
	case errors.Is(err, settingsreset.ErrActiveWork):
		status, code, message = http.StatusConflict, "active_work", settingsreset.ErrActiveWork.Error()
	case errors.Is(err, settingsreset.ErrLifecycleUnavailable):
		code, message = "lifecycle_unavailable", settingsreset.ErrLifecycleUnavailable.Error()
	case errors.Is(err, settingsreset.ErrAdmissionUncertain):
		code, message = "admission_uncertain", settingsreset.ErrAdmissionUncertain.Error()
	case errors.Is(err, resetstate.ErrRecordLimit):
		status, code, message = http.StatusConflict, "preview_limit", resetstate.ErrRecordLimit.Error()
	}
	writeResetError(w, status, code, message, op)
}
