package connectionshttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// WorkspaceLinker gives a workspace its own Gmail account by reusing the global
// grant's identity — no Google re-authorization (FR 47, 54). Implemented by a
// vault-backed adapter; nil disables workspace linking.
type WorkspaceLinker interface {
	LinkGmailToWorkspace(ctx context.Context, credentialRef, vaultID, workspaceID string) (accountID string, err error)
}

// Handler serves the Google Account connection endpoints. Mutating and
// metadata-reading routes are wrapped by the OriginGuard (FR 34); the OAuth
// callback is a top-level browser navigation from Google and is instead
// protected by the single-use state value it must carry (FR 20).
type Handler struct {
	flow     *connections.IdentityFlow
	store    *connections.Store
	guard    *OriginGuard
	linker   WorkspaceLinker
	impacts  ImpactEnumerator
	teardown ProductTeardown
	health   GrantHealthChecker
	consent  *connections.ConsentLog

	resolveLocalUser func(*http.Request) string
	buildRedirectURL func(*http.Request) string
}

// Deps are the Handler's collaborators.
type Deps struct {
	Flow   *connections.IdentityFlow
	Store  *connections.Store
	Guard  *OriginGuard
	Linker WorkspaceLinker
	// Impacts enumerates which workspaces use each product grant, for the
	// disconnect impact preview (FR 77). Nil degrades to an empty preview.
	Impacts ImpactEnumerator
	// Teardown removes a product's local credentials/bindings on disconnect or
	// unlink (FR 78, 79, 80). Nil still drops the grant but leaves credentials.
	Teardown ProductTeardown
	// Health reconciles each grant's live health on status load without opening a
	// browser (FR 85). Nil keeps stored health as-is.
	Health GrantHealthChecker
	// Consent is the token/content-free consent audit log (FR 96). Nil disables
	// consent recording.
	Consent *connections.ConsentLog
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
		linker:           d.Linker,
		impacts:          d.Impacts,
		teardown:         d.Teardown,
		health:           d.Health,
		consent:          d.Consent,
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
	mux.Handle("/api/connections/google/impact", h.guard.Wrap(http.HandlerFunc(h.impact)))
	mux.Handle("/api/connections/google/consent", h.guard.Wrap(http.HandlerFunc(h.consentAudit)))
	mux.Handle("/api/connections/google/product/disconnect", h.guard.Wrap(http.HandlerFunc(h.productDisconnect)))
	mux.Handle("/api/connections/google/product/unlink", h.guard.Wrap(http.HandlerFunc(h.productUnlink)))
	mux.Handle("/api/connections/google/gmail/enable", h.guard.Wrap(http.HandlerFunc(h.gmailEnable)))
	mux.Handle("/api/connections/google/gmail/link", h.guard.Wrap(http.HandlerFunc(h.gmailLink)))
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
	conn, err := h.flow.Complete(r.Context(), connections.CompleteConnectParams{
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
	// Refresh each grant's health from its live source (no browser) so a stale
	// credential shows as Reconnect required (FR 85). Persist only when changed.
	if h.reconcileGrantHealth(conn) {
		if saveErr := h.store.Save(conn); saveErr != nil {
			logger.Warn("connection status: failed to persist reconciled health", logger.Fields{"error": saveErr})
		}
	}
	// Keep the consent audit trail in step with the live grants (FR 96).
	h.recordConsent(conn)
	writeJSON(w, http.StatusOK, connections.Project(conn))
}

func (h *Handler) disconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Tear down every product's local credentials before dropping the identity, so
	// a whole-account disconnect leaves no orphaned Google credentials behind. The
	// MCP server definitions and workspace bindings survive, so each workspace
	// lands in a recoverable "Connection required" rather than being deleted
	// (FR 80). Teardown is best-effort — a failure never blocks the disconnect.
	if h.teardown != nil {
		if conn, err := h.store.Load(); err == nil && conn != nil {
			for _, product := range connections.AllProducts() {
				if g, ok := conn.Grant(product); ok && g != nil {
					_ = h.teardown.DisconnectProduct(r.Context(), product, g.CredentialRef)
				}
			}
		}
	}
	if err := h.store.Delete(); err != nil {
		http.Error(w, "failed to disconnect", http.StatusInternalServerError)
		return
	}
	// Record withdrawal of every remaining active consent (FR 96).
	h.recordConsent(nil)
	writeJSON(w, http.StatusOK, connections.Project(nil))
}

// gmailEnable starts the Gmail product-enablement authorization for the active
// identity (identity + gmail.readonly). The frontend opens the returned URL; the
// shared callback route completes it (the pending state records the product).
func (h *Handler) gmailEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	params := connections.BeginConnectParams{
		LocalUserID: h.resolveLocalUser(r),
		RedirectURL: h.buildRedirectURL(r),
		ReturnTo:    strings.TrimSpace(r.URL.Query().Get("return_to")),
	}
	var res connections.BeginConnectResult
	var err error
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("scope")), "send") {
		res, err = h.flow.BeginEnableGmailSend(params) // explicit send upgrade (FR 44)
	} else {
		res, err = h.flow.BeginEnableGmail(params)
	}
	if err != nil {
		switch {
		case errors.Is(err, connections.ErrOAuthNotConfigured):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "not_configured", "message": "Google sign-in isn't configured in this build yet."})
		case errors.Is(err, connections.ErrNoActiveIdentity):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "no_identity", "message": "Connect your Google account before enabling Gmail."})
		case errors.Is(err, connections.ErrNoCredentialSink):
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable", "message": "Gmail can't be enabled in this build."})
		default:
			http.Error(w, "failed to start Gmail enable", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorize_url": res.AuthorizeURL})
}

// gmailLink gives the target workspace its own Gmail account by reusing the
// active identity's healthy Gmail grant — no Google re-authorization (FR 47, 54).
func (h *Handler) gmailLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_workspace", "message": "A workspace id is required."})
		return
	}
	if h.linker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable", "message": "Workspace linking isn't available in this build."})
		return
	}
	conn, err := h.store.Load()
	if err != nil {
		http.Error(w, "failed to load connection", http.StatusInternalServerError)
		return
	}
	g, ok := connGmailGrant(conn)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "gmail_not_enabled", "message": "Enable Gmail on your Google account first."})
		return
	}
	accountID, err := h.linker.LinkGmailToWorkspace(r.Context(), g.CredentialRef, conn.VaultID, workspaceID)
	if err != nil {
		http.Error(w, "failed to link Gmail to the workspace", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"account_id": accountID})
}

// connGmailGrant returns the connection's Gmail grant when it is healthy and
// carries a credential reference.
func connGmailGrant(conn *connections.Connection) (*connections.ProductGrant, bool) {
	if conn == nil {
		return nil, false
	}
	g, ok := conn.Grant(connections.ProductGmail)
	if !ok || g == nil || g.Health != connections.HealthHealthy || strings.TrimSpace(g.CredentialRef) == "" {
		return nil, false
	}
	return g, true
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
