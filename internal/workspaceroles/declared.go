package workspaceroles

import (
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// maxDerivedDescription bounds the one-line description derived from an
// ordinary blueprint agent's system prompt.
const maxDerivedDescription = 160

// FromAssistantProgram reads the declared roles of an assistant-program
// blueprint. These already are slots: the declaration carries a stable id, a
// scope, and a required flag, which is why this feature is a UI change on top
// of a domain that always modelled it.
func FromAssistantProgram(declaration *workspace.AssistantProgramDeclaration) []Role {
	if declaration == nil {
		return nil
	}
	roles := make([]Role, 0, len(declaration.Roles))
	for _, spec := range declaration.Roles {
		scope := Scope(spec.Scope)
		if scope == "" {
			scope = ScopeProject
		}
		roles = append(roles, Role{
			ID:          normalizeRoleID(spec.ID),
			Label:       strings.TrimSpace(spec.Label),
			Description: strings.TrimSpace(spec.Description),
			Scope:       scope,
			Required:    spec.Required,
			Primary:     spec.Primary,
		})
	}
	return roles
}

// FromTemplateAgents reads the declared roles of an ordinary blueprint, whose
// roster is a list of agents rather than a list of slots.
//
// Identity is the name-derived slug (PRD D4), not the position: an index
// rebinds the wrong agent as soon as a template is edited.
//
// Requiredness has no declared source here, so it follows the roster contract
// the templates already have — the first entry is the workspace's entry agent
// and every later entry is a specialist. The first role is therefore primary
// and required; the rest are optional. Marking every specialist "Missing"
// would read as four failures for a team the user never asked for, which is
// exactly the alarm FR6 exists to avoid.
func FromTemplateAgents(specs []projecttemplates.AgentSpec) []Role {
	if len(specs) == 0 {
		return nil
	}
	ids := projecttemplates.AgentRoleIDs(specs)
	roles := make([]Role, 0, len(specs))
	for index, spec := range specs {
		roles = append(roles, Role{
			ID:          ids[index],
			Label:       strings.TrimSpace(spec.Name),
			Description: summarize(spec.SystemPrompt),
			Scope:       ScopeProject,
			Required:    index == 0,
			Primary:     index == 0,
		})
	}
	return roles
}

// summarize reduces a system prompt to one short line for the roster row. A
// template agent has no description field, and its prompt's first sentence is
// the closest honest answer to "what is this role for".
func summarize(prompt string) string {
	text := strings.Join(strings.Fields(prompt), " ")
	if text == "" {
		return ""
	}
	if end := strings.IndexAny(text, ".!?"); end > 0 && end+1 <= maxDerivedDescription {
		return text[:end+1]
	}
	if len(text) <= maxDerivedDescription {
		return text
	}
	clipped := text[:maxDerivedDescription]
	if space := strings.LastIndexFunc(clipped, unicode.IsSpace); space > 0 {
		clipped = clipped[:space]
	}
	return strings.TrimRight(clipped, " ,;:-") + "…"
}
