package connections

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// This file implements the base-identity Connect-Google flow: the Ori-owned
// Google Desktop OAuth client, authorization-code + PKCE, `openid` scopes, an
// account chooser, and offline access (FR 6–8, 16–24). It establishes the Google
// identity only — product grants (Gmail/Calendar/Drive) are requested separately
// when the user enables a product (FR 12). Token exchange and ID-token
// validation reuse the state store, verifier, and store built in Group 2.

const (
	googleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	// #nosec G101 -- public Google OAuth token endpoint URL, not a credential
	googleTokenURL = "https://oauth2.googleapis.com/token"
)

// IdentityScopes are the OIDC scopes for establishing the base identity. They
// grant no product access (FR 12, 43).
var IdentityScopes = []string{"openid", "email", "profile"}

var (
	// ErrOAuthNotConfigured means no Ori Google OAuth client is available (no
	// baked-in client in this build and no env override).
	ErrOAuthNotConfigured = errors.New("connections: google oauth client is not configured")
	// ErrExpiredFlow means the callback's state is unknown, already used, or
	// expired — treat as an expired/replayed link, never a fresh error (FR 20).
	ErrExpiredFlow = errors.New("connections: authorization link expired or already used")
	// ErrAuthorizationDenied means the user canceled or Google returned an error.
	ErrAuthorizationDenied = errors.New("connections: authorization was denied or canceled")
	// ErrNoIDToken means the token response carried no id_token, so no verifiable
	// identity could be established.
	ErrNoIDToken = errors.New("connections: token response did not include an id_token")
	// ErrDifferentAccountActive means a different Google account is already the
	// active connection; V1 requires an explicit disconnect before switching (FR 5, 73).
	ErrDifferentAccountActive = errors.New("connections: a different Google account is already connected; disconnect it first")
)

// OAuthConfig is the Ori-owned Google Desktop OAuth client configuration. In
// official builds ClientID is baked in; in development it comes from env. A
// Desktop client's secret is not confidential and may be empty when PKCE is used.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	AuthURL      string // defaults to Google's endpoint when empty (overridable in tests)
	TokenURL     string
	// Verdict is the validation result for ClientID as the operator configured it
	// (FR 63-65). Its zero value means "not checked" and is treated as usable, so
	// callers that build a config directly are unaffected.
	Verdict ClientVerdict
}

// IsConfigured reports whether a client id is present.
func (c OAuthConfig) IsConfigured() bool { return strings.TrimSpace(c.ClientID) != "" }

// ClientConfigError means the configured OAuth client cannot work — a defect
// detectable before any browser flow starts, unlike "not configured at all".
type ClientConfigError struct {
	Verdict ClientVerdict
}

func (e *ClientConfigError) Error() string {
	return "connections: google oauth client is misconfigured: " + string(e.Verdict.Problem)
}

// Is lets existing not-configured handling still catch this, while callers that
// want the specific guidance use errors.As.
func (e *ClientConfigError) Is(target error) bool { return target == ErrOAuthNotConfigured }

// checkClient rejects a configured-but-unusable client up front.
func (c OAuthConfig) checkClient() error {
	if c.Verdict.OK() {
		return nil
	}
	return &ClientConfigError{Verdict: c.Verdict}
}

// tokenURL returns the effective token endpoint (Google's default when unset).
func (c OAuthConfig) tokenURL() string {
	if c.TokenURL != "" {
		return c.TokenURL
	}
	return googleTokenURL
}

func (c OAuthConfig) oauth2Config(redirectURL string, scopes []string) *oauth2.Config {
	authURL := c.AuthURL
	if authURL == "" {
		authURL = googleAuthURL
	}
	tokenURL := c.TokenURL
	if tokenURL == "" {
		tokenURL = googleTokenURL
	}
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
		Endpoint:     oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL},
	}
}

// IDVerifier validates a raw ID token and its nonce, returning the identity.
// *GoogleVerifier satisfies it; tests inject a fake.
type IDVerifier interface {
	Verify(ctx context.Context, rawIDToken, expectedNonce string) (Identity, error)
}

// IdentityFlow drives the Connect-Google identity handshake and product
// enablement (see product.go).
type IdentityFlow struct {
	config   OAuthConfig
	states   *StateStore
	store    *Store
	verifier IDVerifier
	sink     CredentialSink
	vaults   VaultCatalog
	newID    func() string
	now      func() time.Time
}

// NewIdentityFlow wires the flow to its collaborators.
func NewIdentityFlow(config OAuthConfig, states *StateStore, store *Store, verifier IDVerifier) *IdentityFlow {
	return &IdentityFlow{
		config:   config,
		states:   states,
		store:    store,
		verifier: verifier,
		newID:    uuid.NewString,
		now:      time.Now,
	}
}

