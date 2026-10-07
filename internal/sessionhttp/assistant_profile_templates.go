package sessionhttp

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// The templates row of the Home profile card. Listing an application's
// templates is the one thing the profile reads from disk, so it happens only
// after the owner reviewed exactly what will be read, and can be taken back in
// one action. The read itself is a directory listing made by the
// application's own plugin: host code opens no folder and no file.

// ReviewAssistantProfileTemplates handles
// POST /api/workspaces/{workspaceID}/assistant-program/profile/templates/review
// {request_id}. It returns a review id, the application, its template folder
// names and the sentence the dialog shows. It reads nothing and stores nothing.
func (h *Handler) ReviewAssistantProfileTemplates(w http.ResponseWriter, r *http.Request) {
	owner, homeID, ok := h.homeProfileOwner(w, r)
	if !ok {
		return
	}
	var request struct {
		RequestID string `json:"request_id"`
	}
	if !decodeStrictAction(w, r, &request, "Invalid profile request") {
		return
	}
	review, err := h.HomeProfiles().TemplatesReview(owner, homeID, request.RequestID)
	if err != nil {
		respondHomeProfileError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

// CommitAssistantProfileTemplates handles
// POST /api/workspaces/{workspaceID}/assistant-program/profile/templates/commit
// {request_id, review_id}. It records the owner's consent and lists the
// templates once. 409 plugin_operation_unavailable when no installed project
// plugin offers the operation; 409 home_profile_changed when the review no
// longer describes the Home.
func (h *Handler) CommitAssistantProfileTemplates(w http.ResponseWriter, r *http.Request) {
	owner, homeID, ok := h.homeProfileOwner(w, r)
	if !ok {
		return
	}
	var request struct {
		RequestID string `json:"request_id"`
		ReviewID  string `json:"review_id"`
	}
	if !decodeStrictAction(w, r, &request, "Invalid profile request") {
		return
	}
	if strings.TrimSpace(request.ReviewID) == "" || len(request.ReviewID) > 120 {
		_ = orihttp.RespondBadRequest(w, "Review the templates read first")
		return
	}
	view, err := h.HomeProfiles().TemplatesCommit(r.Context(), owner, homeID, request.RequestID, request.ReviewID)
	if err != nil {
		respondHomeProfileError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}

// ForgetAssistantProfileTemplates handles
// POST /api/workspaces/{workspaceID}/assistant-program/profile/templates/forget
// {request_id}. It clears the list and withdraws the consent in one write.
func (h *Handler) ForgetAssistantProfileTemplates(w http.ResponseWriter, r *http.Request) {
	owner, homeID, ok := h.homeProfileOwner(w, r)
	if !ok {
		return
	}
	var request struct {
		RequestID string `json:"request_id"`
	}
	if !decodeStrictAction(w, r, &request, "Invalid profile request") {
		return
	}
	view, err := h.HomeProfiles().TemplatesForget(owner, homeID, request.RequestID)
	if err != nil {
		respondHomeProfileError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}
