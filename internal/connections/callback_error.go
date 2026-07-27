package connections

import (
	"errors"
	"fmt"
)

// Typed OAuth-callback failures. The old flow collapsed every callback problem
// into one message, so "your vault is locked" and "Google rejected the sign-in"
// were indistinguishable to the user. Each failure now carries the STAGE it
// happened at, a stable CATEGORY, and — decisively for the result page —
// whether the Google sign-in itself had already succeeded (FR 15, 16).
//
// A CallbackError never carries an authorization code, token, or provider
// payload. `cause` is kept only for server-side wrapping and is never rendered.

// CallbackStage is where in the callback the failure occurred.
type CallbackStage string

const (
	// StageAuthorization covers everything up to and including Google's redirect:
	// cancellation, an expired/replayed state, a provider-reported error.
	StageAuthorization CallbackStage = "authorization"
	// StageTokenExchange is exchanging the authorization code for tokens.
	StageTokenExchange CallbackStage = "token_exchange"
	// StageIdentity is verifying the returned ID token and matching the subject.
	StageIdentity CallbackStage = "identity_verification"
	// StageVault is resolving/validating the vault that must receive the credential.
	StageVault CallbackStage = "vault"
	// StagePersist is writing the credential or the connection record locally.
	StagePersist CallbackStage = "persist"
)

// CallbackCategory is the stable machine code for a callback failure. Clients
// switch on it to choose the repair action; it is safe to log.
type CallbackCategory string

const (
	// CategoryDenied: the user canceled, or Google returned error=...
	CategoryDenied CallbackCategory = "authorization_denied"
	// CategoryExpiredState: unknown, already-used, or expired state value.
	CategoryExpiredState CallbackCategory = "expired_state"
	// CategoryAccountMismatch: a different Google account came back than the one
	// currently connected.
	CategoryAccountMismatch CallbackCategory = "account_mismatch"
	// CategoryTokenExchangeFailed: Google refused the code-for-token exchange.
	CategoryTokenExchangeFailed CallbackCategory = "token_exchange_failed"
	// CategoryIdentityUnverified: no id_token, bad nonce, or invalid signature.
	CategoryIdentityUnverified CallbackCategory = "identity_unverified"
	// CategoryVaultLocked: the recorded vault is password-protected and locked
	// right now; unlocking and retrying will work (FR 13).
	CategoryVaultLocked CallbackCategory = "vault_locked"
	// CategoryVaultSelectionRequired: no vault is recorded, or the recorded one is
	// gone; the user must choose or create one (FR 14).
	CategoryVaultSelectionRequired CallbackCategory = "vault_selection_required"
	// CategoryVaultUnavailable: the vault store itself could not be consulted.
	CategoryVaultUnavailable CallbackCategory = "vault_unavailable"
	// CategoryCredentialPersistFailed: the credential could not be written.
	CategoryCredentialPersistFailed CallbackCategory = "credential_persist_failed"
	// CategoryConnectionPersistFailed: the credential was stored but the grant
	// could not be recorded on the connection.
	CategoryConnectionPersistFailed CallbackCategory = "connection_persist_failed"
	// CategoryNotConfigured: this build has no usable Google OAuth client.
	CategoryNotConfigured CallbackCategory = "not_configured"
	// CategoryNoIdentity: a product callback arrived with no connected identity.
	CategoryNoIdentity CallbackCategory = "no_identity"
	// CategoryUnknown: anything unclassified. Treated as a generic local failure.
	CategoryUnknown CallbackCategory = "unknown"
)

// CallbackError is a typed, token-free callback failure.
type CallbackError struct {
	// Stage is where the failure happened.
	Stage CallbackStage
	// Category is the stable machine code.
	Category CallbackCategory
	// SignedIn reports whether the Google sign-in itself had already succeeded
	// when this failed. It is what lets the result page say "you're signed in;
	// the problem is on this machine" instead of blaming Google (FR 16).
	SignedIn bool
	// VaultID is the vault an unlock/repair action applies to, when known.
	VaultID string
	// CorrelationID ties this failure to the begin-authorization log line (FR 20).
	CorrelationID string

	cause error
}

func (e *CallbackError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("connections: %s failed (%s): %v", e.Stage, e.Category, e.cause)
	}
	return fmt.Sprintf("connections: %s failed (%s)", e.Stage, e.Category)
}

// Unwrap exposes the underlying cause for server-side inspection only.
func (e *CallbackError) Unwrap() error { return e.cause }

// callbackErr builds a typed failure, wrapping cause for server logs.
func callbackErr(stage CallbackStage, category CallbackCategory, signedIn bool, cause error) *CallbackError {
	return &CallbackError{Stage: stage, Category: category, SignedIn: signedIn, cause: cause}
}

// ClassifyCallback maps any error returned by Complete/CompleteConnect/
// CompleteEnableGmail onto a typed CallbackError. Errors that are already typed
// pass through unchanged; the sentinel errors from the identity path are mapped
// here so a single result-page renderer serves every flow (FR 15).
func ClassifyCallback(err error) *CallbackError {
	if err == nil {
		return nil
	}
	var typed *CallbackError
	if errors.As(err, &typed) {
		return typed
	}
	switch {
	case errors.Is(err, ErrExpiredFlow):
		return callbackErr(StageAuthorization, CategoryExpiredState, false, err)
	case errors.Is(err, ErrAuthorizationDenied):
		return callbackErr(StageAuthorization, CategoryDenied, false, err)
	case errors.Is(err, ErrDifferentAccountActive):
		// The user did sign in successfully — with the wrong account.
		return callbackErr(StageIdentity, CategoryAccountMismatch, true, err)
	case errors.Is(err, ErrNonceMismatch), errors.Is(err, ErrIDTokenInvalid), errors.Is(err, ErrNoIDToken):
		return callbackErr(StageIdentity, CategoryIdentityUnverified, false, err)
	case errors.Is(err, ErrOAuthNotConfigured):
		return callbackErr(StageAuthorization, CategoryNotConfigured, false, err)
	case errors.Is(err, ErrNoActiveIdentity):
		return callbackErr(StageAuthorization, CategoryNoIdentity, false, err)
	default:
		return callbackErr(StagePersist, CategoryUnknown, true, err)
	}
}
