package vaulthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/vault"
	"golang.org/x/oauth2"
)

const emailOAuthPopupEventType = "ori:vault-email-oauth"

type emailOAuthProviderStatus struct {
	Provider         string `json:"provider"`
	Label            string `json:"label"`
	ConnectSupported bool   `json:"connect_supported"`
	Enabled          bool   `json:"enabled"`
	Reason           string `json:"reason,omitempty"`
}

type emailOAuthPopupPayload struct {
	Type    string              `json:"type"`
	Success bool                `json:"success"`
	Message string              `json:"message,omitempty"`
	Error   string              `json:"error,omitempty"`
	Account *vault.EmailAccount `json:"account,omitempty"`
}

type emailOAuthProviderConfig struct {
	provider      vault.EmailProvider
	label         string
	authURL       string
	tokenURL      string
	clientID      string
	clientSecret  string
	redirectURL   string
	scopes        []string
	defaultSource string
}

type pendingEmailOAuthFlow struct {
	Provider        vault.EmailProvider
	VaultID         string
	AccountID       string
	Label           string
	EmailAddress    string
	DisplayName     string
	Username        string
	WorkspaceID     string
	Tags            []string
	Source          string
	RetentionPolicy string
	CodeVerifier    string
	RedirectURL     string
}

type emailOAuthFlowStore struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	pending map[string]pendingEmailOAuthFlow
}

func newEmailOAuthFlowStore(now func() time.Time, ttl time.Duration) *emailOAuthFlowStore {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &emailOAuthFlowStore{
		now:     now,
		ttl:     ttl,
		pending: make(map[string]pendingEmailOAuthFlow),
	}
}

func (s *emailOAuthFlowStore) put(flow pendingEmailOAuthFlow) string {
	if s == nil {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked()

	state := fmt.Sprintf("%d.%s", s.now().UTC().UnixNano(), uuid.NewString())
	s.pending[state] = flow
	return state
}

func (s *emailOAuthFlowStore) take(state string) (pendingEmailOAuthFlow, bool) {
	var flow pendingEmailOAuthFlow
	if s == nil {
		return flow, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked()
	flow, ok := s.pending[state]
	if !ok {
		return pendingEmailOAuthFlow{}, false
	}
	delete(s.pending, state)
	return flow, true
}

func (s *emailOAuthFlowStore) cleanupLocked() {
	if s == nil {
		return
	}

	now := s.now()
	for state, flow := range s.pending {
		startedAt, err := parseEmailOAuthStateTimestamp(state)
		if err != nil || now.Sub(startedAt) > s.ttl {
			delete(s.pending, state)
			continue
		}
		if strings.TrimSpace(flow.CodeVerifier) == "" {
			delete(s.pending, state)
		}
	}
}

func parseEmailOAuthStateTimestamp(state string) (time.Time, error) {
	parts := strings.SplitN(strings.TrimSpace(state), ".", 2)
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("invalid state")
	}
	nanos, err := time.ParseDuration(parts[0] + "ns")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, nanos.Nanoseconds()).UTC(), nil
}

func providerStatusForRequest(provider vault.EmailProvider, r *http.Request) emailOAuthProviderStatus {
	switch provider {
	case vault.EmailProviderGmail:
		cfg := loadEmailOAuthProviderConfig(provider, r, "")
		return emailOAuthProviderStatus{
			Provider:         string(provider),
			Label:            "Google",
			ConnectSupported: true,
			Enabled:          cfg.isConfigured(),
			Reason:           cfg.disabledReason(),
		}
	case vault.EmailProviderMicrosoft:
		cfg := loadEmailOAuthProviderConfig(provider, r, "")
		return emailOAuthProviderStatus{
			Provider:         string(provider),
			Label:            "Microsoft",
			ConnectSupported: true,
			Enabled:          cfg.isConfigured(),
			Reason:           cfg.disabledReason(),
		}
	case vault.EmailProviderIMAPSMTP:
		return emailOAuthProviderStatus{
			Provider:         string(provider),
			Label:            "Custom IMAP / SMTP",
			ConnectSupported: false,
			Enabled:          false,
			Reason:           "Custom IMAP / SMTP accounts use manual credentials or advanced token import.",
		}
	default:
		return emailOAuthProviderStatus{
			Provider:         string(provider),
			Label:            "Unsupported Provider",
			ConnectSupported: false,
			Enabled:          false,
			Reason:           "OAuth connect is unavailable for this provider.",
		}
	}
}

