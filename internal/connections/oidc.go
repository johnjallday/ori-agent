package connections

import (
	"context"
	"errors"
	"fmt"
	"sync"

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
// this flow, then extracts the identity (FR 21, 22). Expiry is checked strictly
// (go-oidc adds no clock skew), a safe bound.
func (g *GoogleVerifier) Verify(ctx context.Context, rawIDToken, expectedNonce string) (Identity, error) {
	tok, id, err := g.verify(ctx, rawIDToken)
	if err != nil {
		return Identity{}, err
	}
	if expectedNonce == "" || tok.Nonce != expectedNonce {
		return Identity{}, ErrNonceMismatch
	}
	return id, nil
}

// VerifyNoNonce validates issuer, audience, signature, and expiry and extracts
// the identity WITHOUT a nonce check. It is for the MCP OAuth flow, whose
// authorize request carries no nonce and whose replay protection comes from the
// single-use state + PKCE; the ID token is captured server-side from Ori's own
// token exchange (never an attacker-controlled redirect), so subject
// verification is sound without a nonce (FR 23).
func (g *GoogleVerifier) VerifyNoNonce(ctx context.Context, rawIDToken string) (Identity, error) {
	_, id, err := g.verify(ctx, rawIDToken)
	return id, err
}

// verify validates the token and extracts the identity, returning the raw token
// too so callers can apply flow-specific checks (e.g. nonce).
func (g *GoogleVerifier) verify(ctx context.Context, rawIDToken string) (*oidc.IDToken, Identity, error) {
	if g == nil || g.verifier == nil {
		return nil, Identity{}, ErrIDTokenInvalid
	}
	tok, err := g.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, Identity{}, fmt.Errorf("%w: %v", ErrIDTokenInvalid, err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := tok.Claims(&claims); err != nil {
		return nil, Identity{}, fmt.Errorf("%w: claims: %v", ErrIDTokenInvalid, err)
	}
	if tok.Subject == "" {
		return nil, Identity{}, fmt.Errorf("%w: empty subject", ErrIDTokenInvalid)
	}
	return tok, Identity{
		Subject:       tok.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Name:          claims.Name,
		Picture:       claims.Picture,
	}, nil
}

// NewLazyGoogleVerifier returns an IDVerifier that defers OIDC discovery (a
// network call to Google's issuer) until the first Verify, so it never blocks
// or fails server startup. Discovery is retried on each call until it succeeds,
// then cached — a transient startup-time network failure is not sticky.
func NewLazyGoogleVerifier(clientID string) IDVerifier {
	return &lazyVerifier{clientID: clientID}
}

type lazyVerifier struct {
	clientID string
	mu       sync.Mutex
	inner    *GoogleVerifier
}

func (l *lazyVerifier) Verify(ctx context.Context, rawIDToken, expectedNonce string) (Identity, error) {
	l.mu.Lock()
	if l.inner == nil {
		v, err := NewGoogleVerifier(ctx, l.clientID)
		if err != nil {
			l.mu.Unlock()
			return Identity{}, fmt.Errorf("%w: %v", ErrIDTokenInvalid, err)
		}
		l.inner = v
	}
	inner := l.inner
	l.mu.Unlock()
	return inner.Verify(ctx, rawIDToken, expectedNonce)
}
