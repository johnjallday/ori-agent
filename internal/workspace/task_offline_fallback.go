package workspace

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// Reason codes for offline/fallback blocked states (WS8).
const (
	reasonLocalProviderOffline = "local_provider_offline"
	reasonFallbackNeedsCloudOK = "fallback_requires_cloud_confirmation"
)

// buildOfflineBlockedError converts an unreachable-local-provider failure into an
// actionable blocked state instead of a generic error (WS8.31).
func (h *LLMTaskHandler) buildOfflineBlockedError(task Task, providerName string, cause error) error {
	logger.Warn("Local provider offline; blocking task", logger.Fields{
		"workspace_id": task.WorkspaceID,
		"task_id":      task.ID,
		"provider":     providerName,
		"error":        cause.Error(),
	})
	return &TaskBlockedError{
		ReasonCode:       reasonLocalProviderOffline,
		Reason:           fmt.Sprintf("The local provider %q is unreachable: %v", providerName, cause),
		Question:         fmt.Sprintf("The local provider %q appears to be offline. Retry, switch to another agent, or mark the task failed?", providerName),
		SuggestedActions: []string{"retry", "switch_agent_retry", "mark_failed"},
	}
}

// isLocalProviderOfflineError reports whether an error is the offline blocked
// state, so Execute can decide whether to fall back.
func isLocalProviderOfflineError(err error) bool {
	if be, ok := AsTaskBlockedError(err); ok {
		return be.ReasonCode == reasonLocalProviderOffline
	}
	return false
}

// resolveAgentFallback returns the agent's opt-in fallback provider/model, if set
// (WS8.32). FallbackModel defaults to the primary model when empty.
func resolveAgentFallback(ag *resolvedTaskAgent) (provider string, model string, ok bool) {
	if ag == nil {
		return "", "", false
	}
	provider = strings.TrimSpace(ag.Settings.FallbackProvider)
	if provider == "" {
		return "", "", false
	}
	model = strings.TrimSpace(ag.Settings.FallbackModel)
	if model == "" {
		model = strings.TrimSpace(ag.Settings.Model)
	}
	return provider, model, true
}

// fallbackCloudSpendGate blocks a local->cloud fallback for confirmation unless
// the agent has explicitly opted in, so a task cannot silently incur cloud spend
// (WS8.33b). A local->local fallback (or an opted-in cloud one) returns nil.
func (h *LLMTaskHandler) fallbackCloudSpendGate(ag *resolvedTaskAgent, task Task, agentName, fallbackProvider string) error {
	provider, err := h.llmFactory.GetProvider(fallbackProvider)
	if err != nil {
		// Let runTaskOnProvider surface the resolution error with full context.
		return nil
	}
	if provider.Type() != llm.ProviderTypeCloud {
		return nil // local->local: no spend risk
	}
	if ag != nil && ag.Settings.FallbackAllowCloud != nil && *ag.Settings.FallbackAllowCloud {
		return nil // explicitly allowed
	}
	logger.Info("Local->cloud fallback requires confirmation", logger.Fields{
		"workspace_id":      task.WorkspaceID,
		"task_id":           task.ID,
		"fallback_provider": fallbackProvider,
	})
	return &TaskBlockedError{
		ReasonCode:       reasonFallbackNeedsCloudOK,
		Reason:           fmt.Sprintf("The local provider is offline and the configured fallback %q is a paid cloud provider.", fallbackProvider),
		Question:         fmt.Sprintf("Allow this task to fall back to the paid cloud provider %q (incurs cost)?", fallbackProvider),
		SuggestedActions: []string{"allow_cloud_fallback_retry", "retry", "mark_failed"},
	}
}

// reportProviderFallback logs and emits a provider fallback (with the
// re-resolution note) so it is visible on the execution trace (WS8.32/8.33a).
func (h *LLMTaskHandler) reportProviderFallback(task Task, agentName, from, to string) {
	logger.Warn("Falling back to alternative provider", logger.Fields{
		"workspace_id": task.WorkspaceID,
		"task_id":      task.ID,
		"from":         from,
		"to":           to,
		"reason":       reasonLocalProviderOffline,
	})
	if h.eventBus != nil {
		h.eventBus.Publish(NewTaskEvent(EventTaskThinking, task.WorkspaceID, task.ID, agentName, map[string]any{
			"phase":       "provider_fallback",
			"from":        from,
			"to":          to,
			"reason":      reasonLocalProviderOffline,
			"re_resolved": true,
		}))
	}
}
