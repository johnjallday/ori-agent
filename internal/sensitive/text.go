package sensitive

import (
	"errors"
	"regexp"
)

var ErrSecretLikeText = errors.New("this text looks like a credential or secret; prompt-injected plaintext stores refuse secrets — store it in the Vault instead")

// secretPatterns are obvious credential shapes that prompt-injected plaintext
// stores refuse. The list is deliberately conservative to avoid pretending
// plaintext profile or memory fields are safe secret storage.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`),            // OpenAI/Anthropic-style keys
	regexp.MustCompile(`\bgh[poasur]_[A-Za-z0-9]{8,}`),      // GitHub tokens (ghp_, gho_, ...)
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{8,}`),     // GitHub fine-grained PAT
	regexp.MustCompile(`\bAKIA[0-9A-Z]{8,}`),                // AWS access key ID
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{8,}`),     // Slack tokens
	regexp.MustCompile(`-----BEGIN[A-Z ]*PRIVATE KEY-----`), // PEM private keys
}

func ContainsSecretLikeText(text string) bool {
	for _, re := range secretPatterns {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

func RejectSecretLikeText(text string) error {
	if ContainsSecretLikeText(text) {
		return ErrSecretLikeText
	}
	return nil
}
