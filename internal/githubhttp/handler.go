package githubhttp

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Guard is the CSRF/origin check applied to every route here. It is an
// interface rather than a concrete type so this package does not depend on
// the connections package; server wiring passes
// connectionshttp.NewOriginGuard(), the same guard the Google connection
// uses.
type Guard interface {
	Wrap(next http.Handler) http.Handler
}

// Handler serves the global GitHub connection's HTTP surface.
type Handler struct {
	conn   *Connection
	guard  Guard
	broker *Broker
}

// NewHandler builds the handler. A nil guard leaves routes unwrapped, which
// is only appropriate in tests -- server wiring always supplies one.
func NewHandler(conn *Connection, guard Guard) *Handler {
	return &Handler{conn: conn, guard: guard}
}

// WithBroker attaches the confirm-gated write broker, enabling the proposal
// routes. Without it the handler serves connection management only -- which is
// the correct degraded state, since a workspace with no broker cannot write to
// GitHub at all rather than writing without confirmation.
func (h *Handler) WithBroker(broker *Broker) *Handler {
	if h != nil {
		h.broker = broker
	}
	return h
}

// Connection exposes the global connection so server wiring can share the one
// instance between the HTTP surface and the setup adapter -- they must agree
// about connection state, and two instances would each hold their own client.
func (h *Handler) Connection() *Connection {
	if h == nil {
		return nil
	}
	return h.conn
}

// Register wires the routes onto mux, mirroring the
// /api/connections/<provider>/... convention the Google connection
// established.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("/api/connections/github/connect", h.wrap(http.HandlerFunc(h.connect)))
	mux.Handle("/api/connections/github/disconnect", h.wrap(http.HandlerFunc(h.disconnect)))
	mux.Handle("/api/connections/github/status", h.wrap(http.HandlerFunc(h.status)))
}

func (h *Handler) wrap(next http.Handler) http.Handler {
	if h.guard == nil {
		return next
	}
	return h.guard.Wrap(next)
}

// connectRequest is the connect payload.
//
// The token arrives in a POST body rather than a query parameter on purpose:
// query strings land in server logs, browser history, and Referer headers,
// none of which should ever hold a credential.
type connectRequest struct {
	Token string `json:"token"`
}

// connect serves POST /api/connections/github/connect.
//
// The response deliberately carries only the resulting identity. There is no
// code path in this package that returns a stored token to a client.
func (h *Handler) connect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req connectRequest
	// Bound the body: a pasted PAT is well under 1 KiB, so anything larger
	// is a mistake or an attempt to make the server allocate.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "invalid_request",
			"message": "Could not read the request.",
		})
		return
	}

	identity, err := h.conn.Connect(r.Context(), req.Token)
	if err != nil {
		writeConnectionError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"connected":  true,
		"login":      identity.Login,
		"scopes":     identity.Scopes,
		"token_type": identity.TokenType,
	})
}

// disconnect serves POST /api/connections/github/disconnect. It is
// idempotent: disconnecting when nothing is connected succeeds.
func (h *Handler) disconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.conn.Disconnect(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "disconnect_failed",
			"message": "Could not remove the saved GitHub connection.",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
}

// status serves GET /api/connections/github/status. It runs a live check, so
// a token revoked on GitHub reports as disconnected here immediately rather
// than on some later refresh.
func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, h.conn.Status(r.Context()))
}

// writeConnectionError maps a classified ConnectionError onto an HTTP status.
// The message is the adapter's plain-language copy, which is already
// guaranteed token-free; no raw GitHub error text reaches the client.
func writeConnectionError(w http.ResponseWriter, err error) {
	var connErr *ConnectionError
	if !errors.As(err, &connErr) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "connect_failed",
			"message": "Could not connect to GitHub.",
		})
		return
	}

	statusCode := http.StatusBadGateway
	switch connErr.Category {
	case ErrorCategoryInvalidToken, ErrorCategoryInsufficientScope:
		// The submitted credential is the problem, not the server's.
		statusCode = http.StatusBadRequest
	case ErrorCategoryNotConnected, ErrorCategoryVaultLocked:
		statusCode = http.StatusConflict
	case ErrorCategoryRateLimited:
		statusCode = http.StatusTooManyRequests
	}

	writeJSON(w, statusCode, map[string]string{
		"error":   connErr.Category,
		"message": connErr.Message,
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	// A credential-bearing surface must never be cached by an
	// intermediary or replayed from the browser's back/forward cache.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		return
	}
}
