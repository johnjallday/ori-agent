package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// Sentinel errors surfaced from the remote-connect path. Server.startRemote
// maps them to StatusAuthRequired (rather than the generic StatusError) so
// the UI can offer a single, consistent "reconnect" affordance.
var (
	ErrOAuthCredentialsRequired = errors.New("mcp: oauth client credentials are required")
	ErrOAuthDenied              = errors.New("mcp: oauth authorization was denied or failed")
	ErrOAuthTimeout             = errors.New("mcp: oauth authorization timed out")
)

func isOAuthReconnectError(err error) bool {
	return errors.Is(err, ErrOAuthCredentialsRequired) ||
		errors.Is(err, ErrOAuthDenied) ||
		errors.Is(err, ErrOAuthTimeout)
}

// RemoteCredential is the decrypted OAuth credential material for one remote
// MCP server's AuthRef. It never appears in ServerConfig, mcp_registry.json,
// or an HTTP response; it lives only in the vault and, transiently, in
// memory while a connect attempt is running.
type RemoteCredential struct {
	AuthRef       string
	ServerName    string
	ClientID      string
	ClientSecret  string
	AccessToken   string
	RefreshToken  string
	TokenType     string
	TokenEndpoint string
	Expiry        time.Time
	Scopes        []string
}

// HasToken reports whether cred carries enough material to build a token
// source without a fresh interactive authorization.
func (c RemoteCredential) HasToken() bool {
	return strings.TrimSpace(c.AccessToken) != "" || strings.TrimSpace(c.RefreshToken) != ""
}

// RemoteCredentialStore persists and retrieves the OAuth credential material
// referenced by a remote server's AuthRef. Implemented by a vault-backed
// adapter in server wiring (internal/vault) so this package stays
// storage-agnostic and provider-neutral.
type RemoteCredentialStore interface {
	// LoadCredential returns the current credential for authRef, or
	// ok == false if none has been configured yet.
	LoadCredential(ctx context.Context, authRef string) (cred RemoteCredential, ok bool, err error)
	// SaveCredential creates or updates the credential referenced by
	// cred.AuthRef (which must be non-empty).
	SaveCredential(ctx context.Context, cred RemoteCredential) error
	// DeleteCredential removes the credential record. Deleting a
	// nonexistent record is not an error.
	DeleteCredential(ctx context.Context, authRef string) error
}

var (
	remoteCredentialStoreMu sync.RWMutex
	remoteCredentialStore   RemoteCredentialStore
	resolveOAuthUserID      = func(context.Context) (string, error) { return "local", nil }
)

// ConfigureRemoteOAuth wires the vault-backed credential store and the
// current-user resolver into the mcp package. Called once during server
// wiring (see internal/server), after the vault store exists — mirroring the
// SetWorkspaceStore/SetVaultRootUpdater lazy-wiring pattern used elsewhere,
// since the vault store isn't available yet when the MCP registry itself is
// constructed.
func ConfigureRemoteOAuth(store RemoteCredentialStore, userResolver func(context.Context) (string, error)) {
	remoteCredentialStoreMu.Lock()
	remoteCredentialStore = store
	remoteCredentialStoreMu.Unlock()
	if userResolver != nil {
		resolveOAuthUserID = userResolver
	}
}

func credentialStore() RemoteCredentialStore {
	remoteCredentialStoreMu.RLock()
	defer remoteCredentialStoreMu.RUnlock()
	return remoteCredentialStore
}

// SaveOAuthClientCredentials persists the user-supplied OAuth client id/secret
// for a remote server before the first connect attempt. Called by the HTTP
// connect handler; Start()/buildOAuthHandler only ever reads.
func SaveOAuthClientCredentials(ctx context.Context, cfg ServerConfig, clientID, clientSecret string) error {
	store := credentialStore()
	if store == nil {
		return fmt.Errorf("mcp: remote credential store is not configured")
	}
	authRef := NormalizedAuthRef(cfg)
	existing, ok, err := store.LoadCredential(ctx, authRef)
	if err != nil {
		return err
	}
	cred := RemoteCredential{AuthRef: authRef, ServerName: cfg.Name}
	if ok {
		cred = existing
	}
	cred.AuthRef = authRef
	cred.ServerName = cfg.Name
	cred.ClientID = strings.TrimSpace(clientID)
	cred.ClientSecret = strings.TrimSpace(clientSecret)
	return store.SaveCredential(ctx, cred)
}

