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

// WithVaultCatalog attaches the vault catalog used to preflight (and, at
// callback time, re-verify) the credential vault. Without it product enablement
// cannot start — resolving the destination vault up front is what keeps a
// locked or ambiguous vault from failing only AFTER the user authorized at
// Google (FR 1, 3-9, 12).
func (f *IdentityFlow) WithVaultCatalog(catalog VaultCatalog) *IdentityFlow {
	f.vaults = catalog
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
func (f *IdentityFlow) BeginEnableGmail(ctx context.Context, p BeginConnectParams) (BeginConnectResult, error) {
	return f.beginGmail(ctx, p, gmailEnableScopes())
}

// BeginEnableGmailSend starts the explicit send upgrade (adds gmail.send) for
// the already-connected identity (FR 44). Sends still pass through the existing
// confirm-gated broker.
func (f *IdentityFlow) BeginEnableGmailSend(ctx context.Context, p BeginConnectParams) (BeginConnectResult, error) {
	return f.beginGmail(ctx, p, gmailSendScopes())
}

// beginGmail authorizes the active identity for the given Gmail scope set. It
// uses the known account as a login hint (FR 25); the returned subject is still
// verified against the active identity on completion.
//
// Before any of that it resolves and RECORDS the destination vault, so the
// browser only leaves for Google once Ori knows exactly where the resulting
// credential will land and that the vault can accept it (FR 1, 3-9, 12).
func (f *IdentityFlow) beginGmail(ctx context.Context, p BeginConnectParams, scopes []string) (BeginConnectResult, error) {
	if !f.config.IsConfigured() {
		return BeginConnectResult{}, ErrOAuthNotConfigured
	}
	if err := f.config.checkClient(); err != nil {
		return BeginConnectResult{}, err
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
	if err := f.recordProductVault(ctx, existing, p.VaultID); err != nil {
		return BeginConnectResult{}, err
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
	return BeginConnectResult{AuthorizeURL: authorizeURL, State: pending.State, CorrelationID: pending.CorrelationID}, nil
}

// recordProductVault resolves the vault that will store the product credential
// and persists it on the connection BEFORE the browser leaves Ori (FR 7). Any
// outcome other than "ready" is returned as a *VaultPreflightError so the caller
// can render the exact create/choose/unlock/repair action and resume the pending
// enable afterward (FR 5-11) — critically, without ever opening Google.
//
// explicitVaultID is the user's answer to a previous prompt; it overrides (and
// replaces) whatever the connection remembered, which is what makes the repair
// flow able to move off a missing vault.
func (f *IdentityFlow) recordProductVault(ctx context.Context, conn *Connection, explicitVaultID string) error {
	if f.vaults == nil {
		// No catalog wired: keep the pre-preflight behavior rather than blocking a
		// build that has no vault store at all.
		return nil
	}
	saved := strings.TrimSpace(conn.VaultID)
	if explicit := strings.TrimSpace(explicitVaultID); explicit != "" {
		saved = explicit
	}
	preflight, err := PreflightVault(ctx, f.vaults, saved)
	if err != nil {
		return err
	}
	if preflight.Outcome != VaultOutcomeReady {
		return &VaultPreflightError{Preflight: preflight}
	}
	if conn.VaultID == preflight.VaultID {
		return nil
	}
	conn.VaultID = preflight.VaultID
	conn.touch()
	return f.store.Save(conn)
}

// CompleteEnableGmail consumes the callback: it validates state, exchanges the
// code, verifies the returned id_token matches the active subject (FR 23, 46),
// persists the Gmail credential via the sink, records the exact granted scopes
// (FR 24), and marks the Gmail grant Healthy — or Scope-upgrade-required when
// the user deselected read-only mail on Google's consent screen (FR 26).
// Every failure is a typed *CallbackError naming its stage and category, so the
// result page can distinguish "Google refused" from "your vault is locked"
// (FR 13-16). The vault recorded at begin time is re-verified before the token
// exchange: if it is locked or gone, the flow stops with an actionable error
// rather than obtaining tokens it cannot store (FR 12).
func (f *IdentityFlow) CompleteEnableGmail(ctx context.Context, p CompleteConnectParams) (*Connection, error) {
	pending, ok := f.states.Consume(p.State)
	if !ok {
		return nil, callbackErr(StageAuthorization, CategoryExpiredState, false, ErrExpiredFlow)
	}
	fail := func(stage CallbackStage, category CallbackCategory, signedIn bool, cause error) error {
		e := callbackErr(stage, category, signedIn, cause)
		e.CorrelationID = pending.CorrelationID
		return e
	}
	if strings.TrimSpace(p.OAuthError) != "" {
		return nil, fail(StageAuthorization, CategoryDenied, false, fmt.Errorf("%w: %s", ErrAuthorizationDenied, p.OAuthError))
	}
	if strings.TrimSpace(p.Code) == "" {
		return nil, fail(StageAuthorization, CategoryDenied, false, ErrAuthorizationDenied)
	}
	if f.sink == nil {
		return nil, fail(StagePersist, CategoryVaultUnavailable, true, ErrNoCredentialSink)
	}

	conn, err := f.store.Load()
	if err != nil {
		return nil, fail(StagePersist, CategoryConnectionPersistFailed, true, err)
	}
	if conn == nil || !conn.HasVerifiedIdentity() {
		return nil, fail(StageAuthorization, CategoryNoIdentity, false, ErrNoActiveIdentity)
	}

	// FR 12: save only to the vault recorded on the connection — never guess.
	vaultID, vaultErr := f.verifyRecordedVault(ctx, conn)
	if vaultErr != nil {
		var typed *CallbackError
		if errors.As(vaultErr, &typed) {
			typed.CorrelationID = pending.CorrelationID
		}
		return nil, vaultErr
	}

	cfg := f.config.oauth2Config(pending.CallbackURI, gmailEnableScopes())
	token, err := cfg.Exchange(ctx, p.Code, oauth2.VerifierOption(pending.CodeVerifier))
	if err != nil {
		return nil, fail(StageTokenExchange, CategoryTokenExchangeFailed, false, fmt.Errorf("token exchange failed: %w", err))
	}
	rawID, _ := token.Extra("id_token").(string)
	if strings.TrimSpace(rawID) == "" {
		return nil, fail(StageIdentity, CategoryIdentityUnverified, false, ErrNoIDToken)
	}
	identity, err := f.verifier.Verify(ctx, rawID, pending.Nonce)
	if err != nil {
		return nil, fail(StageIdentity, CategoryIdentityUnverified, false, err)
	}
	if identity.Subject != conn.Subject {
		return nil, fail(StageIdentity, CategoryAccountMismatch, true, ErrDifferentAccountActive)
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
		VaultID:       vaultID,
		ExistingRef:   existingRef,
	})
	if err != nil {
		// A write that failed because the vault locked between preflight and here
		// is still an unlock-and-retry, not a generic persistence fault.
		return nil, fail(StagePersist, persistFailureCategory(err), true, fmt.Errorf("persist gmail credential: %w", err))
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
		return nil, fail(StagePersist, CategoryConnectionPersistFailed, true, err)
	}
	return conn, nil
}

// verifyRecordedVault re-checks, at callback time, the vault chosen during
// preflight. The two checks are not redundant: a password-protected vault can
// lock, and a file-backed vault can disappear, while the user is away at Google.
// Returns the vault id to write to, or a typed vault_locked /
// vault_selection_required failure (FR 12-14).
func (f *IdentityFlow) verifyRecordedVault(ctx context.Context, conn *Connection) (string, error) {
	vaultID := strings.TrimSpace(conn.VaultID)
	if f.vaults == nil {
		return vaultID, nil
	}
	if vaultID == "" {
		return "", callbackErr(StageVault, CategoryVaultSelectionRequired, true, errors.New("no vault recorded on the connection"))
	}
	availability, err := f.vaults.VaultAvailability(ctx, vaultID)
	if err != nil {
		return "", callbackErr(StageVault, CategoryVaultUnavailable, true, err)
	}
	switch availability {
	case VaultLocked:
		e := callbackErr(StageVault, CategoryVaultLocked, true, errors.New("recorded vault is locked"))
		e.VaultID = vaultID
		return "", e
	case VaultMissing:
		e := callbackErr(StageVault, CategoryVaultSelectionRequired, true, errors.New("recorded vault is unavailable"))
		e.VaultID = vaultID
		return "", e
	default:
		return vaultID, nil
	}
}

// persistFailureCategory narrows a credential-write failure. A vault that locked
// mid-flow must still surface as unlock-and-retry rather than a dead end.
func persistFailureCategory(err error) CallbackCategory {
	switch {
	case errors.Is(err, ErrVaultLockedWrite):
		return CategoryVaultLocked
	case errors.Is(err, ErrVaultMissingWrite):
		return CategoryVaultSelectionRequired
	default:
		return CategoryCredentialPersistFailed
	}
}

// Sentinels a CredentialSink can wrap so a write failure keeps its precise
// meaning across the interface boundary without leaking vault types here.
var (
	// ErrVaultLockedWrite: the destination vault was locked at write time.
	ErrVaultLockedWrite = errors.New("connections: destination vault is locked")
	// ErrVaultMissingWrite: the destination vault no longer exists at write time.
	ErrVaultMissingWrite = errors.New("connections: destination vault is unavailable")
)

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
