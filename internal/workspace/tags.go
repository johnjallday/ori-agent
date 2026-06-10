package workspace

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// MaxWorkspaceTags is the maximum number of tags a workspace can store.
	MaxWorkspaceTags = 20
	// MaxWorkspaceTagLength is the maximum tag length in Unicode code points.
	MaxWorkspaceTagLength = 64
)

// NormalizeWorkspaceTags returns a canonical, lenient tag list. It is intended
// for metadata sources such as template manifests where bad values should not
// make the whole source unreadable.
func NormalizeWorkspaceTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		value := normalizeWorkspaceTagValue(tag)
		if value == "" || utf8.RuneCountInString(value) > MaxWorkspaceTagLength {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
		if len(normalized) == MaxWorkspaceTags {
			break
		}
	}
	return normalized
}

// ValidateWorkspaceTags canonicalizes user-submitted tags and rejects values
// that would exceed workspace tag limits.
func ValidateWorkspaceTags(tags []string) ([]string, error) {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		value := normalizeWorkspaceTagValue(tag)
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > MaxWorkspaceTagLength {
			return nil, fmt.Errorf("tag %q exceeds the %d character limit", value, MaxWorkspaceTagLength)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) > MaxWorkspaceTags {
		return nil, fmt.Errorf("workspaces can have at most %d tags", MaxWorkspaceTags)
	}
	if normalized == nil {
		return []string{}, nil
	}
	return normalized, nil
}

// MergeWorkspaceTags merges existing workspace tags with additional metadata
// tags using lenient normalization and the workspace tag cap.
func MergeWorkspaceTags(existing, additional []string) []string {
	combined := make([]string, 0, len(existing)+len(additional))
	combined = append(combined, existing...)
	combined = append(combined, additional...)
	return NormalizeWorkspaceTags(combined)
}

func normalizeWorkspaceTagValue(tag string) string {
	return strings.ToLower(strings.TrimSpace(tag))
}
