package agenthttp

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// SystemModelWriter is the slice of the configuration manager an agent edit
// needs to keep Settings → System Model in step with the assistant's own
// record. *config.Manager satisfies it.
type SystemModelWriter interface {
	GetSystemModel() (provider, model string)
	SetSystemModel(provider, model string) error
	SetSystemReasoningEffort(effort string) error
	Save() error
}

// ProviderAvailability answers whether a provider is registered on this
// server (API key or runtime present). *llm.Factory satisfies it.
type ProviderAvailability interface {
	GetProvider(name string) (llm.Provider, error)
}

// SetSystemModelWriter wires the settings the System Model lives in, plus the
// provider registry that vets a mirrored provider the way the Settings page
// does. Without a writer, an agent edit never touches the System Model.
func (h *Handler) SetSystemModelWriter(writer SystemModelWriter, providers ProviderAvailability) {
	h.systemModel = writer
	h.providers = providers
}

// systemModelLinkedAgent reports whether name's conversations run on Settings
// → System Model rather than on its own record: the protected system
// assistant, or the hired personal assistant's profile — the agent the Home
// drawer is named after. Editing either one's model on the Agents page used
// to change the card and nothing else.
func systemModelLinkedAgent(ctx context.Context, support personalAssistantSupportClassifier, name string) bool {
	return isSystemAssistantAgent(name) || support.isHiredProfile(ctx, name)
}

func (h *Handler) isSystemModelAgent(ctx context.Context, name string) bool {
	return systemModelLinkedAgent(ctx, h.support, name)
}

// systemModelMirror is what an update reports back about the System Model, so
// the editor can say what the assistant's conversations now run on.
type systemModelMirror struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Mirrored bool   `json:"mirrored"`
	Reason   string `json:"reason,omitempty"`
}

// mirrorSystemModel writes a system-model agent's provider and model to
// Settings → System Model after the agent record has been saved. It is the
// reverse of the Settings page's push onto the assistant record
// (EnsureSystemAssistantAgentWithSystemModel), so the two surfaces cannot
// disagree about which model the assistant talks on.
//
// It never clears the System Model: an edit that leaves the provider or model
// empty, names a provider this server has not registered, or fails validation
// is reported in the result and leaves Settings as they were.
func (h *Handler) mirrorSystemModel(record *agent.Agent) *systemModelMirror {
	if h.systemModel == nil || record == nil {
		return nil
	}
	provider := normalizeSystemProviderName(record.Settings.Provider)
	model := strings.TrimSpace(record.Settings.Model)
	out := &systemModelMirror{Provider: provider, Model: model}
	if provider == "" || model == "" {
		out.Reason = "the agent has no provider or model, so the System Model was left unchanged"
		return out
	}
	if h.providers != nil {
		if _, err := h.providers.GetProvider(provider); err != nil {
			out.Reason = fmt.Sprintf("provider %q is not available on this server, so the System Model was left unchanged", provider)
			return out
		}
	}
	if err := h.systemModel.SetSystemModel(provider, model); err != nil {
		out.Reason = err.Error()
		return out
	}
	// The effort setting is Codex's; every other provider clears it, exactly as
	// a save on the Settings page does. The agent's level is already normalized
	// for its provider, so an unknown value can only come from an older record.
	if err := h.systemModel.SetSystemReasoningEffort(systemReasoningEffortFor(provider, record.Settings.ReasoningEffort)); err != nil {
		out.Reason = err.Error()
		return out
	}
	if err := h.systemModel.Save(); err != nil {
		out.Reason = err.Error()
		return out
	}
	out.Mirrored = true
	// The protected system assistant follows the System Model on every path.
	if err := ensureSystemAssistantAgentWithSystemModel(h.State, provider, model); err != nil {
		logger.Warn("Failed to align the system assistant after an agent edit changed the system model", logger.Fields{
			"provider": provider,
			"model":    model,
			"error":    err,
		})
	}
	return out
}

// normalizeSystemProviderName maps an agent record's provider onto the name
// the System Model and the provider registry use.
func normalizeSystemProviderName(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "anthropic" {
		return "claude"
	}
	return provider
}

func systemReasoningEffortFor(provider, effort string) string {
	if provider != "codex" {
		return ""
	}
	switch effort = strings.ToLower(strings.TrimSpace(effort)); effort {
	case "low", "medium", "high", "xhigh":
		return effort
	default:
		return "medium"
	}
}
