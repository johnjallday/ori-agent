package workspace

import (
	"fmt"
	"net/url"
	"strings"
)

const ReferenceURLMaxLength = 2048

// NormalizeReferenceURL trims and validates an optional task/run reference URL.
func NormalizeReferenceURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > ReferenceURLMaxLength {
		return "", fmt.Errorf("reference_url must be %d characters or fewer", ReferenceURLMaxLength)
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("reference_url must be a valid URL")
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("reference_url must use http or https")
	}
	if !parsed.IsAbs() || strings.TrimSpace(parsed.Host) == "" || strings.TrimSpace(parsed.Hostname()) == "" {
		return "", fmt.Errorf("reference_url must be an absolute URL")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("reference_url must not include credentials")
	}
	if strings.ContainsAny(parsed.Host, " \t\r\n") {
		return "", fmt.Errorf("reference_url host must not contain whitespace")
	}

	parsed.Scheme = scheme
	return parsed.String(), nil
}

// ReferenceURLAllowlistHost returns the exact URL authority used for scoped
// network allowlisting. The URL must already pass NormalizeReferenceURL.
func ReferenceURLAllowlistHost(value string) (string, error) {
	normalized, err := NormalizeReferenceURL(value)
	if err != nil || normalized == "" {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("reference_url must be a valid URL")
	}
	return parsed.Host, nil
}