// BeginConnectParams are the inputs to start a Connect-Google flow.
type BeginConnectParams struct {
	LocalUserID string
	RedirectURL string // the loopback callback Ori will receive on
	ReturnTo    string // where the UI should return afterward
	// VaultID is the user's explicit vault choice for a product credential,
	// supplied when resuming after a choose/create/repair prompt (FR 6, 9). Empty
	// means "use whatever the connection already remembers".
	VaultID string
}

// BeginConnectResult carries the authorize URL the frontend opens in the system
// browser and the state value bound to it.
type BeginConnectResult struct {
	AuthorizeURL string
	State        string
	// CorrelationID is the safe-to-log handle for this authorization; the matching
	// callback failure reports the same value (FR 20).
	CorrelationID string
}

// BeginConnect generates PKCE material and a state/nonce, records the pending
// authorization, and returns the Google authorize URL. The URL always requests
// the account chooser (`prompt=select_account`) and offline access.
func (f *IdentityFlow) BeginConnect(p BeginConnectParams) (BeginConnectResult, error) {
	if !f.config.IsConfigured() {
		return BeginConnectResult{}, ErrOAuthNotConfigured
	}
	if err := f.config.checkClient(); err != nil {
		return BeginConnectResult{}, err
	}
	codeVerifier := oauth2.GenerateVerifier()
	pending, err := f.states.Begin(BeginParams{
		LocalUserID:  p.LocalUserID,
		ReturnTo:     p.ReturnTo,
		CallbackURI:  p.RedirectURL,
		CodeVerifier: codeVerifier,
	})
	if err != nil {
		return BeginConnectResult{}, err
	}

	authorizeURL := f.config.oauth2Config(p.RedirectURL, IdentityScopes).AuthCodeURL(
		pending.State,
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(codeVerifier),
		oauth2.SetAuthURLParam("nonce", pending.Nonce),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
	return BeginConnectResult{AuthorizeURL: authorizeURL, State: pending.State, CorrelationID: pending.CorrelationID}, nil
}

// CompleteConnectParams are the callback inputs.
type CompleteConnectParams struct {
	State      string
	Code       string
	OAuthError string // non-empty when Google redirected with error=...
}

// Complete consumes an OAuth callback and dispatches on the product recorded in
// the pending state: a base identity connect, or a product enablement (Gmail).
// A single callback route can therefore serve every flow. An unknown/expired
// state falls through to the identity path, which reports ErrExpiredFlow.
func (f *IdentityFlow) Complete(ctx context.Context, p CompleteConnectParams) (*Connection, error) {
	if product, ok := f.states.Product(p.State); ok && product == ProductGmail {
		return f.CompleteEnableGmail(ctx, p)
	}
	return f.CompleteConnect(ctx, p)
}

// CompleteConnect consumes the callback: it validates the state, exchanges the
// code (with PKCE), verifies the returned ID token and nonce, and persists the
// identity keyed on the Google subject. Reconnecting the same subject preserves
// the existing connection's vault and product grants; a different subject is
// rejected (the user must Switch Account) per FR 5/73.
func (f *IdentityFlow) CompleteConnect(ctx context.Context, p CompleteConnectParams) (*Connection, error) {
	pending, ok := f.states.Consume(p.State)
	if !ok {
		return nil, ErrExpiredFlow
	}
	if strings.TrimSpace(p.OAuthError) != "" {
		return nil, fmt.Errorf("%w: %s", ErrAuthorizationDenied, p.OAuthError)
	}
	if strings.TrimSpace(p.Code) == "" {
		return nil, ErrAuthorizationDenied
	}

	cfg := f.config.oauth2Config(pending.CallbackURI, IdentityScopes)
	token, err := cfg.Exchange(ctx, p.Code, oauth2.VerifierOption(pending.CodeVerifier))
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	rawID, _ := token.Extra("id_token").(string)
	if strings.TrimSpace(rawID) == "" {
		return nil, ErrNoIDToken
	}
	identity, err := f.verifier.Verify(ctx, rawID, pending.Nonce)
	if err != nil {
		return nil, err
	}

	now := f.now()
	conn := &Connection{
		ID:          f.newID(),
		Provider:    ProviderGoogle,
		Subject:     identity.Subject,
		Email:       identity.Email,
		DisplayName: identity.Name,
		AvatarURL:   identity.Picture,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	existing, err := f.store.Load()
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Subject != "" {
		if existing.Subject != identity.Subject {
			return nil, ErrDifferentAccountActive
		}
		// Same account reconnect: preserve id, vault, and product grants (FR 40, 74).
		conn.ID = existing.ID
		conn.VaultID = existing.VaultID
		conn.Grants = existing.Grants
		conn.CreatedAt = existing.CreatedAt
	}

	if err := f.store.Save(conn); err != nil {
		return nil, err
	}
	return conn, nil
}
