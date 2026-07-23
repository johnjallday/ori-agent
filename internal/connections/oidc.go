package connections

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// googleIssuer is Google's stable OpenID Connect issuer. The verifier discovers
// Google's JWKS from it rather than pinning keys, so signing-key rotation is
// handled by the library.
const googleIssuer = "https://accounts.google.com"

var (
	// ErrNonceMismatch means the ID token's nonce did not match the one Ori bound
	// to this flow (a replay/CSRF signal).
	ErrNonceMismatch = errors.New("connections: id token nonce mismatch")
	// ErrIDTokenInvalid wraps any failure to verify the ID token (issuer,
	// audience, signature, expiry) or extract a usable identity from it.
	ErrIDTokenInvalid = errors.New("connections: id token verification failed")
)

// Identity is the validated identity extracted from a Google ID token. Subject
// is the stable primary key (FR 4); email/name/picture are display metadata and
// must never be treated as the identity key.
type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
}

// idTokenVerifier is the slice of *oidc.IDTokenVerifier this package depends on,
// extracted as an interface so tests can inject a static-keyset verifier and
// exercise the full path offline (no network to Google's JWKS).
type idTokenVerifier interface {
	Verify(ctx context.Context, rawIDToken string) (*oidc.IDToken, error)
}

// GoogleVerifier validates Google-issued ID tokens via go-oidc: issuer,
// audience, signature, and expiry against Google's published JWKS, plus the
// nonce (FR 21, 22). It never performs custom signature verification.
type GoogleVerifier struct {
	verifier idTokenVerifier
}

// NewGoogleVerifier builds a verifier bound to the given OAuth client ID
// (the expected audience). It performs OIDC discovery against Google's issuer,
// which requires network access.
func NewGoogleVerifier(ctx context.Context, clientID string) (*GoogleVerifier, error) {
	if clientID == "" {
		return nil, fmt.Errorf("connections: verifier requires a client id (audience)")
	}
	provider, err := oidc.NewProvider(ctx, googleIssuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	return &GoogleVerifier{verifier: provider.Verifier(&oidc.Config{ClientID: clientID})}, nil
}

// Verify validates the raw ID token and that its nonce matches the one bound to
// this flow, then extracts the identity. Expiry is checked strictly (go-oidc
// adds no clock skew); being strict on expiry is a safe bound for FR 22.
func (g *GoogleVerifier) Verify(ctx context.Context, rawIDToken, expectedNonce string) (Identity, error) {
	if g == nil || g.verifier == nil {
		return Identity{}, ErrIDTokenInvalid
	}
	tok, err := g.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrIDTokenInvalid, err)
	}
	if expectedNonce == "" || tok.Nonce != expectedNonce {
		return Identity{}, ErrNonceMismatch
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := tok.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("%w: claims: %v", ErrIDTokenInvalid, err)
	}
	if tok.Subject == "" {
		return Identity{}, fmt.Errorf("%w: empty subject", ErrIDTokenInvalid)
	}
	return Identity{
		Subject:       tok.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Name:          claims.Name,
		Picture:       claims.Picture,
	}, nil
}
