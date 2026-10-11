package assistantdiscovery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/publicread"
	"github.com/johnjallday/ori-agent/internal/skills"
)

// ValidatePublicQuery is only validation, not authorization. Every outbound
// lookup still needs an exact host-owned review; this does not guess consent
// from a user prompt or permit private-context-derived egress.
func ValidatePublicQuery(query string) error {
	if err := skills.ValidateMarketplaceQuery(query); err != nil {
		return err
	}
	if secretLike(query) {
		return errors.New("secret-like query is not permitted")
	}
	return nil
}

// PublicURL validates safe source-link syntax. This alone never authorizes a
// read; publicread's strict transport also checks DNS/IPs at execution time.
func PublicURL(raw string) (string, error) { return publicread.ValidateURL(raw) }

var privatePath = regexp.MustCompile(`(^|[\s"'(])(/[^\s"'<>]+|[A-Za-z]:\\[^\s"'<>]+)`)

func secretLike(text string) bool {
	return publicread.ContainsCredentialMaterial(text)
}

func referenceText(raw string, limit int) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", true
	}
	if secretLike(raw) {
		return "[withheld: secret-like text]", true
	}
	raw = privatePath.ReplaceAllString(raw, "$1[private path]")
	var out strings.Builder
	count, truncated := 0, false
	for _, ch := range raw {
		if unicode.Is(unicode.Cf, ch) || unicode.IsControl(ch) && ch != '\n' && ch != '\t' {
			truncated = true
			continue
		}
		if count == limit {
			truncated = true
			break
		}
		out.WriteRune(ch)
		count++
	}
	return out.String(), truncated
}

func contentHash(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

func identity(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:16])
}