// HasOAuthCredentials reports whether client credentials have been submitted
// for cfg yet (regardless of whether authorization has completed).
func HasOAuthCredentials(ctx context.Context, cfg ServerConfig) (bool, error) {
	store := credentialStore()
	if store == nil {
		return false, nil
	}
	cred, ok, err := store.LoadCredential(ctx, NormalizedAuthRef(cfg))
	if err != nil || !ok {
		return false, err
	}
	return strings.TrimSpace(cred.ClientID) != "", nil
}

// DisconnectOAuth revokes a remote server's credentials locally by deleting
// its vault record. It does not affect other servers' credentials.
func DisconnectOAuth(ctx context.Context, cfg ServerConfig) error {
	store := credentialStore()
	if store == nil {
		return nil
	}
	return store.DeleteCredential(ctx, NormalizedAuthRef(cfg))
}

func resolveOAuthRedirectURL() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ORI_MCP_OAUTH_BASE_URL")); override != "" {
		return strings.TrimRight(override, "/") + "/api/mcp/oauth/callback", nil
	}
	port := 8765
	if raw := strings.TrimSpace(os.Getenv("PORT")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			port = parsed
		}
	}
	return fmt.Sprintf("http://localhost:%d/api/mcp/oauth/callback", port), nil
}

// --- Browser-authorization handoff -----------------------------------------
//
// auth.AuthorizationCodeHandler.Authorize calls our AuthorizationCodeFetcher
// synchronously (from inside the goroutine running Server.Start) and blocks
// until it returns an authorization code. We bridge that to Ori's HTTP
// server, which is a separate request: the fetcher publishes the
// SDK-generated authorize URL (extracted from the state query param) into an
// OAuthSessions entry, Server.Start() surfaces it via GetAuthorizeURL(), the
// frontend opens it in the system browser, and when the browser redirects
// back to /api/mcp/oauth/callback, DeliverOAuthCallback wakes the blocked
// fetcher with the code (or an error).

type oauthCallbackResult struct {
	code string
	err  error
}

type oauthSessionInfo struct {
	ServerName  string
	UserID      string
	RedirectURL string
}

type oauthSession struct {
	info      oauthSessionInfo
	createdAt time.Time
	resultCh  chan oauthCallbackResult
}

// OAuthSessions tracks in-flight browser-authorization handoffs keyed by the
// OAuth `state` value. Entries are single-use (removed on delivery) and
// expire on their own after ttl if the browser flow is abandoned.
type OAuthSessions struct {
	mu       sync.Mutex
	sessions map[string]*oauthSession
	ttl      time.Duration
	now      func() time.Time
}

func newOAuthSessions(ttl time.Duration) *OAuthSessions {
	return &OAuthSessions{sessions: make(map[string]*oauthSession), ttl: ttl, now: time.Now}
}

var globalOAuthSessions = newOAuthSessions(defaultMCPOAuthTimeout + time.Minute)

func (o *OAuthSessions) open(state string, info oauthSessionInfo) *oauthSession {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.evictExpiredLocked()
	session := &oauthSession{
		info:      info,
		createdAt: o.now(),
		resultCh:  make(chan oauthCallbackResult, 1),
	}
	o.sessions[state] = session
	return session
}

// deliver completes a pending session exactly once. ok is false if state is
// unknown, expired, or already consumed -- the caller should treat that as
// state-replay/expiry rather than a fresh error.
func (o *OAuthSessions) deliver(state string, result oauthCallbackResult) (oauthSessionInfo, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.evictExpiredLocked()
	session, ok := o.sessions[state]
	if !ok {
		return oauthSessionInfo{}, false
	}
	delete(o.sessions, state) // single-use: gone before we release the lock
	select {
	case session.resultCh <- result:
		return session.info, true
	default:
		return oauthSessionInfo{}, false
	}
}

// expire drops a session the fetcher gave up on (context deadline/cancel)
// without ever receiving a callback, so a late/duplicate browser redirect
// finds nothing to replay against.
func (o *OAuthSessions) expire(state string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.sessions, state)
}

func (o *OAuthSessions) evictExpiredLocked() {
	cutoff := o.now().Add(-o.ttl)
	for state, session := range o.sessions {
		if session.createdAt.Before(cutoff) {
			delete(o.sessions, state)
		}
	}
}

