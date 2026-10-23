package sessionhttp

import (
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
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
	// Role is the role the blueprint gives the assistant, and Goal the
	// blueprint's own workspace goal; either may be empty.
	Role string
	Goal string
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
		Role: strings.TrimSpace(entry.Role), Goal: strings.TrimSpace(raw.Goal),
	}, nil
}

// FolderOfferHire is who a project made from an installed blueprint would get
// for its project role, read the way the Create Workspace review previews it:
// a new agent, or the Home's shared assistant its standing consent names. Mode
// is "create" or "bind". Reading it creates nothing and grants nothing.
type FolderOfferHire struct {
	Mode      string
	AgentName string
	RoleLabel string
	Model     string
	Provider  string
	// ModelSource identifies the applicable configuration, not connectivity.
	ModelSource  string // existing, system, template
	ModelChecked bool
	// Goal is the blueprint's own workspace goal; it may be empty.
	Goal string
}

// FolderOfferHire previews the project-role hire of an installed blueprint for
// one owner in the given composition ("grouped" or "standalone"). A blueprint
// whose roles are not scoped falls back to its roster's entry assistant.
func (h *Handler) FolderOfferHire(templateID, ownerUserID, composition string) (FolderOfferHire, error) {
	if h == nil || strings.TrimSpace(templateID) == "" {
		return FolderOfferHire{}, ErrFolderOfferRoster
	}
	source, err := h.resolveProjectTemplate(templateID, "")
	if err != nil {
		return FolderOfferHire{}, ErrFolderOfferRoster
	}
	ownerUserID = strings.TrimSpace(ownerUserID)
	effective := source
	if strings.TrimSpace(composition) == "standalone" && source.GroupRequirement != nil {
		if standalone, standaloneErr := projecttemplates.StandaloneTemplate(source); standaloneErr == nil {
			effective = standalone
		}
	}
	var ownerErr error
	if ownerUserID == "" {
		ownerErr = grouprequirements.ErrUnavailable
	}
	h.buildTemplateGroupRequirementPlan(source, composition, ownerUserID, ownerErr, &effective)
	roster := h.buildTemplateAgentPlanForOwner(effective, ownerUserID)
	hire := FolderOfferHire{Goal: strings.TrimSpace(source.Goal)}
	for _, fill := range h.planRoleAutoFills(source, effective, composition, ownerUserID) {
		if fill.Scope != string(agentworkspace.AssistantRoleScopeProject) || strings.TrimSpace(fill.AgentName) == "" {
			continue
		}
		hire.Mode, hire.AgentName, hire.RoleLabel = FolderOfferHireCreate, strings.TrimSpace(fill.AgentName), strings.TrimSpace(fill.Label)
		if fill.Outcome == roleAutoFillExisting {
			hire.Mode = FolderOfferHireBind
		}
		break
	}
	if hire.AgentName == "" {
		if len(roster.Agents) == 0 || strings.TrimSpace(roster.Agents[0].Name) == "" {
			return FolderOfferHire{}, ErrFolderOfferRoster
		}
		entry := roster.Agents[0]
		hire.Mode, hire.AgentName, hire.RoleLabel = FolderOfferHireCreate, strings.TrimSpace(entry.Name), strings.TrimSpace(entry.Role)
		if entry.Action == "reuse" {
			hire.Mode = FolderOfferHireBind
		}
	}
	// The model the agent would run on: a saved agent's own, a roster agent's
	// resolved one, else the model new agents get.
	if provider, model, found := h.SavedAgentModel(hire.AgentName); found {
		hire.Provider, hire.Model = provider, model
		hire.ModelSource, hire.ModelChecked = "existing", true
	} else {
		for _, agent := range roster.Agents {
			if strings.EqualFold(strings.TrimSpace(agent.Name), hire.AgentName) {
				hire.Provider, hire.Model = strings.TrimSpace(agent.Provider), strings.TrimSpace(agent.Model)
				hire.ModelSource = agent.ModelSource
				hire.ModelChecked = hire.Model != "" && hire.Provider != ""
				break
			}
		}
		if hire.Model == "" && h.systemModelReader != nil {
			hire.Provider, hire.Model = h.systemModelReader.GetSystemModel()
			hire.ModelSource, hire.ModelChecked = "system", true
		}
	}
	hire.Provider, hire.Model = strings.TrimSpace(hire.Provider), strings.TrimSpace(hire.Model)
	return hire, nil
}

// The modes FolderOfferHire reports.
const (
	FolderOfferHireCreate = "create"
	FolderOfferHireBind   = "bind"
)