// Gmail OAuth scopes are staged for least privilege (contract §3.1): a mailbox
// connects read-only, and sending requires a separate, explicit scope upgrade
// requested only when the user first confirms a send. gmail.readonly is
// sufficient for triage, the brief, and drafting (which is local until the
// broker sends).
const (
	gmailScopeReadonly = "https://www.googleapis.com/auth/gmail.readonly"
	gmailScopeSend     = "https://www.googleapis.com/auth/gmail.send"
)

// gmailScopesForStage picks the Gmail scope set for the requested OAuth stage.
// The default (connect) stage is least-privilege read-only; the "send" stage
// adds gmail.send on top of read (an incremental-consent upgrade), never
// requesting the full-mailbox scope.
func gmailScopesForStage(stage string) []string {
	if strings.EqualFold(strings.TrimSpace(stage), "send") {
		return []string{gmailScopeReadonly, gmailScopeSend, "openid", "email", "profile"}
	}
	return []string{gmailScopeReadonly, "openid", "email", "profile"}
}

// EmailOAuthCredentialOverride, when set, supplies OAuth client credentials
// (e.g. from in-app Settings) that take precedence over the ORI_EMAIL_*
// environment variables — so a self-hosted user can configure Google OAuth
// entirely in-app. The server wires it once at startup from config.Manager. A
// resolver that returns empty strings falls back to the env vars.
var EmailOAuthCredentialOverride func(provider vault.EmailProvider) (clientID, clientSecret string)

// resolveEmailOAuthCredentials returns the client id/secret for provider,
// preferring the configured override over the environment.
func resolveEmailOAuthCredentials(provider vault.EmailProvider, envID, envSecret string) (string, string) {
	if EmailOAuthCredentialOverride != nil {
		if id, secret := EmailOAuthCredentialOverride(provider); strings.TrimSpace(id) != "" && strings.TrimSpace(secret) != "" {
			return strings.TrimSpace(id), strings.TrimSpace(secret)
		}
	}
	return envID, envSecret
}

func loadEmailOAuthProviderConfig(provider vault.EmailProvider, r *http.Request, redirectOverride string) emailOAuthProviderConfig {
	redirectURL := firstNonEmpty(
		strings.TrimSpace(redirectOverride),
		strings.TrimSpace(os.Getenv("ORI_EMAIL_"+providerEnvPrefix(provider)+"_REDIRECT_URL")),
		buildEmailOAuthRedirectURL(r),
	)

	switch provider {
	case vault.EmailProviderGmail:
		stage := ""
		if r != nil {
			stage = r.URL.Query().Get("stage")
		}
		clientID, clientSecret := resolveEmailOAuthCredentials(provider,
			strings.TrimSpace(os.Getenv("ORI_EMAIL_GOOGLE_CLIENT_ID")),
			strings.TrimSpace(os.Getenv("ORI_EMAIL_GOOGLE_CLIENT_SECRET")))
		return emailOAuthProviderConfig{
			provider:      provider,
			label:         "Google",
			authURL:       firstNonEmpty(strings.TrimSpace(os.Getenv("ORI_EMAIL_GOOGLE_AUTH_URL")), "https://accounts.google.com/o/oauth2/v2/auth"),
			tokenURL:      firstNonEmpty(strings.TrimSpace(os.Getenv("ORI_EMAIL_GOOGLE_TOKEN_URL")), "https://oauth2.googleapis.com/token"),
			clientID:      clientID,
			clientSecret:  clientSecret,
			redirectURL:   redirectURL,
			scopes:        gmailScopesForStage(stage),
			defaultSource: "google-oauth",
		}
	case vault.EmailProviderMicrosoft:
		return emailOAuthProviderConfig{
			provider:      provider,
			label:         "Microsoft",
			authURL:       firstNonEmpty(strings.TrimSpace(os.Getenv("ORI_EMAIL_MICROSOFT_AUTH_URL")), "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"),
			tokenURL:      firstNonEmpty(strings.TrimSpace(os.Getenv("ORI_EMAIL_MICROSOFT_TOKEN_URL")), "https://login.microsoftonline.com/common/oauth2/v2.0/token"),
			clientID:      strings.TrimSpace(os.Getenv("ORI_EMAIL_MICROSOFT_CLIENT_ID")),
			clientSecret:  strings.TrimSpace(os.Getenv("ORI_EMAIL_MICROSOFT_CLIENT_SECRET")),
			redirectURL:   redirectURL,
			scopes:        []string{"offline_access", "openid", "profile", "email", "https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send"},
			defaultSource: "microsoft-oauth",
		}
	default:
		return emailOAuthProviderConfig{provider: provider, redirectURL: redirectURL}
	}
}

