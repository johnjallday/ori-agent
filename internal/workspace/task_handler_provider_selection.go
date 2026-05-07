package workspace

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// Provider selection. These helpers pick the right LLM provider for a task
// given the agent's configured provider/model pair, including model-family
// detection and a few well-known mismatches that need auto-correction (e.g.
// "openai" configured but a Claude model in the field).

// normalizeProviderName collapses common provider aliases ("anthropic" →
// "claude") so downstream comparisons can use a stable name.
func normalizeProviderName(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case "anthropic":
		return "claude"
	default:
		return normalized
	}
}

func isClaudeFamilyModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "claude-") {
		return true
	}
	return normalized == "haiku" || normalized == "sonnet" || normalized == "opus"
}

func isGeminiFamilyModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(normalized, "gemini")
}

func isCodexFamilyModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(normalized, "codex")
}

// normalizeModelForProvider expands the short Claude Code aliases ("haiku"/
// "sonnet"/"opus") into Anthropic API model IDs when the resolved provider
// is the Claude API. Other providers see the model unchanged.
func (h *LLMTaskHandler) normalizeModelForProvider(providerName, model string) string {
	trimmedModel := strings.TrimSpace(model)
	normalizedModel := strings.ToLower(trimmedModel)

	if providerName == "claude" {
		switch normalizedModel {
		case "haiku":
			return "claude-3-5-haiku-latest"
		case "sonnet":
			return "claude-3-5-sonnet-latest"
		case "opus":
			return "claude-3-opus-latest"
		}
	}

	return trimmedModel
}

// getProviderForAgent resolves the best provider for the agent's configured
// provider/model. It auto-corrects two mismatch cases that we've seen drift
// in: (a) explicit "openai" with a non-OpenAI model (use the inferred one);
// (b) explicit "claude" with a short alias when claude_code is available
// (route to claude_code, since Anthropic's REST API doesn't accept the
// short aliases). Falls back to inferred when the explicit provider isn't
// registered, otherwise preserves the explicit name so upstream errors
// surface clearly.
func (h *LLMTaskHandler) getProviderForAgent(configuredProvider, model string) string {
	explicitProvider := normalizeProviderName(configuredProvider)
	inferredProvider := h.getProviderForModel(model)

	if explicitProvider == "" {
		return inferredProvider
	}

	if h.llmFactory.HasProvider(explicitProvider) {
		// Auto-correct common stale mismatch cases where provider was not persisted with model updates.
		if explicitProvider == "openai" &&
			inferredProvider != "" &&
			inferredProvider != "openai" &&
			(isClaudeFamilyModel(model) || isGeminiFamilyModel(model) || isCodexFamilyModel(model)) {
			logFields := logger.Fields{
				"configured_provider": explicitProvider,
				"inferred_provider":   inferredProvider,
				"model":               model,
			}
			if h.llmFactory.HasProvider(inferredProvider) {
				logger.Warn("Detected provider/model mismatch; using inferred provider for task execution", logFields)
				return inferredProvider
			}
			logger.Warn("Detected provider/model mismatch; inferred provider is not configured, keeping configured provider", logFields)
			return explicitProvider
		}

		// Claude API does not accept short Claude Code model aliases.
		if explicitProvider == "claude" && isClaudeFamilyModel(model) &&
			(strings.EqualFold(strings.TrimSpace(model), "haiku") ||
				strings.EqualFold(strings.TrimSpace(model), "sonnet") ||
				strings.EqualFold(strings.TrimSpace(model), "opus")) &&
			h.llmFactory.HasProvider("claude_code") {
			logger.Warn("Detected Claude short model alias; using claude_code provider", logger.Fields{
				"configured_provider": explicitProvider,
				"inferred_provider":   "claude_code",
				"model":               model,
			})
			return "claude_code"
		}

		return explicitProvider
	}

	if inferredProvider != "" && h.llmFactory.HasProvider(inferredProvider) {
		logger.Warn("Configured provider unavailable; falling back to inferred provider", logger.Fields{
			"configured_provider": explicitProvider,
			"inferred_provider":   inferredProvider,
			"model":               model,
		})
		return inferredProvider
	}

	// Preserve configured name for clearer upstream error messaging if no fallback exists.
	return explicitProvider
}

// getProviderForModel infers a provider purely from the model name string
// (no agent configuration). Used by getProviderForAgent for the
// inferred-provider lane and as a standalone fallback when no provider is
// configured at all.
func (h *LLMTaskHandler) getProviderForModel(model string) string {
	trimmedModel := strings.TrimSpace(model)
	normalizedModel := strings.ToLower(trimmedModel)
	if trimmedModel == "" {
		return "openai"
	}

	// Claude Code short aliases map directly to claude_code when available.
	if normalizedModel == "haiku" || normalizedModel == "sonnet" || normalizedModel == "opus" {
		if h.llmFactory.HasProvider("claude_code") {
			return "claude_code"
		}
		if h.llmFactory.HasProvider("claude") {
			return "claude"
		}
		// Keep this in the Claude family even if provider isn't currently configured.
		return "claude"
	}

	// Check for Claude API models.
	if strings.HasPrefix(normalizedModel, "claude-") {
		return "claude"
	}

	// Check for Gemini models.
	if strings.HasPrefix(normalizedModel, "gemini") {
		return "gemini"
	}

	// Check for Codex models.
	if strings.HasPrefix(normalizedModel, "codex") {
		return "codex"
	}

	if localProvider := llm.FindLocalProviderByModel(h.llmFactory, trimmedModel); localProvider != "" {
		logger.Info("Model found in local provider, using local provider", logger.Fields{
			"model":    trimmedModel,
			"provider": localProvider,
		})
		return localProvider
	}

	// Default to OpenAI
	return "openai"
}
