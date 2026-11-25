package chathttp

import "strings"

// containsAnyPhrase reports whether text contains any non-blank phrase.
func containsAnyPhrase(text string, phrases []string) bool {
	for _, phrase := range phrases {
		candidate := strings.TrimSpace(phrase)
		if candidate == "" {
			continue
		}
		if strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

// hasAnyMCPServer reports whether any enabled server is one of the candidates,
// compared by logical MCP server name.
func hasAnyMCPServer(enabledServers, candidates []string) bool {
	candidateSet := normalizeLogicalMCPServerSet(candidates)
	for _, enabled := range enabledServers {
		normalizedEnabled := normalizeLogicalMCPServerName(enabled)
		if candidateSet[normalizedEnabled] {
			return true
		}
	}
	return false
}