func providerEnvPrefix(provider vault.EmailProvider) string {
	switch provider {
	case vault.EmailProviderGmail:
		return "GOOGLE"
	case vault.EmailProviderMicrosoft:
		return "MICROSOFT"
	default:
		return strings.ToUpper(strings.ReplaceAll(string(provider), "-", "_"))
	}
}

func buildEmailOAuthRedirectURL(r *http.Request) string {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ORI_EMAIL_OAUTH_BASE_URL")), "/")
	if baseURL == "" && r != nil {
		scheme := firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), strings.TrimSpace(r.URL.Scheme))
		if scheme == "" {
			if r.TLS != nil {
				scheme = "https"
			} else {
				scheme = "http"
			}
		}
		host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
		if host == "" {
			host = strings.TrimSpace(r.Host)
		}
		if host != "" {
			baseURL = scheme + "://" + host
		}
	}
	if baseURL == "" {
		return ""
	}
	return baseURL + "/api/vault/email-oauth/callback"
}

func (c emailOAuthProviderConfig) isConfigured() bool {
	return c.clientID != "" && c.clientSecret != "" && c.redirectURL != "" && c.authURL != "" && c.tokenURL != ""
}

func (c emailOAuthProviderConfig) disabledReason() string {
	switch {
	case c.provider == vault.EmailProviderIMAPSMTP:
		return "Custom IMAP / SMTP accounts use manual credentials or advanced token import."
	case c.provider != vault.EmailProviderGmail && c.provider != vault.EmailProviderMicrosoft:
		return "OAuth connect is unavailable for this provider."
	case c.clientID == "" || c.clientSecret == "":
		return fmt.Sprintf("%s OAuth is not configured on this Ori server yet.", c.label)
	case c.redirectURL == "":
		return fmt.Sprintf("%s OAuth could not determine a callback URL for this Ori server.", c.label)
	default:
		return ""
	}
}

func (c emailOAuthProviderConfig) oauthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.clientID,
		ClientSecret: c.clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  c.authURL,
			TokenURL: c.tokenURL,
		},
		RedirectURL: c.redirectURL,
		Scopes:      append([]string{}, c.scopes...),
	}
}

func (h *Handler) handleEmailOAuthProviders(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	providers := []emailOAuthProviderStatus{
		providerStatusForRequest(vault.EmailProviderGmail, r),
		providerStatusForRequest(vault.EmailProviderMicrosoft, r),
		providerStatusForRequest(vault.EmailProviderIMAPSMTP, r),
	}
	orihttp.Success(w, map[string]any{
		"providers": providers,
	})
}

func (h *Handler) handleEmailOAuthStart(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	provider := vault.EmailProvider(strings.TrimSpace(r.URL.Query().Get("provider")))
	cfg := loadEmailOAuthProviderConfig(provider, r, "")
	if !cfg.isConfigured() {
		writeEmailOAuthPopupResult(w, http.StatusServiceUnavailable, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: cfg.disabledReason(),
		})
		return
	}

	vaultID := vaultIDFromRequest(r)
	if vaultID == "" {
		writeEmailOAuthPopupResult(w, http.StatusBadRequest, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: vault.ErrVaultRequired.Error(),
		})
		return
	}

	status, err := h.store.Status(r.Context(), vaultID)
	if err != nil {
		writeEmailOAuthPopupResult(w, vaultErrorStatus(err), emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: err.Error(),
		})
		return
	}
	if !status.Available {
		writeEmailOAuthPopupResult(w, http.StatusNotFound, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: vault.ErrVaultNotFound.Error(),
		})
		return
	}
	if status.Locked {
		writeEmailOAuthPopupResult(w, http.StatusLocked, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: "Unlock this vault before connecting an email account.",
		})
		return
	}

	flow, err := h.buildPendingEmailOAuthFlow(r.Context(), r, provider, vaultID, cfg)
	if err != nil {
		writeEmailOAuthPopupResult(w, vaultErrorStatus(err), emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: err.Error(),
		})
		return
	}

	if h.oauthFlows == nil {
		h.oauthFlows = newEmailOAuthFlowStore(nil, 10*time.Minute)
	}

	flow.CodeVerifier = oauth2.GenerateVerifier()
	flow.RedirectURL = cfg.redirectURL

	state := h.oauthFlows.put(flow)

	authOpts := []oauth2.AuthCodeOption{
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(flow.CodeVerifier),
	}
	if strings.Contains(flow.EmailAddress, "@") {
		authOpts = append(authOpts, oauth2.SetAuthURLParam("login_hint", flow.EmailAddress))
	}
	switch provider {
	case vault.EmailProviderGmail:
		authOpts = append(authOpts, oauth2.SetAuthURLParam("prompt", "consent"))
	case vault.EmailProviderMicrosoft:
		authOpts = append(authOpts, oauth2.SetAuthURLParam("prompt", "select_account"))
	}

	http.Redirect(w, r, cfg.oauthConfig().AuthCodeURL(state, authOpts...), http.StatusFound)
}

