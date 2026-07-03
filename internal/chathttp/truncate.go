package chathttp

import "strings"

// truncateRunes caps a display string at maxRunes, appending "..." when it
// was cut. Slicing is rune-based so multi-byte characters are never split.
// It is the package's single truncation helper — previously four near-copies
// (truncate, truncateRunes, truncatePlanText, truncateResultPreview) existed,
// two of which byte-sliced and could emit invalid UTF-8.
func truncateRunes(input string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(input)
	if len(runes) <= maxRunes {
		return input
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

// truncateResultPreview collapses all whitespace runs to single spaces and
// then truncates — used for one-line previews of multi-line tool results.
func truncateResultPreview(result string, maxRunes int) string {
	compact := strings.Join(strings.Fields(result), " ")
	return truncateRunes(compact, maxRunes)
}
