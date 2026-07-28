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

// WorkspaceLinker points a workspace at the connection's authoritative Gmail
// credential — no Google re-authorization (FR 47, 54, 68-70). Implemented by a
// vault-backed adapter; nil disables workspace linking.
type WorkspaceLinker interface {
	LinkGmailToWorkspace(ctx context.Context, credentialRef, vaultID, workspaceID string) (accountID string, err error)
}

// ErrCredentialMissing means the connection's grant references a vault
// credential that no longer exists. Implementations return an error matching
// this so the endpoint can offer a reconnect instead of failing opaquely.
var ErrCredentialMissing = errors.New("connectionshttp: the referenced Gmail credential no longer exists")

// Handler serves the Google Account connection endpoints. Mutating and
// metadata-reading routes are wrapped by the OriginGuard (FR 34); the OAuth
// callback is a top-level browser navigation from Google and is instead
// protected by the single-use state value it must carry (FR 20).
type Handler struct {
	flow           *connections.IdentityFlow
	store          *connections.Store
	guard          *OriginGuard
	vaults         connections.VaultCatalog
	linker         WorkspaceLinker
	impacts        ImpactEnumerator
	teardown       ProductTeardown
	health         GrantHealthChecker
	healthNotifier HealthNotifier
	consent        *connections.ConsentLog
	migrator       Migrator

	resolveLocalUser func(*http.Request) string
	buildRedirectURL func(*http.Request) string
}

// Deps are the Handler's collaborators.
type Deps struct {
	Flow  *connections.IdentityFlow
	Store *connections.Store
	Guard *OriginGuard
	// Vaults answers read-only "which vault step is needed?" queries for the
	// connection card. Nil disables the preflight endpoint.
	Vaults connections.VaultCatalog
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
	// HealthNotifier proactively surfaces a grant's health transition via the
	// event bus + Action Center (FR 86). Nil disables surfacing.
	HealthNotifier HealthNotifier
	// Consent is the token/content-free consent audit log (FR 96). Nil disables
	// consent recording.
	Consent *connections.ConsentLog
	// Migrator detects + folds legacy Gmail accounts into the connection (FR 88/89).
	// Nil disables migration.
	Migrator Migrator
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
		vaults:           d.Vaults,
		linker:           d.Linker,
		impacts:          d.Impacts,
		teardown:         d.Teardown,
		health:           d.Health,
		healthNotifier:   d.HealthNotifier,
		consent:          d.Consent,
		migrator:         d.Migrator,
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
	mux.Handle("/api/connections/google/migratable", h.guard.Wrap(http.HandlerFunc(h.migratable)))
	mux.Handle("/api/connections/google/migrate", h.guard.Wrap(http.HandlerFunc(h.migrate)))
	mux.Handle("/api/connections/google/product/disconnect", h.guard.Wrap(http.HandlerFunc(h.productDisconnect)))
	mux.Handle("/api/connections/google/product/unlink", h.guard.Wrap(http.HandlerFunc(h.productUnlink)))
	mux.Handle("/api/connections/google/vault", h.guard.Wrap(http.HandlerFunc(h.vaultPreflight)))
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
		var misconfigured *connections.ClientConfigError
		switch {
		case errors.As(err, &misconfigured):
			// A configured-but-wrong client: name the exact fix (FR 63-65). The
			// message never echoes the configured id or secret.
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":   "client_invalid",
				"problem": string(misconfigured.Verdict.Problem),
				"message": misconfigured.Verdict.Message(),
			})
		case errors.Is(err, connections.ErrOAuthNotConfigured):
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":   "not_configured",
				"message": "Google sign-in isn't configured in this build yet.",
			})
		default:
			http.Error(w, "failed to start connection", http.StatusInternalServerError)
		}
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
		failure := connections.ClassifyCallback(err)
		// Safe diagnostics only: stage, category, and the correlation id shared with
		// the begin-authorization log line. Never the code, token, or state (FR 19, 20).
		logger.Warn("google connection callback failed", logger.Fields{
			"stage":          string(failure.Stage),
			"category":       string(failure.Category),
			"signed_in":      failure.SignedIn,
			"correlation_id": failure.CorrelationID,
		})
		h.renderFailure(w, failure)
		return
	}
	h.renderResult(w, conn.Email)
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	conn, err := h.store.Load()
	if err != nil {
		http.Error(w, "failed to load connection", http.StatusInternalServerError)
		return
	}
	// Refresh each grant's health from its live source (no browser) so a stale
	// credential shows as Reconnect required (FR 85). Persist only when changed.
	if h.reconcileGrantHealth(r.Context(), conn) {
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
		// An explicit choice supplied when the caller resumes after a
		// choose/create/repair prompt (FR 6, 9, 10).
		VaultID: strings.TrimSpace(r.URL.Query().Get("vault_id")),
	}
	var res connections.BeginConnectResult
	var err error
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("scope")), "send") {
		res, err = h.flow.BeginEnableGmailSend(r.Context(), params) // explicit send upgrade (FR 44)
	} else {
		res, err = h.flow.BeginEnableGmail(r.Context(), params)
	}
	if err != nil {
		var preflight *connections.VaultPreflightError
		var misconfigured *connections.ClientConfigError
		switch {
		case errors.As(err, &misconfigured):
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":   "client_invalid",
				"problem": string(misconfigured.Verdict.Problem),
				"message": misconfigured.Verdict.Message(),
			})
		case errors.As(err, &preflight):
			// The vault needs user action first. Nothing has been sent to Google, so
			// Gmail stays disabled until the user completes (or cancels) the repair.
			writeJSON(w, http.StatusConflict, vaultActionPayload(preflight.Preflight))
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
	// Pairs with the callback log line via correlation_id. No URL, state, or
	// client secret is logged — the authorize URL carries the client id (FR 19, 20).
	logger.Info("google connection: gmail authorization started", logger.Fields{
		"stage":          string(connections.StageAuthorization),
		"correlation_id": res.CorrelationID,
	})
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
		// A grant can reference a credential the vault no longer holds — a vault
		// recreated, a data directory moved, a partial teardown. That is a
		// reconnect, not a server fault, and reporting it as a 500 left the user
		// with a dead button and an opaque console error.
		if errors.Is(err, ErrCredentialMissing) {
			logger.Warn("gmail link: grant references a missing credential", logger.Fields{
				"workspace_id": workspaceID,
			})
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":   "credential_missing",
				"message": "Your Gmail credential is no longer in the vault. Re-enable Gmail on your Google account to reconnect it.",
				"action":  "enable_gmail",
			})
			return
		}
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