func (h *Handler) buildPendingEmailOAuthFlow(ctx context.Context, r *http.Request, provider vault.EmailProvider, vaultID string, cfg emailOAuthProviderConfig) (pendingEmailOAuthFlow, error) {
	flow := pendingEmailOAuthFlow{
		Provider:        provider,
		VaultID:         vaultID,
		AccountID:       strings.TrimSpace(r.URL.Query().Get("account_id")),
		Label:           strings.TrimSpace(r.URL.Query().Get("label")),
		EmailAddress:    strings.TrimSpace(r.URL.Query().Get("email_address")),
		DisplayName:     strings.TrimSpace(r.URL.Query().Get("display_name")),
		Username:        strings.TrimSpace(r.URL.Query().Get("username")),
		WorkspaceID:     strings.TrimSpace(r.URL.Query().Get("workspace_id")),
		Tags:            normalizeTags(strings.Split(strings.TrimSpace(r.URL.Query().Get("tags")), ",")),
		Source:          strings.TrimSpace(r.URL.Query().Get("source")),
		RetentionPolicy: strings.TrimSpace(r.URL.Query().Get("retention_policy")),
		RedirectURL:     cfg.redirectURL,
	}

	if flow.AccountID == "" {
		if flow.EmailAddress == "" {
			return flow, fmt.Errorf("%w: email_address is required before starting OAuth", vault.ErrInvalidEmailAccount)
		}
		if flow.Source == "" {
			flow.Source = cfg.defaultSource
		}
		return flow, nil
	}

	account, err := h.store.GetEmailAccount(ctx, flow.AccountID)
	if err != nil {
		return flow, err
	}
	if strings.TrimSpace(account.VaultID) != vaultID {
		return flow, fmt.Errorf("%w: selected email account does not belong to this vault", vault.ErrInvalidEmailAccount)
	}
	if vault.NormalizeEmailProvider(account.Provider) != vault.NormalizeEmailProvider(provider) {
		return flow, fmt.Errorf("%w: reconnecting an account cannot change providers", vault.ErrInvalidEmailAccount)
	}

	flow.Label = firstNonEmpty(flow.Label, account.Label)
	flow.EmailAddress = firstNonEmpty(flow.EmailAddress, account.EmailAddress)
	flow.DisplayName = firstNonEmpty(flow.DisplayName, account.DisplayName)
	flow.Username = firstNonEmpty(flow.Username, account.Username)
	flow.WorkspaceID = firstNonEmpty(flow.WorkspaceID, account.WorkspaceID)
	if len(flow.Tags) == 0 {
		flow.Tags = append([]string{}, account.Tags...)
	}
	flow.Source = firstNonEmpty(flow.Source, account.Source, cfg.defaultSource)
	flow.RetentionPolicy = firstNonEmpty(flow.RetentionPolicy, account.RetentionPolicy)
	if flow.EmailAddress == "" {
		return flow, fmt.Errorf("%w: email_address is required before reconnecting OAuth", vault.ErrInvalidEmailAccount)
	}
	return flow, nil
}

