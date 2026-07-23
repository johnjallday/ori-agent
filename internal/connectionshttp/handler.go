package connectionshttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
)

// Handler serves the Google Account connection endpoints. Mutating and
// metadata-reading routes are wrapped by the OriginGuard (FR 34); the OAuth
// callback is a top-level browser navigation from Google and is instead
// protected by the single-use state value it must carry (FR 20).
type Handler struct {
	flow  *connections.IdentityFlow
	store *connections.Store
	guard *OriginGuard

	resolveLocalUser func(*http.Request) string
	buildRedirectURL func(*http.Request) string
}

// Deps are the Handler's collaborators.
type Deps struct {
	Flow  *connections.IdentityFlow
	Store *connections.Store
	Guard *OriginGuard
	// ResolveLocalUser maps a request to Ori's local user id (single-user app
	// defaults to "local").
	ResolveLocalUser func(*http.Request) string
	// BuildRedirectURL returns the loopback callback URL for a request. Defaults
	// to "<scheme>://<host>/api/connections/google/callback".
	BuildRedirectURL func(*http.Request) string
}

// NewHandler builds a Handler, filling sensible defaults.
func NewHandler(d Deps) *Handler {
	h := &Handler{
		flow:             d.Flow,
		store:            d.Store,
		guard:            d.Guard,
		resolveLocalUser: d.ResolveLocalUser,
		buildRedirectURL: d.BuildRedirectURL,
	}
	if h.guard == nil {
		h.guard = NewOriginGuard()
	}
	if h.resolveLocalUser == nil {
		h.resolveLocalUser = func(*http.Request) string { return "local" }
	}
	if h.buildRedirectURL == nil {
		h.buildRedirectURL = defaultRedirectURL
	}
	return h
}

func defaultRedirectURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/api/connections/google/callback", scheme, r.Host)
}

// Register wires the routes onto mux. Connect/disconnect/status sit behind the
// origin guard; callback does not (it is protected by its state value).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("/api/connections/google/connect", h.guard.Wrap(http.HandlerFunc(h.connect)))
	mux.Handle("/api/connections/google/disconnect", h.guard.Wrap(http.HandlerFunc(h.disconnect)))
	mux.Handle("/api/connections/google/status", h.guard.Wrap(http.HandlerFunc(h.status)))
	mux.HandleFunc("/api/connections/google/callback", h.callback)
}

func (h *Handler) connect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	res, err := h.flow.BeginConnect(connections.BeginConnectParams{
		LocalUserID: h.resolveLocalUser(r),
		RedirectURL: h.buildRedirectURL(r),
		ReturnTo:    strings.TrimSpace(r.URL.Query().Get("return_to")),
	})
	if err != nil {
		if errors.Is(err, connections.ErrOAuthNotConfigured) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":   "not_configured",
				"message": "Google sign-in isn't configured in this build yet.",
			})
			return
		}
		http.Error(w, "failed to start connection", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorize_url": res.AuthorizeURL})
}

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	conn, err := h.flow.CompleteConnect(r.Context(), connections.CompleteConnectParams{
		State:      q.Get("state"),
		Code:       q.Get("code"),
		OAuthError: q.Get("error"),
	})
	if err != nil {
		h.renderResult(w, false, "", userMessageFor(err))
		return
	}
	h.renderResult(w, true, conn.Email, "")
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	conn, err := h.store.Load()
	if err != nil {
		http.Error(w, "failed to load connection", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, connections.Project(conn))
}

func (h *Handler) disconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.store.Delete(); err != nil {
		http.Error(w, "failed to disconnect", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, connections.Project(nil))
}

// userMessageFor maps a flow error to a safe, specific user-facing message. It
// never includes tokens, codes, or provider internals (FR 83, 84).
func userMessageFor(err error) string {
	switch {
	case errors.Is(err, connections.ErrExpiredFlow):
		return "This sign-in link expired or was already used. Please try connecting again."
	case errors.Is(err, connections.ErrAuthorizationDenied):
		return "Sign-in was canceled."
	case errors.Is(err, connections.ErrDifferentAccountActive):
		return "A different Google account is already connected. Disconnect it first, then try again."
	case errors.Is(err, connections.ErrNonceMismatch), errors.Is(err, connections.ErrIDTokenInvalid):
		return "We couldn't verify the Google sign-in. Please try again."
	case errors.Is(err, connections.ErrNoIDToken):
		return "Google didn't return an identity for this sign-in. Please try again."
	default:
		return "We couldn't complete the Google sign-in. Please try again."
	}
}

// renderResult writes a minimal, self-contained result page. It exposes no
// authorization code or token and links the user back to the connection card.
func (h *Handler) renderResult(w http.ResponseWriter, ok bool, email, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	status := http.StatusOK
	title, body := "Google connected", "You can close this tab and return to Ori."
	if ok && email != "" {
		body = "Connected as " + html.EscapeString(email) + ". You can close this tab and return to Ori."
	}
	if !ok {
		status = http.StatusBadRequest
		title, body = "Sign-in not completed", html.EscapeString(message)
	}
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title>`+
		`<meta name="viewport" content="width=device-width, initial-scale=1">`+
		`<style>body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1.5rem;text-align:center}`+
		`h1{font-size:1.3rem}a{color:#2563eb}</style></head><body>`+
		`<h1>%s</h1><p>%s</p><p><a href="/settings#google-account">Return to Ori</a></p></body></html>`,
		html.EscapeString(title), html.EscapeString(title), body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
