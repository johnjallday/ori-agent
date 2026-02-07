package authdiscovery

import (
	"github.com/johnjallday/ori-agent/internal/logger"
)

// Result represents the discovery result
type Result struct {
	Token  string
	Source string
}

// DiscoverAnthropicToken attempts to discover an Anthropic token
func DiscoverAnthropicToken() string {
	token, err := DiscoverClaudeToken()
	if err == nil && token != "" {
		logger.Debug("Discovered Anthropic token from Claude CLI", logger.Fields{})
		return token
	}
	return ""
}

// DiscoverOpenAIToken attempts to discover an OpenAI/Codex token
func DiscoverOpenAIToken() string {
	token, err := DiscoverCodexToken()
	if err == nil && token != "" {
		logger.Debug("Discovered OpenAI token from Codex CLI", logger.Fields{})
		return token
	}
	return ""
}
