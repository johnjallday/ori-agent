package chathttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
)

func resolveChatProviderName(agentName string, ag *resolvedChatAgent, factory *llm.Factory) (string, error) {
	if ag == nil || ag.Agent == nil {
		return "", fmt.Errorf("agent %q is unavailable", agentName)
	}

	provider := strings.ToLower(strings.TrimSpace(ag.Settings.Provider))
	switch provider {
	case "":
		// Fall through to model-based inference for providers that have
		// unambiguous model namespaces. Do not implicitly treat empty provider
		// as OpenAI.
	case "anthropic":
		return "claude", nil
	case "openai", "codex", "claude_code", "claude", "gemini", "ollama":
		return provider, nil
	default:
		return "", fmt.Errorf("agent %q has unsupported provider %q", agentName, ag.Settings.Provider)
	}

	model := strings.TrimSpace(ag.Settings.Model)
	if model == "" {
		return "", fmt.Errorf("agent %q has no provider configured. Update the agent settings to select a provider explicitly", agentName)
	}

	lowerModel := strings.ToLower(model)
	switch {
	case isCodexProviderOrModel("", model):
		return "codex", nil
	case strings.HasPrefix(lowerModel, "claude-"):
		return "claude", nil
	case strings.HasPrefix(lowerModel, "gemini-"):
		return "gemini", nil
	}

	if factory != nil {
		if ollamaProvider, err := factory.GetProvider("ollama"); err == nil {
			if ollamaProv, ok := ollamaProvider.(*llm.OllamaProvider); ok && ollamaProv.HasModel(model) {
				return "ollama", nil
			}
		}
	}

	return "", fmt.Errorf("agent %q has no provider configured. Update the agent settings to select a provider explicitly", agentName)
}
