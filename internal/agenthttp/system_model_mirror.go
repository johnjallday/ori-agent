package agenthttp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
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
	// The agent's reasoning level is already normalized for its provider, so an
	// unknown value can only come from an older record.
	err := ApplySystemModel(h.systemModel, h.providers, h.State, provider, model, record.Settings.ReasoningEffort)
	var unavailable *UnavailableProviderError
	switch {
	case err == nil:
		out.Mirrored = true
	case errors.Is(err, ErrSystemModelIncomplete):
		out.Reason = "the agent has no provider or model, so the Default model was left unchanged"
	case errors.As(err, &unavailable):
		out.Reason = err.Error() + ", so the Default model was left unchanged"
	default:
		out.Reason = err.Error()
	}
	return out
}

// ErrSystemModelIncomplete is ApplySystemModel's refusal of an empty provider
// or model. It never clears the Default model; Settings is the only surface
// that can do that.
var ErrSystemModelIncomplete = errors.New("a provider and a model are both required")

// UnavailableProviderError names a provider this server has not registered
// (no API key or runtime), which the Settings page refuses too.
type UnavailableProviderError struct {
	Provider string
}

func (e *UnavailableProviderError) Error() string {
	return fmt.Sprintf("provider %q is not available on this server", e.Provider)
}

// SystemModelSaveError is a failure to write settings.json after the values
// passed validation: the request was fine and a retry may succeed. Its text is
// the underlying error's.
type SystemModelSaveError struct {
	Err error
}

func (e *SystemModelSaveError) Error() string { return e.Err.Error() }
func (e *SystemModelSaveError) Unwrap() error { return e.Err }

// ApplySystemModel saves provider and model as Settings → Default model with
// the checks a Settings save makes: the provider must be registered on this
// server, the values must pass the settings validation, and the reasoning level
// is kept for Codex only (default medium) and cleared for everyone else. After
// the save the protected system assistant is aligned, as a Settings save does,
// so the drawer talks on the new model without a restart.
//
// It is the one vetted write behind both the agent-edit mirror and the
// personal assistant hire. It returns ErrSystemModelIncomplete,
// *UnavailableProviderError, a settings validation error, or a
// *SystemModelSaveError, and changes nothing on any of them except the last.
func ApplySystemModel(writer SystemModelWriter, providers ProviderAvailability, st store.Store, provider, model, reasoningEffort string) error {
	if writer == nil {
		return errors.New("the Default model cannot be saved on this server")
	}
	provider = normalizeSystemProviderName(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return ErrSystemModelIncomplete
	}
	if providers != nil {
		if _, err := providers.GetProvider(provider); err != nil {
			return &UnavailableProviderError{Provider: provider}
		}
	}
	if err := writer.SetSystemModel(provider, model); err != nil {
		return err
	}
	// The effort setting is Codex's; every other provider clears it, exactly as
	// a save on the Settings page does.
	if err := writer.SetSystemReasoningEffort(systemReasoningEffortFor(provider, reasoningEffort)); err != nil {
		return err
	}
	if err := writer.Save(); err != nil {
		return &SystemModelSaveError{Err: err}
	}
	// The protected system assistant follows the Default model on every path.
	if err := ensureSystemAssistantAgentWithSystemModel(st, provider, model); err != nil {
		logger.Warn("Failed to align the system assistant after the Default model changed", logger.Fields{
			"provider": provider,
			"model":    model,
			"error":    err,
		})
	}
	return nil
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
