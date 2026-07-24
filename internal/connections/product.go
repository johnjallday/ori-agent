package connections

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Gmail product enablement. Enabling Gmail re-authorizes the active Google
// identity with the read-only Gmail scope added, hands the resulting credential
// to the vault via CredentialSink, and marks the connection's Gmail grant
// Healthy (FR 25, 43, 48). The connection domain never persists tokens itself —
// CredentialSink returns an opaque reference that is all the grant stores.

// GmailReadonlyScope is the restricted, read-only Gmail scope (FR 43, 48).
const GmailReadonlyScope = "https://www.googleapis.com/auth/gmail.readonly"

// GmailSendScope is the separate send scope, requested only as an explicit
// upgrade (FR 44); sends still pass through the existing confirm-gated broker.
const GmailSendScope = "https://www.googleapis.com/auth/gmail.send"

var (
	// ErrNoCredentialSink means product enablement was attempted without a sink.
	ErrNoCredentialSink = errors.New("connections: no credential sink configured for product enablement")
	// ErrNoActiveIdentity means Gmail was enabled before a Google identity was
	// connected; the user must Connect Google first.
	ErrNoActiveIdentity = errors.New("connections: connect a Google account before enabling a product")
)

// GmailCredential is the credential material produced by enabling Gmail. It
// crosses to the vault via CredentialSink and must never be logged or returned
// to the browser.
type GmailCredential struct {
	Subject       string
	Email         string
	DisplayName   string
	AccessToken   string
	RefreshToken  string
	Expiry        time.Time
	GrantedScopes []string
	ClientID      string
	ClientSecret  string
	TokenEndpoint string
	VaultID       string
	// ExistingRef, when set, is the vault reference of the account this credential
	// should UPDATE in place (a re-auth or send upgrade) rather than create anew,
	// so a scope upgrade never orphans the prior account.
	ExistingRef string
}

// CredentialSink persists a product OAuth credential behind an opaque reference
// (e.g. a vault EmailAccount id). The connection domain depends only on this
// interface — never on internal/vault — which keeps the layering clean and the
// flow testable with a fake.
type CredentialSink interface {
	SaveGmailCredential(ctx context.Context, cred GmailCredential) (ref string, err error)
}

// WithCredentialSink attaches the sink used by product enablement and returns
// the flow for chaining.
func (f *IdentityFlow) WithCredentialSink(sink CredentialSink) *IdentityFlow {
	f.sink = sink
	return f
}

// gmailEnableScopes are requested when enabling Gmail: identity + read-only mail
// (FR 48). Send access is a separate later upgrade (FR 44), never bundled here.
func gmailEnableScopes() []string {
	return append(append([]string{}, IdentityScopes...), GmailReadonlyScope)
}

// gmailSendScopes add the send scope for the explicit send upgrade (FR 44).
func gmailSendScopes() []string {
	return append(gmailEnableScopes(), GmailSendScope)
}

// BeginEnableGmail starts a Gmail-enable authorization (identity + read-only
// mail) for the already-connected identity.
func (f *IdentityFlow) BeginEnableGmail(p BeginConnectParams) (BeginConnectResult, error) {
	return f.beginGmail(p, gmailEnableScopes())
}

// BeginEnableGmailSend starts the explicit send upgrade (adds gmail.send) for
// the already-connected identity (FR 44). Sends still pass through the existing
// confirm-gated broker.
func (f *IdentityFlow) BeginEnableGmailSend(p BeginConnectParams) (BeginConnectResult, error) {
	return f.beginGmail(p, gmailSendScopes())
}

