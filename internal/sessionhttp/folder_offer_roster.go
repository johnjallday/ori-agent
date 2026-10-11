package sessionhttp

import (
	"errors"
	"strings"
)

// FolderOfferRoster is the entry assistant a folder-offer workspace would get
// from a blueprint, read the same way the Create Workspace Team step reads it:
// the blueprint's own roster plan, with the one rename a review may ask for.
// Reading it creates no agent and no workspace.
type FolderOfferRoster struct {
	TemplateID       string
	TemplateName     string
	TemplateRevision string
	// PlanRevision is the revision of the blueprint's roster plan as the
	// creation endpoint computes it. A create that carries this revision is
	// refused if the roster, a saved agent of the same name, or the system
	// model has changed since.
	PlanRevision string
	// DeclaredName is the blueprint's name for the assistant; Assistant is the
	// name it will have, which differs only when the review renamed it.
	DeclaredName string
	Assistant    string
	// Action is "create" for a new saved agent and "reuse" for a saved agent of
	// that name, which is attached exactly as saved.
	Action      string
	Provider    string
	Model       string
	ModelSource string
}

// SavedAgentModel reads the provider and model a saved agent is set to use.
// found is false when no agent of that name is saved. It changes nothing.
func (h *Handler) SavedAgentModel(name string) (provider, model string, found bool) {
	if h == nil || h.agentStore == nil || strings.TrimSpace(name) == "" {
		return "", "", false
	}
	agent, exists := h.agentStore.GetAgent(strings.TrimSpace(name))
	if !exists || agent == nil {
		return "", "", false
	}
	return strings.TrimSpace(agent.Settings.Provider), strings.TrimSpace(agent.Settings.Model), true
}

// ErrFolderOfferRoster reports a blueprint that cannot staff a folder-offer
// workspace: not installed, without a roster, or with a name that is not valid.
var ErrFolderOfferRoster = errors.New("the blueprint has no usable assistant roster")

// FolderOfferRoster returns the entry assistant of an installed blueprint.
// assistantName, when it differs from the blueprint's name, is applied through
// the same override the Team step sends.
func (h *Handler) FolderOfferRoster(templateID, assistantName string) (FolderOfferRoster, error) {
	if h == nil || strings.TrimSpace(templateID) == "" {
		return FolderOfferRoster{}, ErrFolderOfferRoster
	}
	raw, err := h.resolveProjectTemplate(templateID, "")
	if err != nil || len(raw.Agents) == 0 {
		return FolderOfferRoster{}, ErrFolderOfferRoster
	}
	effective := raw
	declared := strings.TrimSpace(raw.Agents[0].Name)
	assistantName = strings.TrimSpace(assistantName)
	if assistantName != "" && assistantName != declared {
		index := 0
		effective, err = applyTemplateAgentOverrides(raw, []templateAgentOverride{{Index: &index, Name: &assistantName}})
		if err != nil {
			return FolderOfferRoster{}, ErrFolderOfferRoster
		}
	}
	plan := h.buildTemplateAgentPlan(effective)
	if len(plan.Agents) == 0 || strings.TrimSpace(plan.Agents[0].Name) == "" {
		return FolderOfferRoster{}, ErrFolderOfferRoster
	}
	entry := plan.Agents[0]
	return FolderOfferRoster{
		TemplateID: raw.ID, TemplateName: raw.Name, TemplateRevision: raw.Revision,
		PlanRevision: h.buildTemplateAgentPlan(raw).Revision,
		DeclaredName: declared, Assistant: strings.TrimSpace(entry.Name), Action: entry.Action,
		Provider: entry.Provider, Model: entry.Model, ModelSource: entry.ModelSource,
	}, nil
}
