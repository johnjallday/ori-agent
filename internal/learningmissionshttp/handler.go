// Package learningmissionshttp exposes local owner interactions, never tools.
package learningmissionshttp

import (
	"errors"
	"io"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type Handler struct {
	service *learningmissions.Service
	users   userprofile.UserProvider
}

func NewHandler(service *learningmissions.Service, users userprofile.UserProvider) *Handler {
	return &Handler{service: service, users: users}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/learning-missions", h.Status)
	mux.HandleFunc("POST /api/learning-missions/proposals", h.Propose)
	mux.HandleFunc("POST /api/learning-missions/instances/{id}/decisions", h.Decide)
	mux.HandleFunc("POST /api/learning-missions/instances/{id}/review", h.Review)
	mux.HandleFunc("POST /api/learning-missions/instances/{id}/work", h.Work)
	mux.HandleFunc("POST /api/learning-missions/clear", h.Clear)
	mux.HandleFunc("GET /api/learning-missions/operations/{id}", h.Operation)
}

// Owner is resolved outside the body. Exact origin (including port) protects
// against other local sites; this is not hostile-local-process authentication.
func (h *Handler) owner(w http.ResponseWriter, r *http.Request, mutation bool) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if h == nil || h.service == nil || h.users == nil {
		respond(w, nil, learningmissions.ErrUnavailable)
		return "", false
	}
	owner, err := LocalOwner(r, h.users, mutation)
	if err != nil {
		status := http.StatusForbidden
		if errors.Is(err, ErrJSONRequired) {
			status = http.StatusUnsupportedMediaType
		}
		if errors.Is(err, ErrOwnerUnavailable) {
			status = http.StatusUnauthorized
		}
		http.Error(w, err.Error(), status)
		return "", false
	}
	return owner, true
}

func parse(w http.ResponseWriter, r *http.Request, into any) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, learningmissions.MaxInputBytes))
	if err != nil || learningmissions.DecodeStrict(data, learningmissions.MaxInputBytes, into) != nil {
		respond(w, nil, learningmissions.ErrInvalid)
		return false
	}
	return true
}

func respond(w http.ResponseWriter, value any, err error) {
	if err == nil {
		_ = orihttp.RespondJSON(w, http.StatusOK, value)
		return
	}
	code, reason := http.StatusServiceUnavailable, "unavailable"
	switch {
	case errors.Is(err, learningmissions.ErrInvalid):
		code, reason = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, learningmissions.ErrUnavailable):
		code, reason = http.StatusNotFound, "unavailable"
	case errors.Is(err, learningmissions.ErrConflict):
		code, reason = http.StatusConflict, "state_changed"
	case errors.Is(err, learningmissions.ErrUnsupported):
		code, reason = http.StatusConflict, "definition_unavailable"
	case errors.Is(err, learningmissions.ErrLimit):
		code, reason = http.StatusTooManyRequests, "limit_reached"
	case errors.Is(err, resetstate.ErrWorkFenced), errors.Is(err, resetstate.ErrWorkUntracked):
		reason = "writes_unavailable"
	}
	_ = orihttp.RespondJSON(w, code, map[string]string{"error": reason})
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if owner, ok := h.owner(w, r, false); ok {
		value, err := h.service.Status(r.Context(), owner)
		respond(w, value, err)
	}
}

func (h *Handler) Propose(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.owner(w, r, true)
	if !ok {
		return
	}
	var req learningmissions.ProposalRequest
	if !parse(w, r, &req) {
		return
	}
	value, err := h.service.Propose(r.Context(), owner, req)
	respond(w, h.service.VisibleOutcome(r.Context(), owner, value), err)
}

func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.owner(w, r, true)
	if !ok {
		return
	}
	var req learningmissions.DecisionRequest
	if !parse(w, r, &req) {
		return
	}
	value, err := h.service.Decide(r.Context(), owner, r.PathValue("id"), req)
	respond(w, h.service.VisibleOutcome(r.Context(), owner, value), err)
}

func (h *Handler) Work(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.owner(w, r, true)
	if !ok {
		return
	}
	var req learningmissions.WorkRequest
	if !parse(w, r, &req) {
		return
	}
	value, err := h.service.PrepareWork(r.Context(), owner, r.PathValue("id"), req)
	respond(w, h.service.VisibleOutcome(r.Context(), owner, value), err)
}

func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.owner(w, r, true)
	if !ok {
		return
	}
	var req learningmissions.ReviewRequest
	if !parse(w, r, &req) {
		return
	}
	value, err := h.service.ReviewResult(r.Context(), owner, r.PathValue("id"), req)
	respond(w, h.service.VisibleOutcome(r.Context(), owner, value), err)
}

func (h *Handler) Clear(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.owner(w, r, true)
	if !ok {
		return
	}
	var req struct {
		learningmissions.MutationRequest
		Scope string `json:"scope"`
	}
	if !parse(w, r, &req) {
		return
	}
	if req.Scope != "history" {
		respond(w, nil, learningmissions.ErrInvalid)
		return
	}
	value, err := h.service.ClearHistory(r.Context(), owner, req.MutationRequest)
	respond(w, value, err)
}

func (h *Handler) Operation(w http.ResponseWriter, r *http.Request) {
	if owner, ok := h.owner(w, r, false); ok {
		value, err := h.service.Operation(r.Context(), owner, r.PathValue("id"))
		respond(w, h.service.VisibleOutcome(r.Context(), owner, value), err)
	}
}
