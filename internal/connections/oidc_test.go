package connections

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func testVerifier(t *testing.T, priv *rsa.PrivateKey, clientID string) *GoogleVerifier {
	t.Helper()
	ks := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{priv.Public()}}
	return &GoogleVerifier{verifier: oidc.NewVerifier(googleIssuer, ks, &oidc.Config{ClientID: clientID})}
}

func mintToken(t *testing.T, priv *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: priv}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	raw, err := jwt.Signed(sig).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return raw
}

func baseClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss":            googleIssuer,
		"sub":            "sub-123",
		"aud":            "aud-123",
		"iat":            now.Unix(),
		"exp":            now.Add(time.Hour).Unix(),
		"nonce":          "nonce-abc",
		"email":          "jane@example.com",
		"email_verified": true,
		"name":           "Jane",
		"picture":        "https://p/9",
	}
}

func TestGoogleVerifier_Valid(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	gv := testVerifier(t, priv, "aud-123")
	id, err := gv.Verify(context.Background(), mintToken(t, priv, baseClaims(time.Now())), "nonce-abc")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Subject != "sub-123" || id.Email != "jane@example.com" || !id.EmailVerified || id.Name != "Jane" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestGoogleVerifier_NonceMismatch(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	gv := testVerifier(t, priv, "aud-123")
	_, err := gv.Verify(context.Background(), mintToken(t, priv, baseClaims(time.Now())), "wrong-nonce")
	if !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("want ErrNonceMismatch, got %v", err)
	}
}

func TestGoogleVerifier_Expired(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	gv := testVerifier(t, priv, "aud-123")
	_, err := gv.Verify(context.Background(), mintToken(t, priv, baseClaims(time.Now().Add(-2*time.Hour))), "nonce-abc")
	if !errors.Is(err, ErrIDTokenInvalid) {
		t.Fatalf("want ErrIDTokenInvalid (expired), got %v", err)
	}
}

func TestGoogleVerifier_WrongAudience(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	gv := testVerifier(t, priv, "aud-123")
	cl := baseClaims(time.Now())
	cl["aud"] = "someone-else"
	_, err := gv.Verify(context.Background(), mintToken(t, priv, cl), "nonce-abc")
	if !errors.Is(err, ErrIDTokenInvalid) {
		t.Fatalf("want ErrIDTokenInvalid (aud), got %v", err)
	}
}

func TestGoogleVerifier_WrongSigningKey(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	gv := testVerifier(t, priv, "aud-123") // trusts priv's public key
	_, err := gv.Verify(context.Background(), mintToken(t, other, baseClaims(time.Now())), "nonce-abc")
	if !errors.Is(err, ErrIDTokenInvalid) {
		t.Fatalf("want ErrIDTokenInvalid (signature), got %v", err)
	}
}