func (h *Handler) handleEmailOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		writeEmailOAuthPopupResult(w, http.StatusBadRequest, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: "OAuth callback state is missing.",
		})
		return
	}

	if h.oauthFlows == nil {
		writeEmailOAuthPopupResult(w, http.StatusBadRequest, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: "This email connection request expired. Start the connection again from the vault page.",
		})
		return
	}

	flow, ok := h.oauthFlows.take(state)
	if !ok {
		writeEmailOAuthPopupResult(w, http.StatusBadRequest, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: "This email connection request expired. Start the connection again from the vault page.",
		})
		return
	}

	if errMessage := strings.TrimSpace(r.URL.Query().Get("error")); errMessage != "" {
		description := strings.TrimSpace(r.URL.Query().Get("error_description"))
		if description == "" {
			description = errMessage
		}
		writeEmailOAuthPopupResult(w, http.StatusBadRequest, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: fmt.Sprintf("Email connection was canceled: %s", description),
		})
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeEmailOAuthPopupResult(w, http.StatusBadRequest, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: "OAuth provider did not return an authorization code.",
		})
		return
	}

	cfg := loadEmailOAuthProviderConfig(flow.Provider, r, flow.RedirectURL)
	if !cfg.isConfigured() {
		writeEmailOAuthPopupResult(w, http.StatusServiceUnavailable, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: cfg.disabledReason(),
		})
		return
	}

	token, err := cfg.oauthConfig().Exchange(r.Context(), code, oauth2.VerifierOption(flow.CodeVerifier))
	if err != nil {
		writeEmailOAuthPopupResult(w, http.StatusBadGateway, emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: "Ori could not complete the email OAuth exchange. Try again.",
		})
		return
	}

	account, err := h.persistEmailOAuthAccount(r.Context(), flow, cfg, token)
	if err != nil {
		writeEmailOAuthPopupResult(w, vaultErrorStatus(err), emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: err.Error(),
		})
		return
	}

	writeEmailOAuthPopupResult(w, http.StatusOK, emailOAuthPopupPayload{
		Type:    emailOAuthPopupEventType,
		Success: true,
		Message: fmt.Sprintf("%s account connected.", cfg.label),
		Account: account,
	})
}

func (h *Handler) persistEmailOAuthAccount(ctx context.Context, flow pendingEmailOAuthFlow, cfg emailOAuthProviderConfig, token *oauth2.Token) (*vault.EmailAccount, error) {
	if token == nil {
		return nil, fmt.Errorf("%w: missing oauth token", vault.ErrInvalidEmailAccount)
	}

	accessToken := strings.TrimSpace(token.AccessToken)
	refreshToken := strings.TrimSpace(token.RefreshToken)
	if accessToken == "" && refreshToken == "" {
		return nil, fmt.Errorf("%w: oauth provider did not return an access or refresh token", vault.ErrInvalidEmailAccount)
	}

	if flow.AccountID != "" {
		update := vault.EmailAccountUpdate{
			Label:           stringPointer(flow.Label),
			WorkspaceID:     stringPointer(flow.WorkspaceID),
			Tags:            slicePointer(flow.Tags),
			Source:          stringPointer(firstNonEmpty(flow.Source, cfg.defaultSource)),
			RetentionPolicy: stringPointer(flow.RetentionPolicy),
			Provider:        providerPointer(flow.Provider),
			EmailAddress:    stringPointer(flow.EmailAddress),
			DisplayName:     stringPointer(flow.DisplayName),
			Username:        stringPointer(flow.Username),
			AuthType:        authTypePointer(vault.EmailAuthTypeOAuth2),
			Password:        stringPointer(""),
			IMAPHost:        nil,
			SMTPHost:        nil,
			AccessToken:     stringPointer(accessToken),
			RefreshToken:    stringPointer(refreshToken),
			ClientID:        stringPointer(cfg.clientID),
			ClientSecret:    stringPointer(cfg.clientSecret),
			TokenEndpoint:   stringPointer(cfg.tokenURL),
		}
		return h.store.UpdateEmailAccount(ctx, flow.AccountID, update)
	}

	return h.store.CreateEmailAccount(ctx, vault.EmailAccountInput{
		VaultID:         flow.VaultID,
		WorkspaceID:     flow.WorkspaceID,
		Label:           flow.Label,
		Tags:            append([]string{}, flow.Tags...),
		Source:          firstNonEmpty(flow.Source, cfg.defaultSource),
		RetentionPolicy: flow.RetentionPolicy,
		Provider:        flow.Provider,
		EmailAddress:    flow.EmailAddress,
		DisplayName:     flow.DisplayName,
		Username:        flow.Username,
		AuthType:        vault.EmailAuthTypeOAuth2,
		Credentials: vault.EmailAccountCredentials{
			AccessToken:   accessToken,
			RefreshToken:  refreshToken,
			ClientID:      cfg.clientID,
			ClientSecret:  cfg.clientSecret,
			TokenEndpoint: cfg.tokenURL,
		},
	})
}