// DeliverOAuthCallback is called by the HTTP callback route with the raw
// query parameters from the browser redirect. It returns the bound server
// name so the handler can render a useful result page, and ok == false when
// state is missing/expired/already used (the handler should treat that as an
// expired-link error, not retry).
func DeliverOAuthCallback(state, code, oauthError, oauthErrorDescription string) (serverName string, ok bool) {
	state = strings.TrimSpace(state)
	if state == "" {
		return "", false
	}

	var result oauthCallbackResult
	if strings.TrimSpace(oauthError) != "" {
		desc := strings.TrimSpace(oauthErrorDescription)
		if desc == "" {
			desc = oauthError
		}
		result.err = fmt.Errorf("%w: %s", ErrOAuthDenied, desc)
	} else if strings.TrimSpace(code) == "" {
		result.err = fmt.Errorf("%w: authorization server did not return a code", ErrOAuthDenied)
	} else {
		result.code = code
	}

	info, delivered := globalOAuthSessions.deliver(state, result)
	if !delivered {
		return "", false
	}
	return info.ServerName, true
}

func stateFromAuthorizeURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid authorize url: %w", err)
	}
	state := strings.TrimSpace(parsed.Query().Get("state"))
	if state == "" {
		return "", fmt.Errorf("authorize url missing state parameter")
	}
	return state, nil
}

// --- Server-facing OAuth handler construction -------------------------------

// buildOAuthHandler resolves the auth.OAuthHandler used for one remote
// connect attempt. If a stored access/refresh token already exists, the
// returned handler's TokenSource is ready immediately (silent refresh, no
// browser interaction). Authorize() -- invoked by the transport only on a
// 401/403 -- lazily builds the SDK's full authorization-code flow.
func (s *Server) buildOAuthHandler(ctx context.Context, httpClient *http.Client) (auth.OAuthHandler, error) {
	store := credentialStore()
	if store == nil {
		return nil, fmt.Errorf("%w: no remote credential store configured", ErrOAuthCredentialsRequired)
	}

	authRef := NormalizedAuthRef(s.config)
	cred, ok, err := store.LoadCredential(ctx, authRef)
	if err != nil {
		return nil, err
	}
	if !ok || strings.TrimSpace(cred.ClientID) == "" {
		return nil, fmt.Errorf("%w: connect with an OAuth client id and secret first", ErrOAuthCredentialsRequired)
	}

	redirectURL, err := resolveOAuthRedirectURL()
	if err != nil {
		return nil, err
	}

	userID, err := resolveOAuthUserID(ctx)
	if err != nil {
		return nil, err
	}

	clientCreds := &oauthex.ClientCredentials{ClientID: cred.ClientID}
	if cred.ClientSecret != "" {
		clientCreds.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: cred.ClientSecret}
	}

	handler := &persistingOAuthHandler{
		store:       store,
		authRef:     authRef,
		serverName:  s.config.Name,
		mcpEndpoint: s.config.URL,
		redirectURL: redirectURL,
		httpClient:  httpClient,
		clientCreds: clientCreds,
		fetcher:     s.oauthFetcher(userID, redirectURL),
	}

	if cred.HasToken() && strings.TrimSpace(cred.TokenEndpoint) != "" {
		// oauth2.Config.TokenSource silently falls back to http.DefaultClient
		// for refresh requests unless the client is threaded through the
		// context this way -- without it, refresh would bypass our SSRF
		// hardening entirely rather than erroring loudly.
		refreshCtx := context.WithValue(ctx, oauth2.HTTPClient, httpClient)
		handler.tokenSource = newPersistingTokenSource(
			(&oauth2.Config{
				ClientID:     cred.ClientID,
				ClientSecret: cred.ClientSecret,
				Endpoint:     oauth2.Endpoint{TokenURL: cred.TokenEndpoint},
				RedirectURL:  redirectURL,
			}).TokenSource(refreshCtx, &oauth2.Token{
				AccessToken:  cred.AccessToken,
				RefreshToken: cred.RefreshToken,
				TokenType:    cred.TokenType,
				Expiry:       cred.Expiry,
			}),
			handler.persist,
		)
	}

	return handler, nil
}

