package chathttp

import (
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/skills"
)

func filterToolsForSkill(tools []llm.Tool, skill *skills.Skill) []llm.Tool {
	if skill == nil {
		return tools
	}

	allowlist := normalizeStringSet(skill.AllowedTools)
	denylist := normalizeStringSet(skill.DisallowedTools)

	if len(allowlist) == 0 && len(denylist) == 0 {
		return tools
	}

	filtered := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		name := strings.ToLower(tool.Name)
		if len(allowlist) > 0 && !allowlist[name] {
			continue
		}
		if len(denylist) > 0 && denylist[name] {
			continue
		}
		filtered = append(filtered, tool)
	}

	return filtered
}

func missingMCPServers(enabled []string, required []string) []string {
	enabledSet := normalizeLogicalMCPServerSet(enabled)
	missing := make([]string, 0)
	for _, server := range required {
		if server == "" {
			continue
		}
		if !enabledSet[strings.ToLower(strings.TrimSpace(server))] {
			missing = append(missing, server)
		}
	}
	sort.Strings(missing)
	return missing
}

func normalizeStringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		set[strings.ToLower(trimmed)] = true
	}
	return set
}
