package chathttp

import (
	"strings"
)

// inferOpenAppCommandFromChat converts simple natural-language app launch prompts
// into an internal /openapp command.
//
// Examples:
//   - "open safari" -> "/openapp safari"
//   - "launch obsidian app" -> "/openapp obsidian"
//
// The parser is intentionally conservative to avoid accidental launches from
// broader requests (URLs, paths, folder/file intents, multi-step prompts).
func inferOpenAppCommandFromChat(input string) (string, bool) {
	original := strings.TrimSpace(input)
	if original == "" {
		return "", false
	}
	if strings.HasPrefix(original, "/") {
		return "", false
	}

	normalized := strings.ToLower(original)

	// Strip a single polite prefix.
	for _, prefix := range []string{"please ", "can you ", "could you ", "would you ", "hey "} {
		if strings.HasPrefix(normalized, prefix) {
			normalized = strings.TrimSpace(strings.TrimPrefix(normalized, prefix))
			break
		}
	}

	target := ""
	for _, prefix := range []string{"open up ", "open ", "launch ", "start ", "run "} {
		if strings.HasPrefix(normalized, prefix) {
			target = strings.TrimSpace(strings.TrimPrefix(normalized, prefix))
			break
		}
	}
	if target == "" {
		return "", false
	}

	target = strings.Trim(target, " \t\n\r\"'`.,!?;:")
	target = strings.TrimPrefix(target, "the ")
	target = strings.TrimSuffix(target, " app")
	target = strings.TrimSuffix(target, " application")
	target = strings.TrimSuffix(target, " browser")
	target = strings.TrimSuffix(target, " please")
	target = strings.TrimSuffix(target, " now")
	target = strings.TrimSuffix(target, " right now")
	target = strings.TrimSuffix(target, " for me")
	target = strings.TrimSuffix(target, " thanks")
	target = strings.TrimSuffix(target, " thank you")
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}

	// Avoid launching for paths/URLs and multi-step prompts.
	if strings.Contains(target, "://") ||
		strings.Contains(target, "/") ||
		strings.Contains(target, "\\") ||
		strings.Contains(target, " and ") {
		return "", false
	}

	// Avoid obvious non-app targets.
	for _, blocked := range []string{
		".com", ".org", ".net", ".io", ".dev", "www.",
		"website", "url", "link", "tab", "folder", "directory", "file", "email",
	} {
		if strings.Contains(target, blocked) {
			return "", false
		}
	}

	if len(strings.Fields(target)) > 5 {
		return "", false
	}

	return "/openapp " + target, true
}
