package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspaceupdates"
)

// SetWorkspaceWelcomeCreated installs host-only durable enrollment at successful
// creation. Nil is allowed in reduced hosts/tests that do not provide updates.
func (h *Handler) SetWorkspaceWelcomeCreated(created func(context.Context, string) error) {
	h.workspaceWelcomeCreated = created
}

// SetWorkspacePreparationCompleted composes the existing wizard readiness
// projection after the user's explicit File Janitor preparation succeeds. It
// never belongs on a status GET or a background delivery retry.
func (h *Handler) SetWorkspacePreparationCompleted(completed func(context.Context, string) error) {
	h.workspacePreparationCompleted = completed
}

func (h *Handler) SetWorkspaceUpdates(service *workspaceupdates.Service) {
	h.workspaceUpdates = service
}

func updateError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusServiceUnavailable, "updates_unavailable", "Workspace updates could not be verified. Try again."
	switch {
	case errors.Is(err, workspaceupdates.ErrNotFound):
		status, code, message = http.StatusNotFound, "updates_not_found", "Workspace updates are unavailable."
	case errors.Is(err, session.ErrWelcomeUnavailable), errors.Is(err, session.ErrWorkspaceVersionConflict):
		status, code, message = http.StatusConflict, "update_changed", "This conversation changed. Refresh its current updates."
	case errors.Is(err, session.ErrInvalidID):
		status, code, message = http.StatusBadRequest, "invalid_update", "Name the exact saved message and revision."
	}
	_ = orihttp.RespondAPIError(w, status, &orihttp.APIError{Code: code, Message: message})
}

func (h *Handler) workspaceUpdateOwner(w http.ResponseWriter, r *http.Request, method string) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, method) {
		return "", false
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "workspace updates do not accept query parameters")
		return "", false
	}
	if h.workspaceUpdates == nil {
		updateError(w, workspaceupdates.ErrUnavailable)
		return "", false
	}
	owner, err := workspaceupdates.ResolveOwner(r.Context(), h.currentUserID)
	if err != nil {
		updateError(w, err)
		return "", false
	}
	return owner, true
}

// GetWorkspaceUpdates is observational: neither opening the workspace nor
// hydrating its summary delivers/acknowledges a conversation or starts work.
func (h *Handler) GetWorkspaceUpdates(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.workspaceUpdateOwner(w, r, http.MethodGet)
	if !ok {
		return
	}
	out, err := h.workspaceUpdates.Summary(r.Context(), owner, r.PathValue("workspaceID"))
	if err != nil {
		updateError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, out)
}

func (h *Handler) AcknowledgeWorkspaceUpdate(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.workspaceUpdateOwner(w, r, http.MethodPost)
	if !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var ack session.WelcomeAcknowledgement
	if err := decoder.Decode(&ack); err != nil {
		updateError(w, session.ErrInvalidID)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		updateError(w, session.ErrInvalidID)
		return
	}
	validID := func(id string) bool { return id != "" && len(id) <= 128 && id == strings.TrimSpace(id) }
	if !validID(ack.SessionID) || !validID(ack.MessageID) || (ack.Revision != 1 && ack.Revision != 2) {
		updateError(w, session.ErrInvalidID)
		return
	}
	out, err := h.workspaceUpdates.Acknowledge(r.Context(), owner, r.PathValue("workspaceID"), ack)
	if err != nil {
		updateError(w, err)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, out)
}
