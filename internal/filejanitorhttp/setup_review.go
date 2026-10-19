package filejanitorhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// SetupReview reads an owner-scoped proposal. POST selects which existing
// reference/monitoring terms to review; neither method grants or prepares work.
func (h *Handler) SetupReview(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable, orihttp.NewAPIError("setup_unavailable", "Folder setup is temporarily unavailable."))
		return
	}
	var request struct {
		ReferenceID string `json:"reference_id"`
		Monitoring  bool   `json:"monitoring"`
	}
	if r.Method == http.MethodPost {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			_ = orihttp.RespondBadRequest(w, "Choose an attached folder and whether to review monitoring.")
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF || len(request.ReferenceID) > 128 {
			_ = orihttp.RespondBadRequest(w, "Invalid folder review request.")
			return
		}
	}
	review, err := h.service.ReviewLinkedSetup(workspaceID, owner, request.ReferenceID, request.Monitoring)
	if err != nil {
		h.respondError(w, err, "Could not review attached-folder setup")
		return
	}
	// Owner-only paths must never be cached by a shared intermediary.
	w.Header().Set("Cache-Control", "no-store")
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "review": review})
}

// PrepareReview is an explicit owner confirmation, not a generic wizard or
// model action. The application HTTP reset middleware fences this finite work.
func (h *Handler) PrepareReview(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable, orihttp.NewAPIError("setup_unavailable", "Folder setup is temporarily unavailable."))
		return
	}
	var request struct {
		ReferenceID    string `json:"reference_id"`
		Monitoring     bool   `json:"monitoring"`
		ReviewRevision string `json:"review_revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		_ = orihttp.RespondBadRequest(w, "Confirm the current folder review.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || len(request.ReferenceID) > 128 || len(request.ReviewRevision) != 64 {
		_ = orihttp.RespondBadRequest(w, "Invalid folder review confirmation.")
		return
	}
	automation, _ := h.automation.(filejanitor.PreparationAutomation)
	prepared, err := h.service.PrepareLinkedSetup(filejanitor.LinkedPreparationRequest{
		WorkspaceID: workspaceID, OwnerUserID: owner, ReferenceID: request.ReferenceID,
		Monitoring: request.Monitoring, ReviewRevision: request.ReviewRevision,
	}, automation)
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		code, message, status := "setup_incomplete", "Setup stopped. Keep this workspace and retry the current preparation step.", http.StatusServiceUnavailable
		var domainError *filejanitor.SetupError
		if errors.As(err, &domainError) {
			code, message = domainError.Code, domainError.Message
			status = http.StatusConflict
			if domainError.Code == filejanitor.CodeWorkspaceMissing {
				status = http.StatusNotFound
			}
		}
		_ = orihttp.RespondAPIError(w, status, &orihttp.APIError{
			Code: code, Message: message, Details: map[string]any{"preparation": prepared},
		})
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "preparation": prepared})
}
