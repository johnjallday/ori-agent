package agenthttp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Structured codes an editor branches on. Messages are for people; these are
// for code, so a client never has to guess from wording.
const (
	staleAgentEditCode   = "stale_agent_edit"
	agentRenameFailedErr = "agent_rename_failed"
)

// agentUpdateFields are the definition fields PATCH /api/agents/{name} edits.
// Every field is optional: nil leaves the stored value alone.
type agentUpdateFields struct {
	Role            *string  `json:"role,omitempty"`
	Model           *string  `json:"model,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	LLMProvider     *string  `json:"llm_provider,omitempty"`
	ReasoningEffort *string  `json:"reasoning_effort,omitempty"`
	MaxTokens       *int     `json:"max_output_tokens,omitempty"`
	SystemPrompt    *string  `json:"system_prompt,omitempty"`
	AllowWebSearch  *bool    `json:"allow_web_search,omitempty"`
	// Metadata
	Description    *string                    `json:"description,omitempty"`
	Tags           *[]string                  `json:"tags,omitempty"`
	Favorite       *bool                      `json:"favorite,omitempty"`
	RoutingProfile *types.AgentRoutingProfile `json:"routing_profile,omitempty"`
	// ExpertMode lifts stage-based skill slot caps for this agent (PRD FR13).
	// Metadata-only; does not touch model/prompt/skills.
	ExpertMode *bool `json:"expert_mode,omitempty"`
	// Appearance selects the agent's visual source. Presentation only: it
	// never changes role, model, tools, skills, permissions, routing, or
	// execution (FR-17).
	Appearance *appearanceRequest `json:"appearance,omitempty"`
}

// staleAgentEditError refuses an edit whose loaded version is no longer the
// stored one.
type staleAgentEditError struct {
	current string
}

func (e *staleAgentEditError) Error() string { return "agent definition changed since it was loaded" }

// agentUpdateInvalidError refuses an edit whose values do not validate.
type agentUpdateInvalidError struct {
	message string
}

func (e *agentUpdateInvalidError) Error() string { return e.message }

// applyAgentUpdate applies f to draft, the store's private copy of the
// definition. It may fail part-way: the store discards the draft on any error,
// so nothing assigned before the failing check survives (FR-58).
func applyAgentUpdate(draft *agent.Agent, f *agentUpdateFields) error {
	if draft.Metadata == nil {
		draft.Metadata = &types.AgentMetadata{}
	}
	if f.Role != nil {
		draft.Role = types.AgentRole(*f.Role)
	}
	if f.Model != nil {
		draft.Settings.Model = *f.Model
	}
	if f.Temperature != nil {
		draft.Settings.Temperature = *f.Temperature
	}
	if f.LLMProvider != nil {
		draft.Settings.Provider = *f.LLMProvider
	}
	if f.ReasoningEffort != nil {
		effort, err := normalizeAgentReasoningEffort(draft.Settings.Provider, draft.Settings.Model, *f.ReasoningEffort)
		if err != nil {
			return &agentUpdateInvalidError{message: err.Error()}
		}
		draft.Settings.ReasoningEffort = effort
	}
	if f.MaxTokens != nil {
		draft.Settings.MaxOutputTokens = *f.MaxTokens
	}
	if f.SystemPrompt != nil {
		draft.Settings.SystemPrompt = *f.SystemPrompt
	}
	if f.AllowWebSearch != nil {
		allow := *f.AllowWebSearch
		draft.Settings.AllowWebSearch = &allow
	}
	// A provider or model change can leave a stored level the new pair does not
	// accept (for example "max" moving from Claude Code to Codex); drop it
	// rather than keep a setting the runtime would ignore or reject.
	draft.Settings.ReasoningEffort = types.NormalizeReasoningEffortFor(
		draft.Settings.Provider, draft.Settings.Model, draft.Settings.ReasoningEffort)

	if f.Description != nil {
		draft.Metadata.Description = *f.Description
	}
	if f.Tags != nil {
		draft.Metadata.Tags = append([]string{}, (*f.Tags)...)
	}
	if f.Favorite != nil {
		draft.Metadata.Favorite = *f.Favorite
	}
	if f.RoutingProfile != nil {
		draft.Metadata.RoutingProfile = cloneRoutingProfile(f.RoutingProfile)
	}
	if f.ExpertMode != nil {
		expert := *f.ExpertMode
		draft.Metadata.ExpertMode = &expert
	}
	if !f.Appearance.isEmpty() {
		next, err := applyAppearanceRequest(draft.Appearance, f.Appearance)
		if err != nil {
			return &agentUpdateInvalidError{message: err.Error()}
		}
		draft.Appearance = next
	}
	draft.EnsureAppearance()
	return nil
}

// writeAgentMutationError answers a refused or failed definition mutation.
// Nothing was written in any of these cases.
func writeAgentMutationError(w http.ResponseWriter, agentName string, err error) {
	var stale *staleAgentEditError
	var invalid *agentUpdateInvalidError
	switch {
	case errors.As(err, &stale):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error":           staleAgentEditCode,
			"code":            staleAgentEditCode,
			"message":         fmt.Sprintf("%q was changed since you loaded it. Reload the latest version and reapply your edit.", agentName),
			"current_version": stale.current,
		})
	case errors.As(err, &invalid):
		orihttp.BadRequest(w, invalid.message)
	case errors.Is(err, store.ErrAgentNotFound):
		orihttp.NotFound(w, "Agent not found")
	default:
		logger.Error("Failed to update agent", logger.Fields{"agent": agentName, "error": err})
		WriteAgentStoreError(w, "Failed to update agent", err)
	}
}

// writeRenameFailed answers a request whose definition edit was saved under
// the current name but whose rename did not happen. It is not a refusal: the
// body says exactly what was saved, so the editor can keep the requested name
// as an unsaved change instead of retrying the whole request.
func writeRenameFailed(w http.ResponseWriter, oldName, newName string, committed *agent.Agent, version string, err error) {
	status := http.StatusInternalServerError
	message := fmt.Sprintf("Your other changes were saved, but %q could not be renamed to %q.", oldName, newName)
	if strings.Contains(err.Error(), "already exists") {
		status = http.StatusConflict
		message = fmt.Sprintf("Your other changes were saved, but an agent named %q already exists, so %q kept its name.", newName, oldName)
	}
	_ = orihttp.RespondJSON(w, status, map[string]any{
		"success":          false,
		"error":            agentRenameFailedErr,
		"code":             agentRenameFailedErr,
		"message":          message,
		"definition_saved": true,
		"name":             oldName,
		"requested_name":   newName,
		"version":          version,
		"agent":            definitionReceipt(oldName, committed),
		"appearance":       appearanceForAgent(committed),
	})
}

// definitionReceipt is the committed definition in the detail endpoint's field
// names, so an editor can adopt it field for field.
func definitionReceipt(name string, a *agent.Agent) map[string]any {
	if a == nil {
		return nil
	}
	metadata := map[string]any{}
	if a.Metadata != nil {
		metadata["description"] = a.Metadata.Description
		metadata["tags"] = a.Metadata.Tags
		metadata["favorite"] = a.Metadata.Favorite
		metadata["routing_profile"] = a.Metadata.RoutingProfile
		if a.Metadata.ExpertMode != nil {
			metadata["expert_mode"] = *a.Metadata.ExpertMode
		}
	}
	return map[string]any{
		"name":              name,
		"role":              a.Role,
		"model":             a.Settings.Model,
		"provider":          a.Settings.Provider,
		"temperature":       a.Settings.Temperature,
		"reasoning_effort":  a.Settings.EffectiveReasoningEffort(a.Settings.Provider),
		"max_output_tokens": a.Settings.MaxOutputTokens,
		"system_prompt":     a.Settings.SystemPrompt,
		"allow_web_search":  a.Settings.IsWebSearchAllowed(),
		"metadata":          metadata,
		"appearance":        appearanceForAgent(a),
		"version":           agentConfigVersion(a),
	}
}
