// Package emailsetuphttp serves the "Set up email" card: detect who hosts an
// address, then connect it with an app password.
package emailsetuphttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/emailsetup"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// maxConnectBody bounds the connect request; it holds an address, a password,
// and at most a server name.
const maxConnectBody = 16 << 10

// Service is the setup flow. *emailsetup.Service satisfies it.
type Service interface {
	Detect(ctx context.Context, address string) (emailsetup.Profile, error)
	Connect(ctx context.Context, userID string, req emailsetup.ConnectRequest) (emailsetup.ConnectResult, error)
}

// Handler serves the card's two endpoints. Both sit behind the same local-origin
// guard as the Google connection endpoints: connect carries a password, and
// detect makes DNS lookups no other site should be able to trigger.
type Handler struct {
	service Service
	guard   *connectionshttp.OriginGuard
}

// NewHandler builds the handler.
func NewHandler(service Service) *Handler {
	return &Handler{service: service, guard: connectionshttp.NewOriginGuard()}
}

// Register wires the routes onto mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("/api/email-setup/detect", h.guard.Wrap(http.HandlerFunc(h.detect)))
	mux.Handle("/api/email-setup/connect", h.guard.Wrap(http.HandlerFunc(h.connect)))
}

// detect handles GET /api/email-setup/detect?address=… and answers with what
// the card should ask for that address.
func (h *Handler) detect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	profile, err := h.service.Detect(r.Context(), r.URL.Query().Get("address"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": profile})
}

// connect handles POST /api/email-setup/connect with a JSON ConnectRequest.
func (h *Handler) connect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req emailsetup.ConnectRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request", "message": "The request could not be read."})
		return
	}
	result, err := h.service.Connect(r.Context(), userprofile.LocalUserID, req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

// writeFailure answers with the setup failure the card shows. Only its safe
// fields are serialized; the underlying cause is logged by category, never the
// request.
func writeFailure(w http.ResponseWriter, err error) {
	var failure *emailsetup.Failure
	if !errors.As(err, &failure) {
		failure = &emailsetup.Failure{Code: emailsetup.FailureInternal, Message: "Email setup failed. Try again."}
	}
	status := http.StatusUnprocessableEntity
	switch failure.Code {
	case emailsetup.FailureInvalidAddress:
		status = http.StatusBadRequest
	case emailsetup.FailureUnreachable:
		status = http.StatusBadGateway
	case emailsetup.FailureKeyringUnavailable:
		status = http.StatusServiceUnavailable
	case emailsetup.FailureInternal:
		status = http.StatusInternalServerError
	}
	if status >= http.StatusInternalServerError || failure.Code == emailsetup.FailureUnreachable {
		logger.Warn("email setup failed", logger.Fields{"code": failure.Code, "cause": causeCategory(failure)})
	}
	writeJSON(w, status, failure)
}

// causeCategory names the failure's cause without its text, which can carry
// server responses or paths.
func causeCategory(failure *emailsetup.Failure) string {
	cause := errors.Unwrap(failure)
	if cause == nil {
		return "none"
	}
	text := cause.Error()
	if i := strings.IndexByte(text, ':'); i > 0 && i < 48 {
		return text[:i]
	}
	return "error"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
