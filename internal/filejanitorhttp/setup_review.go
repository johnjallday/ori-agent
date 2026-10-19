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
	w.Header().Set("Cache-Control", "no-store")
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
		respondReviewedSetupError(w, err, "setup_unavailable", "Ori could not read the current folder review. Keep this workspace and try again.")
		return
	}
	// Owner-only paths must never be cached by a shared intermediary.
	w.Header().Set("Cache-Control", "no-store")
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "review": review})
}

// StopPreparation is an explicit defer action for the exact accepted operation.
// It never revokes saved access or removes an adopted link/Filed directory.
func (h *Handler) StopPreparation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "Your workspace identity is unavailable.")
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		_ = orihttp.RespondBadRequest(w, "Choose the current preparation to stop.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || len(request.OperationID) != 36 {
		_ = orihttp.RespondBadRequest(w, "Invalid preparation identity.")
		return
	}
	status, err := h.service.StopLinkedPreparation(workspaceID, owner, request.OperationID)
	if err != nil {
		respondReviewedSetupError(w, err, "stop_unavailable", "Preparation could not be stopped. Open this workspace's current settings.")
		return
	}
	status = h.syncAutomationAndRefresh(workspaceID, status)
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "status": status})
}

// These new boundaries deliberately do not log raw filesystem/store errors.
// Unexpected causes may contain owner paths; public repair uses bounded copy.
func respondReviewedSetupError(w http.ResponseWriter, err error, code, message string) {
	status := http.StatusServiceUnavailable
	var domainError *filejanitor.SetupError
	if errors.As(err, &domainError) {
		code, message, status = domainError.Code, reviewedSetupMessage(domainError), http.StatusConflict
		if code == filejanitor.CodeWorkspaceMissing {
			status = http.StatusNotFound
		}
	}
	_ = orihttp.RespondAPIError(w, status, orihttp.NewAPIError(code, message))
}

func reviewedSetupMessage(err *filejanitor.SetupError) string {
	if err.Code == filejanitor.CodeFolderConflict {
		return "Another workspace already manages an overlapping folder. Choose another folder or open its current setup."
	}
	return err.Message
}

// PrepareReview is an explicit owner confirmation, not a generic wizard or
// model action. The application HTTP reset middleware fences this finite work.
func (h *Handler) PrepareReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
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
		Context: r.Context(), WorkspaceID: workspaceID, OwnerUserID: owner, ReferenceID: request.ReferenceID,
		Monitoring: request.Monitoring, ReviewRevision: request.ReviewRevision,
	}, automation)
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		code, message := filejanitor.PreparationFailureSummary(prepared.Stage)
		status := http.StatusServiceUnavailable
		var domainError *filejanitor.SetupError
		if errors.As(err, &domainError) {
			code, message = domainError.Code, reviewedSetupMessage(domainError)
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
