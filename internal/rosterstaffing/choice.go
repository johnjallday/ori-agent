package rosterstaffing

import "github.com/johnjallday/ori-agent/internal/types"

// RoleChoice is explicit public draft input, never assignment authority. The
// host resolves declarations and original advisory handles, then freezes the
// private RoleIntent. No prompt, ownership, grant or source witness is accepted
// through this shape.
type RoleChoice struct {
	RoleID          string                 `json:"role_id"`
	Mode            string                 `json:"mode"`
	Name            string                 `json:"name"`
	Recommendation  string                 `json:"recommendation,omitempty"`
	Provider        string                 `json:"provider,omitempty"`
	Model           string                 `json:"model,omitempty"`
	ReasoningEffort string                 `json:"reasoning_effort,omitempty"`
	Appearance      *types.AgentAppearance `json:"appearance,omitempty"`
}

// Choices projects exact reviewed effects for existing review surfaces. It
// neither renews a selection nor reconstructs advisory/source authority.
func (i *TeamIntent) Choices() []RoleChoice {
	if i == nil {
		return nil
	}
	out := make([]RoleChoice, 0, len(i.Roles))
	for _, role := range i.Roles {
		out = append(out, RoleChoice{RoleID: role.RoleID, Mode: role.Mode, Name: role.Name,
			Provider: role.Provider, Model: role.Model, ReasoningEffort: role.ReasoningEffort, Appearance: role.Appearance.Clone()})
	}
	return out
}