func normalizeTags(values []string) []string {
	tags := make([]string, 0, len(values))
	for _, value := range values {
		tag := strings.ToLower(strings.TrimSpace(value))
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func writeEmailOAuthPopupResult(w http.ResponseWriter, status int, payload emailOAuthPopupPayload) {
	if payload.Type == "" {
		payload.Type = emailOAuthPopupEventType
	}
	if status <= 0 {
		status = http.StatusOK
	}

	data, err := json.Marshal(payload)
	if err != nil {
		status = http.StatusInternalServerError
		payload = emailOAuthPopupPayload{
			Type:  emailOAuthPopupEventType,
			Error: "Ori could not finish rendering the email connection result.",
		}
		data, _ = json.Marshal(payload)
	}

	title := "Email Connected"
	headline := "You can close this window."
	bodyClass := "is-success"
	bodyCopy := "Ori saved the mailbox credentials to the selected vault."
	if !payload.Success {
		title = "Connection Incomplete"
		headline = "This account is not connected yet."
		bodyClass = "is-error"
		bodyCopy = firstNonEmpty(payload.Error, "Ori could not complete the mailbox connection.")
	}

	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	orihttp.WriteHTML(w, fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <style>
    :root {
      color-scheme: light dark;
      --bg: #f6efe4;
      --panel: rgba(255, 252, 246, 0.92);
      --border: rgba(173, 129, 84, 0.22);
      --text: #2f2418;
      --muted: #6d5943;
      --accent: #b56a2c;
      --accent-soft: rgba(181, 106, 44, 0.12);
      --danger: #a43f2f;
      --danger-soft: rgba(164, 63, 47, 0.12);
    }
    @media (prefers-color-scheme: dark) {
      :root {
        --bg: #151b28;
        --panel: rgba(29, 36, 51, 0.92);
        --border: rgba(219, 179, 132, 0.18);
        --text: #f7f0e6;
        --muted: #d5c2aa;
        --accent: #f0a15d;
        --accent-soft: rgba(240, 161, 93, 0.14);
        --danger: #ef8f7b;
        --danger-soft: rgba(239, 143, 123, 0.14);
      }
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      padding: 24px;
      background:
        radial-gradient(circle at top left, rgba(255, 217, 178, 0.34), transparent 32%%),
        linear-gradient(180deg, var(--bg), color-mix(in srgb, var(--bg), #ffffff 4%%));
      color: var(--text);
      font: 15px/1.6 "Iowan Old Style", "Palatino Linotype", "Book Antiqua", serif;
    }
    .card {
      width: min(100%%, 420px);
      padding: 28px;
      border-radius: 24px;
      border: 1px solid var(--border);
      background: var(--panel);
      box-shadow: 0 18px 48px rgba(53, 34, 16, 0.12);
    }
    .eyebrow {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      margin-bottom: 14px;
      padding: 6px 10px;
      border-radius: 999px;
      background: %s;
      color: %s;
      font-size: 12px;
      font-weight: 700;
      letter-spacing: 0.08em;
      text-transform: uppercase;
    }
    h1 {
      margin: 0;
      font-size: 29px;
      line-height: 1.08;
    }
    p {
      margin: 12px 0 0;
      color: var(--muted);
    }
  </style>
</head>
<body>
  <main class="card %s">
    <div class="eyebrow">%s</div>
    <h1>%s</h1>
    <p>%s</p>
  </main>
  <script>
    const payload = %s;
    try {
      if (window.opener && typeof window.opener.postMessage === "function") {
        window.opener.postMessage(payload, window.location.origin);
        window.setTimeout(() => window.close(), 120);
      }
    } catch (error) {
      console.error("Failed to deliver OAuth result:", error);
    }
  </script>
</body>
</html>`,
		html.EscapeString(title),
		map[bool]string{true: "var(--accent-soft)", false: "var(--danger-soft)"}[payload.Success],
		map[bool]string{true: "var(--accent)", false: "var(--danger)"}[payload.Success],
		bodyClass,
		html.EscapeString(map[bool]string{true: "Connected", false: "Needs Attention"}[payload.Success]),
		html.EscapeString(headline),
		html.EscapeString(bodyCopy),
		strings.ReplaceAll(string(data), "</", `<\/`),
	))
}

func stringPointer(value string) *string {
	return &value
}

func slicePointer(values []string) *[]string {
	copyValues := append([]string{}, values...)
	return &copyValues
}

func providerPointer(value vault.EmailProvider) *vault.EmailProvider {
	return &value
}

func authTypePointer(value vault.EmailAuthType) *vault.EmailAuthType {
	return &value
}
