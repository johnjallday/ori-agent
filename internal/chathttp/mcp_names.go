package chathttp

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func logicalMCPServerName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if _, serverName, _, ok := workspace.ParseRuntimeMCPServerName(trimmed); ok {
		return strings.TrimSpace(serverName)
	}
	return trimmed
}

func normalizeLogicalMCPServerName(name string) string {
	return strings.ToLower(strings.TrimSpace(logicalMCPServerName(name)))
}

func normalizeLogicalMCPServerSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		normalized := normalizeLogicalMCPServerName(value)
		if normalized == "" {
			continue
		}
		set[normalized] = true
	}
	return set
}
