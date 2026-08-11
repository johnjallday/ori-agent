package agenthttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// agentConfigVersion returns a short, deterministic token that changes only when
// an agent's *editable definition* changes. It is the optimistic-concurrency
// token used to detect stale edits (Agents page redesign, PRD FR13): the detail
// endpoint echoes it, and handleUpdate rejects a PUT whose expected version no
// longer matches the stored agent.
//
// The token is derived from the config-defining fields only — deliberately NOT
// from statistics or evolution. Statistics.UpdatedAt is bumped on every message
// (RecordMessage/RecordTokens), so hashing activity would fire false conflicts
// on any busy agent. The field set mirrors the shared-definition fields the
// update handler already treats as consequential.
func agentConfigVersion(a *agent.Agent) string {
	if a == nil {
		return ""
	}

	// Canonical projection of the editable definition. json.Marshal emits struct
	// fields in declaration order and map keys sorted, so this is stable across
	// calls and serialization round-trips.
	type versionedSettings struct {
		Model           string  `json:"model"`
		Temperature     float64 `json:"temperature"`
		SystemPrompt    string  `json:"system_prompt"`
		Provider        string  `json:"provider"`
		ReasoningEffort string  `json:"reasoning_effort"`
		MaxOutputTokens int     `json:"max_output_tokens"`
		AllowWebSearch  bool    `json:"allow_web_search"`
	}
	type versionedMetadata struct {
		Description    string                     `json:"description"`
		Tags           []string                   `json:"tags"`
		Favorite       bool                       `json:"favorite"`
		RoutingProfile *types.AgentRoutingProfile `json:"routing_profile"`
	}
	payload := struct {
		Type     string            `json:"type"`
		Role     types.AgentRole   `json:"role"`
		Settings versionedSettings `json:"settings"`
		Metadata versionedMetadata `json:"metadata"`
		// Appearance is part of the editable definition, so a concurrent
		// appearance change is caught by the same stale-edit guard as a prompt
		// change (PRD FR-16).
		//
		// It is hashed as the canonical object including inactive sources: two
		// agents that render identically but hold different saved uploads are
		// genuinely different definitions, and an edit based on one must not
		// silently overwrite the other.
		Appearance *types.AgentAppearance `json:"appearance"`
	}{
		Type: a.Type,
		Role: a.Role,
		Settings: versionedSettings{
			Model:           a.Settings.Model,
			Temperature:     a.Settings.Temperature,
			SystemPrompt:    a.Settings.SystemPrompt,
			Provider:        a.Settings.Provider,
			ReasoningEffort: a.Settings.ReasoningEffort,
			MaxOutputTokens: a.Settings.MaxOutputTokens,
			AllowWebSearch:  a.Settings.IsWebSearchAllowed(),
		},
		Appearance: a.Appearance,
	}
	if a.Metadata != nil {
		payload.Metadata = versionedMetadata{
			Description:    a.Metadata.Description,
			Tags:           a.Metadata.Tags,
			Favorite:       a.Metadata.Favorite,
			RoutingProfile: a.Metadata.RoutingProfile,
		}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		// Marshal of this closed struct cannot realistically fail; return empty so
		// callers treat the version as absent rather than panicking.
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8]) // 16 hex chars — ample collision resistance here
}
