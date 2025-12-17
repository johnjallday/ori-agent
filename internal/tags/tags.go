package tags

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var normalizedTagPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// NormalizeTag converts a user-provided tag into a normalized format:
// lowercase, words separated by single hyphens, and no leading/trailing separators.
//
// Examples:
//
//	"dev_tools" -> "dev-tools"
//	"DevTools"  -> "dev-tools"
//	"  audio  " -> "audio"
func NormalizeTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}

	var out []rune
	var prevOut rune
	var hasPrevOut bool
	var prevWasHyphen bool

	for _, r := range tag {
		if r == '_' || unicode.IsSpace(r) || r == '-' {
			if !prevWasHyphen && hasPrevOut {
				out = append(out, '-')
				prevOut = '-'
				hasPrevOut = true
				prevWasHyphen = true
			}
			continue
		}

		if unicode.IsUpper(r) {
			needsHyphen := hasPrevOut && !prevWasHyphen && (unicode.IsLower(prevOut) || unicode.IsDigit(prevOut))
			if needsHyphen {
				out = append(out, '-')
			}
			r = unicode.ToLower(r)
		} else {
			r = unicode.ToLower(r)
		}

		out = append(out, r)
		prevOut = r
		hasPrevOut = true
		prevWasHyphen = r == '-'
	}

	normalized := strings.Trim(strings.TrimSpace(string(out)), "-")
	normalized = strings.Trim(normalized, "-")
	for strings.Contains(normalized, "--") {
		normalized = strings.ReplaceAll(normalized, "--", "-")
	}
	return normalized
}

// ValidateTag validates a normalized tag.
// Constraints: min 2 chars, lowercase alphanumeric with single hyphens separating segments.
func ValidateTag(tag string) error {
	tag = strings.TrimSpace(tag)
	if len(tag) < 2 {
		return fmt.Errorf("tag must be at least 2 characters: %q", tag)
	}
	if !normalizedTagPattern.MatchString(tag) {
		return fmt.Errorf("tag must match %s: %q", normalizedTagPattern.String(), tag)
	}
	return nil
}

// ValidateTags normalizes and validates all tags, returning the valid normalized tags and any errors.
func ValidateTags(tags []string) ([]string, []error) {
	valid := make([]string, 0, len(tags))
	var errs []error

	for _, raw := range tags {
		normalized := NormalizeTag(raw)
		if normalized == "" {
			errs = append(errs, fmt.Errorf("tag is empty after normalization: %q", raw))
			continue
		}
		if err := ValidateTag(normalized); err != nil {
			errs = append(errs, err)
			continue
		}
		valid = append(valid, normalized)
	}

	return valid, errs
}

// NormalizeTags normalizes, validates, de-duplicates (preserving order), and enforces a max of 5 tags.
// Invalid tags are dropped.
func NormalizeTags(tags []string) []string {
	valid, _ := ValidateTags(tags)

	seen := make(map[string]struct{}, len(valid))
	out := make([]string, 0, len(valid))
	for _, tag := range valid {
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
		if len(out) >= 5 {
			break
		}
	}
	return out
}