// oauthFetcher returns the AuthorizationCodeFetcher passed to the SDK's
// AuthorizationCodeHandler. It publishes the authorize URL onto the server
// (so the HTTP connect handler can hand it to the browser) and blocks on the
// matching OAuthSessions entry until the callback route delivers a result or
// ctx (bounded by resolveMCPOAuthTimeout) is done.
func (s *Server) oauthFetcher(userID, redirectURL string) auth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		authorizeURL := args.URL
		// Google Workspace MCP servers authorize against accounts.google.com, so
		// also request the OIDC identity scopes: the token response then carries
		// an ID token Ori verifies to bind the grant to an account subject (FR 23).
		if isGoogleMCPEndpoint(s.config.URL) {
			if injected, injErr := injectGoogleIdentityScopes(authorizeURL); injErr == nil {
				authorizeURL = injected
			}
			// Pre-select the connected Google account so its subject matches the
			// active identity (FR 58 "use active Google account").
			if hint := googleMCPLoginHint; hint != nil {
				if hinted, hErr := injectGoogleLoginHint(authorizeURL, hint()); hErr == nil {
					authorizeURL = hinted
				}
			}
		}

		state, err := stateFromAuthorizeURL(authorizeURL)
		if err != nil {
			return nil, err
		}

		session := globalOAuthSessions.open(state, oauthSessionInfo{
			ServerName:  s.config.Name,
			UserID:      userID,
			RedirectURL: redirectURL,
		})

		s.setAuthorizeURL(authorizeURL)
		s.setStatus(StatusAuthRequired)

		select {
		case result := <-session.resultCh:
			if result.err != nil {
				return nil, result.err
			}
			return &auth.AuthorizationResult{Code: result.code, State: state}, nil
		case <-ctx.Done():
			globalOAuthSessions.expire(state)
			return nil, fmt.Errorf("%w: %v", ErrOAuthTimeout, ctx.Err())
		}
	}
}

// --- Token persistence -------------------------------------------------------

// persistingOAuthHandler implements auth.OAuthHandler. It composes a
// lazily-built SDK AuthorizationCodeHandler (only constructed if a 401/403
// actually requires a fresh interactive flow) with a vault-persisting token
// source, so every access/refresh cycle -- first authorization or silent
// background refresh -- writes the current token back to the vault.
type persistingOAuthHandler struct {
	store       RemoteCredentialStore
	authRef     string
	serverName  string
	mcpEndpoint string
	redirectURL string
	httpClient  *http.Client
	clientCreds *oauthex.ClientCredentials
	fetcher     auth.AuthorizationCodeFetcher

	mu          sync.Mutex
	tokenSource oauth2.TokenSource
	sdkHandler  *auth.AuthorizationCodeHandler
}

func (h *persistingOAuthHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tokenSource, nil
}

func (h *persistingOAuthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	h.mu.Lock()
	sdkHandler := h.sdkHandler
	h.mu.Unlock()

	if sdkHandler == nil {
		built, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
			PreregisteredClient:      h.clientCreds,
			RedirectURL:              h.redirectURL,
			AuthorizationCodeFetcher: h.fetcher,
			Client:                   h.httpClient,
		})
		if err != nil {
			return err
		}
		sdkHandler = built
		h.mu.Lock()
		h.sdkHandler = sdkHandler
		h.mu.Unlock()
	}

	if err := sdkHandler.Authorize(ctx, req, resp); err != nil {
		return err
	}

	inner, err := sdkHandler.TokenSource(ctx)
	if err != nil {
		return err
	}
	if inner == nil {
		return fmt.Errorf("%w: authorization completed without a token", ErrOAuthDenied)
	}
	token, err := inner.Token()
	if err != nil {
		return fmt.Errorf("mcp oauth: fetch token after authorize: %w", err)
	}

	// A Google MCP token carries an ID token (openid scope injected upstream);
	// surface it so the connection layer can verify the account subject and bind
	// the grant (FR 23). Best-effort: a missing/invalid ID token never blocks the
	// product-scoped grant itself.
	if hook := googleMCPIdentityHook; hook != nil && isGoogleMCPEndpoint(h.mcpEndpoint) {
		if rawID, _ := token.Extra("id_token").(string); strings.TrimSpace(rawID) != "" {
			clientID := ""
			if h.clientCreds != nil {
				clientID = h.clientCreds.ClientID
			}
			hook(h.serverName, h.mcpEndpoint, rawID, clientID)
		}
	}

	h.mu.Lock()
	h.tokenSource = newPersistingTokenSource(inner, h.persist)
	h.mu.Unlock()

	// Discover and persist the token endpoint now, once, so a future process
	// restart can refresh directly from the stored refresh token without
	// requiring another interactive browser flow.
	tokenEndpoint, discErr := discoverTokenEndpoint(ctx, h.mcpEndpoint, h.httpClient)
	if discErr != nil {
		logger.Warn("mcp oauth: could not discover token endpoint; reconnect will require re-authorization", logger.Fields{"server": h.serverName, "error": discErr})
	}
	h.persistWithEndpoint(ctx, token, tokenEndpoint)
	return nil
}