// beginGmail authorizes the active identity for the given Gmail scope set. It
// uses the known account as a login hint (FR 25); the returned subject is still
// verified against the active identity on completion.
func (f *IdentityFlow) beginGmail(p BeginConnectParams, scopes []string) (BeginConnectResult, error) {
	if !f.config.IsConfigured() {
		return BeginConnectResult{}, ErrOAuthNotConfigured
	}
	if f.sink == nil {
		return BeginConnectResult{}, ErrNoCredentialSink
	}
	existing, err := f.store.Load()
	if err != nil {
		return BeginConnectResult{}, err
	}
	if existing == nil || !existing.HasVerifiedIdentity() {
		return BeginConnectResult{}, ErrNoActiveIdentity
	}

	codeVerifier := oauth2.GenerateVerifier()
	pending, err := f.states.Begin(BeginParams{
		LocalUserID:   p.LocalUserID,
		Product:       ProductGmail,
		ActiveSubject: existing.Subject,
		ReturnTo:      p.ReturnTo,
		CallbackURI:   p.RedirectURL,
		CodeVerifier:  codeVerifier,
	})
	if err != nil {
		return BeginConnectResult{}, err
	}

	opts := []oauth2.AuthCodeOption{
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(codeVerifier),
		oauth2.SetAuthURLParam("nonce", pending.Nonce),
	}
	if existing.Email != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", existing.Email))
	}
	authorizeURL := f.config.oauth2Config(p.RedirectURL, scopes).AuthCodeURL(pending.State, opts...)
	return BeginConnectResult{AuthorizeURL: authorizeURL, State: pending.State}, nil
}

// CompleteEnableGmail consumes the callback: it validates state, exchanges the
// code, verifies the returned id_token matches the active subject (FR 23, 46),
// persists the Gmail credential via the sink, records the exact granted scopes
// (FR 24), and marks the Gmail grant Healthy — or Scope-upgrade-required when
// the user deselected read-only mail on Google's consent screen (FR 26).
func (f *IdentityFlow) CompleteEnableGmail(ctx context.Context, p CompleteConnectParams) (*Connection, error) {
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
	if f.sink == nil {
		return nil, ErrNoCredentialSink
	}

	conn, err := f.store.Load()
	if err != nil {
		return nil, err
	}
	if conn == nil || !conn.HasVerifiedIdentity() {
		return nil, ErrNoActiveIdentity
	}

	cfg := f.config.oauth2Config(pending.CallbackURI, gmailEnableScopes())
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
	if identity.Subject != conn.Subject {
		return nil, ErrDifferentAccountActive
	}

	grantedScopes := scopesFromToken(token)
	existingRef := ""
	if g, ok := conn.Grant(ProductGmail); ok && g != nil {
		existingRef = g.CredentialRef
	}
	ref, err := f.sink.SaveGmailCredential(ctx, GmailCredential{
		Subject:       identity.Subject,
		Email:         identity.Email,
		DisplayName:   identity.Name,
		AccessToken:   token.AccessToken,
		RefreshToken:  token.RefreshToken,
		Expiry:        token.Expiry,
		GrantedScopes: grantedScopes,
		ClientID:      f.config.ClientID,
		ClientSecret:  f.config.ClientSecret,
		TokenEndpoint: f.config.tokenURL(),
		VaultID:       conn.VaultID,
		ExistingRef:   existingRef,
	})
	if err != nil {
		return nil, fmt.Errorf("persist gmail credential: %w", err)
	}

	health := HealthHealthy
	if !slices.Contains(grantedScopes, GmailReadonlyScope) {
		health = HealthScopeUpgradeRequired
	}
	if conn.Grants == nil {
		conn.Grants = map[ProductKey]*ProductGrant{}
	}
	var expiry *time.Time
	if !token.Expiry.IsZero() {
		e := token.Expiry
		expiry = &e
	}
	conn.Grants[ProductGmail] = &ProductGrant{
		ConnectionID:  conn.ID,
		Product:       ProductGmail,
		Transport:     TransportNative,
		CredentialRef: ref,
		GrantedScopes: grantedScopes,
		TokenExpiry:   expiry,
		Health:        health,
	}
	conn.touch()
	if err := f.store.Save(conn); err != nil {
		return nil, err
	}
	return conn, nil
}

// scopesFromToken reads the space-separated "scope" field Google returns in the
// token response — the EXACT granted set, which may be narrower than requested.
func scopesFromToken(token *oauth2.Token) []string {
	raw, _ := token.Extra("scope").(string)
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return nil
	}
	return fields
}