// renderResult writes the success page. It exposes no authorization code or
// token, and returns the user to Settings → Google Account so the card can
// refresh its product rows without reconnecting the base identity (FR 18).
func (h *Handler) renderResult(w http.ResponseWriter, email string) {
	body := "You're connected. Returning you to Ori…"
	if email != "" {
		body = "Connected as " + html.EscapeString(email) + ". Returning you to Ori…"
	}
	writeResultPage(w, http.StatusOK, resultPage{
		Title:       "Google connected",
		Body:        body,
		ActionLabel: "Return to Ori",
		ActionURL:   settingsAnchor,
		// Land back on the card automatically; the manual link covers the case
		// where the redirect is blocked.
		RedirectAfterSeconds: 2,
	})
}

// renderFailure writes the category-specific failure page: what happened,
// whether the Google half succeeded, and the exact repair action (FR 13-16).
func (h *Handler) renderFailure(w http.ResponseWriter, failure *connections.CallbackError) {
	c := copyFor(failure)
	writeResultPage(w, http.StatusBadRequest, resultPage{
		Title:       c.Title,
		Body:        html.EscapeString(c.Body),
		ActionLabel: c.ActionLabel,
		ActionURL:   returnURL(failure, c.Action),
	})
}

// resultPage is the callback result page's content. Body is pre-escaped by the
// caller because the success page embeds an escaped email address.
type resultPage struct {
	Title                string
	Body                 string
	ActionLabel          string
	ActionURL            string
	RedirectAfterSeconds int
}

// writeResultPage renders a minimal, self-contained page. Everything
// interpolated is either a constant or already escaped.
func writeResultPage(w http.ResponseWriter, status int, page resultPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	refresh := ""
	if page.RedirectAfterSeconds > 0 {
		refresh = fmt.Sprintf(`<meta http-equiv="refresh" content="%d;url=%s">`,
			page.RedirectAfterSeconds, html.EscapeString(page.ActionURL))
	}
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title>`+
		`<meta name="viewport" content="width=device-width, initial-scale=1">%s`+
		`<style>body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1.5rem;text-align:center}`+
		`h1{font-size:1.3rem}a{color:#2563eb}</style></head><body>`+
		`<h1>%s</h1><p>%s</p><p><a href="%s">%s</a></p></body></html>`,
		html.EscapeString(page.Title), refresh,
		html.EscapeString(page.Title), page.Body,
		html.EscapeString(page.ActionURL), html.EscapeString(page.ActionLabel))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