func (h *persistingOAuthHandler) persist(ctx context.Context, token *oauth2.Token) {
	h.persistWithEndpoint(ctx, token, "")
}

func (h *persistingOAuthHandler) persistWithEndpoint(ctx context.Context, token *oauth2.Token, tokenEndpoint string) {
	if token == nil || h.store == nil {
		return
	}
	cred := RemoteCredential{
		AuthRef:      h.authRef,
		ServerName:   h.serverName,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		Expiry:       token.Expiry,
	}
	if h.clientCreds != nil {
		cred.ClientID = h.clientCreds.ClientID
		if h.clientCreds.ClientSecretAuth != nil {
			cred.ClientSecret = h.clientCreds.ClientSecretAuth.ClientSecret
		}
	}
	if tokenEndpoint == "" {
		if existing, ok, err := h.store.LoadCredential(ctx, h.authRef); err == nil && ok {
			tokenEndpoint = existing.TokenEndpoint
			if cred.RefreshToken == "" {
				// A refresh response sometimes omits refresh_token when the
				// original grant is still valid; keep the last known one
				// rather than dropping it.
				cred.RefreshToken = existing.RefreshToken
			}
		}
	}
	cred.TokenEndpoint = tokenEndpoint

	if err := h.store.SaveCredential(ctx, cred); err != nil {
		logger.Warn("mcp oauth: failed to persist refreshed token", logger.Fields{"server": h.serverName, "error": err})
	}
}

// persistingTokenSource decorates an oauth2.TokenSource so every call that
// yields a *changed* access token (first authorization, or a transparent
// background refresh) is written back to the vault.
type persistingTokenSource struct {
	inner oauth2.TokenSource
	save  func(ctx context.Context, token *oauth2.Token)

	mu        sync.Mutex
	lastToken string
}

func newPersistingTokenSource(inner oauth2.TokenSource, save func(ctx context.Context, token *oauth2.Token)) *persistingTokenSource {
	return &persistingTokenSource{inner: inner, save: save}
}

func (t *persistingTokenSource) Token() (*oauth2.Token, error) {
	token, err := t.inner.Token()
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	changed := t.lastToken != token.AccessToken
	t.lastToken = token.AccessToken
	t.mu.Unlock()

	if changed {
		t.save(context.Background(), token)
	}
	return token, nil
}

// discoverTokenEndpoint resolves the OAuth token endpoint for an MCP server
// using the same public discovery primitives the SDK's own Authorize()
// implementation uses internally (protected-resource metadata, then
// authorization-server metadata), falling back to the 2025-03-26 spec's
// "server root is the authorization server" rule. The SDK does not expose
// the endpoint it discovers internally, so this duplicates that lookup
// deliberately -- it is the only way to persist a token endpoint for
// non-interactive refresh after a process restart.
func discoverTokenEndpoint(ctx context.Context, mcpEndpoint string, client *http.Client) (string, error) {
	base, err := url.Parse(mcpEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse mcp endpoint: %w", err)
	}

	type candidate struct{ metadataURL, resource string }
	var candidates []candidate

	withWellKnown := *base
	withWellKnown.Path = "/.well-known/oauth-protected-resource" + strings.TrimSuffix(base.Path, "/")
	candidates = append(candidates, candidate{metadataURL: withWellKnown.String(), resource: mcpEndpoint})

	rootWellKnown := *base
	rootWellKnown.Path = "/.well-known/oauth-protected-resource"
	rootResource := *base
	rootResource.Path = ""
	candidates = append(candidates, candidate{metadataURL: rootWellKnown.String(), resource: rootResource.String()})

	for _, c := range candidates {
		prm, err := oauthex.GetProtectedResourceMetadata(ctx, c.metadataURL, c.resource, client)
		if err != nil || prm == nil || len(prm.AuthorizationServers) == 0 {
			continue
		}
		if asm, err := auth.GetAuthServerMetadata(ctx, prm.AuthorizationServers[0], client); err == nil && asm != nil && strings.TrimSpace(asm.TokenEndpoint) != "" {
			return asm.TokenEndpoint, nil
		}
	}

	// 2025-03-26 fallback: the MCP server's own root is the authorization server.
	authServerRoot := *base
	authServerRoot.Path = ""
	if asm, err := auth.GetAuthServerMetadata(ctx, authServerRoot.String(), client); err == nil && asm != nil && strings.TrimSpace(asm.TokenEndpoint) != "" {
		return asm.TokenEndpoint, nil
	}

	return "", fmt.Errorf("could not discover an oauth token endpoint for %s", mcpEndpoint)
}
