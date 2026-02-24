package chathttp

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
)

func isWebUtilityToolName(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "web_search", "web_fetch", "browser":
		return true
	default:
		return false
	}
}

func isUtilityToolAllowedForAgent(ag *agent.Agent, toolName string) bool {
	if !isWebUtilityToolName(toolName) {
		return true
	}
	if ag == nil {
		return true
	}
	return ag.Settings.IsWebSearchAllowed()
}

func disallowedUtilityToolMessage(toolName string) string {
	if isWebUtilityToolName(toolName) {
		return "Web tools are disabled for this agent. Enable web search in the agent settings to use web search, web fetch, or browser actions."
	}
	return "This tool is disabled for this agent."
}
