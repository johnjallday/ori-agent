package workspace

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	// WorkspaceConfigFile is the filename for workspace metadata inside a workspace folder.
	WorkspaceConfigFile = "workspace.json"

	// FilesDir is the subdirectory for workspace-specific file storage.
	FilesDir = "files"

	// NotesDir is the subdirectory for workspace notes stored as markdown files.
	NotesDir = "notes"

	// OutputsDir is the subdirectory for auto-saved task results when no store
	// node or explicit file path is configured.
	OutputsDir = "outputs"

	// WorkspaceAgentsDir is the subdirectory for workspace-local agent snapshots.
	// Each agent lives at <workspace>/agents/<agent-name>/config.json.
	WorkspaceAgentsDir = "agents"

	// WorkspaceAgentConfigFile is the per-agent snapshot filename inside a
	// workspace's agents directory.
	WorkspaceAgentConfigFile = "config.json"

	// SubWorkspacesDir is the subdirectory for nested child workspaces.
	SubWorkspacesDir = "sub-workspaces"

	// MaxSlugLength is the maximum length of a slugified workspace folder name.
	MaxSlugLength = 64

	// MaxNestingDepth is the maximum allowed sub-workspace nesting depth.
	MaxNestingDepth = 5
)

var (
	// nonAlphanumHyphen matches anything that isn't a letter, digit, or hyphen.
	nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9-]+`)
	// multipleHyphens collapses runs of hyphens.
	multipleHyphens = regexp.MustCompile(`-{2,}`)
)

// Slugify converts a workspace name into a filesystem-safe folder name.
// It lowercases the input, normalises unicode to ASCII-compatible forms,
// replaces non-alphanumeric characters with hyphens, collapses repeated
// hyphens, trims leading/trailing hyphens, and truncates to MaxSlugLength.
// Returns "untitled" for empty or whitespace-only input.
func Slugify(name string) string {
	// Trim whitespace
	s := strings.TrimSpace(name)
	if s == "" {
		return "untitled"
	}

	// Normalise unicode (NFD) so accented chars decompose, then strip
	// non-ASCII marks (accents) to get a rough transliteration.
	s = stripAccents(s)

	// Lowercase
	s = strings.ToLower(s)

	// Replace non-alphanumeric characters with hyphens
	s = nonAlphanumHyphen.ReplaceAllString(s, "-")

	// Collapse multiple hyphens
	s = multipleHyphens.ReplaceAllString(s, "-")

	// Trim leading/trailing hyphens
	s = strings.Trim(s, "-")

	// Truncate to max length, but don't cut in the middle of a word if possible
	if len(s) > MaxSlugLength {
		s = s[:MaxSlugLength]
		// Trim trailing hyphen from truncation
		s = strings.TrimRight(s, "-")
	}

	if s == "" {
		return "untitled"
	}

	return s
}

// IsCanonicalWorkspaceSlug reports whether slug is already in the exact form
// Slugify emits. Browser routes use this to reject empty, malformed, or
// non-canonical tokens instead of silently rewriting them or treating them as
// workspace IDs.
func IsCanonicalWorkspaceSlug(slug string) bool {
	trimmed := strings.TrimSpace(slug)
	return trimmed != "" && trimmed == slug && Slugify(trimmed) == trimmed
}

// WorkspaceSlugWithSuffix appends a numeric conflict suffix without exceeding
// MaxSlugLength.
func WorkspaceSlugWithSuffix(baseSlug string, suffix int) string {
	suffixText := fmt.Sprintf("-%d", suffix)
	maxBaseLen := MaxSlugLength - len(suffixText)
	if maxBaseLen < 1 {
		maxBaseLen = 1
	}

	trimmedBase := strings.Trim(strings.TrimSpace(baseSlug), "-")
	if len(trimmedBase) > maxBaseLen {
		trimmedBase = strings.TrimRight(trimmedBase[:maxBaseLen], "-")
	}
	if trimmedBase == "" {
		trimmedBase = "untitled"
		if len(trimmedBase) > maxBaseLen {
			trimmedBase = strings.TrimRight(trimmedBase[:maxBaseLen], "-")
		}
		if trimmedBase == "" {
			trimmedBase = "w"
		}
	}
	return trimmedBase + suffixText
}

// stripAccents removes combining unicode marks (accents) from the string,
// converting e.g. "café" → "cafe".
func stripAccents(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue // skip combining marks
		}
		b.WriteRune(r)
	}
	return b.String()
}
