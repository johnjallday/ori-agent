package trigger

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// tokenBytes is the entropy of a webhook token. 32 bytes ≈ 256 bits, encoded
// to 43 URL-safe characters.
const tokenBytes = 32

// GenerateToken returns a new webhook URL token: crypto-random, URL-safe,
// with no structural prefix that could leak what it identifies.
func GenerateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate webhook token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// SecureCompare reports whether two secrets are equal without leaking where
// they diverge. Inputs are hashed first so the comparison is constant-time
// even for different lengths.
func SecureCompare(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
