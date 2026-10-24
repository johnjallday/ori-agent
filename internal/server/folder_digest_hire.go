package server

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
)

// hireRoleLabel is the role a hire card shows for an agent: the role the
// blueprint gives it, or the blueprint's own name for an agent with no
// particular role.
func hireRoleLabel(role, blueprintLabel string) string {
	role = strings.TrimSpace(role)
	if role == "" || strings.EqualFold(role, "general") {
		return strings.TrimSpace(blueprintLabel)
	}
	return role
}

// pluginHire names who a recognized project's plan hires, and the brief its
// card starts from. While the integration's plugin is not ready its blueprint
// cannot be read, so the card names the capability and the shape's brief; once
// it is, the blueprint's roster and goal are read the way the Create Workspace
// review reads them. A roster that cannot be read still gets a hire card,
// named by the blueprint.
func (h *folderSetupHost) pluginHire(userID string, target setupTarget, integration foldersetup.Plugin) (*personalassistant.FolderHire, string, *personalassistant.FolderSetupModel) {
	model := &personalassistant.FolderSetupModel{State: "not_checked"}
	label := target.row.Blueprint.Label
	shape := target.row.Shape
	if integration.State != personalassistant.FolderInstallReady || h.builder == nil || h.builder.sessionHandler == nil {
		return &personalassistant.FolderHire{Mode: personalassistant.FolderHirePending, RoleLabel: label},
			personalassistant.FolderDefaultBrief(shape, ""), model
	}
	composition := "standalone"
	if target.provider != nil {
		composition = "grouped"
	}
	templateID := "plugin:" + target.entry.PluginID + ":" + target.entry.ExpectedBlueprintID
	read, err := h.builder.sessionHandler.FolderOfferHire(templateID, userID, composition)
	if err != nil {
		return &personalassistant.FolderHire{Mode: personalassistant.FolderHireCreate, RoleLabel: label},
			personalassistant.FolderDefaultBrief(shape, ""), model
	}
	hire := &personalassistant.FolderHire{
		Mode: personalassistant.FolderHireCreate, AgentName: read.AgentName,
		RoleLabel: hireRoleLabel(read.RoleLabel, label), Model: read.Model,
	}
	if read.Mode == sessionhttp.FolderOfferHireBind {
		hire.Mode = personalassistant.FolderHireBind
	}
	if read.ModelChecked {
		model.State, model.Provider, model.Name = "missing", read.Provider, read.Model
		if read.Provider != "" && read.Model != "" {
			model.State = "configured"
		}
		switch read.ModelSource {
		case "existing":
			model.Settings, model.AgentName = "agent", read.AgentName
		case "system":
			model.Settings = "system"
		}
	}
	return hire, personalassistant.FolderDefaultBrief(shape, read.Goal), model
}

// softwareHire names who a software project's plan hires: the assistant the
// reviewed roster creates or reuses, or the one the folder's existing
// workspace keeps. Only a plan that creates a workspace asks for a brief.
func (h *folderSetupHost) softwareHire(plan personalassistant.FolderSetupPlan, roster sessionhttp.FolderOfferRoster, row folderdigest.CapabilityRow) (*personalassistant.FolderHire, string) {
	intent := plan.Intent.Software
	if intent == nil || strings.TrimSpace(intent.Assistant) == "" {
		return nil, ""
	}
	hire := &personalassistant.FolderHire{
		Mode: personalassistant.FolderHireBind, AgentName: strings.TrimSpace(intent.Assistant),
		RoleLabel: hireRoleLabel(roster.Role, row.Blueprint.Label), Model: strings.TrimSpace(intent.Model),
	}
	switch intent.AssistantAction {
	case personalassistant.FolderSoftwareCreate:
		hire.Mode = personalassistant.FolderHireCreate
	case personalassistant.FolderSoftwareKeep:
		// The workspace's own assistant, whatever the blueprint would make.
		hire.RoleLabel = strings.TrimSpace(row.Blueprint.Label)
		if _, model, found := h.builder.sessionHandler.SavedAgentModel(hire.AgentName); found {
			hire.Model = strings.TrimSpace(model)
		}
	}
	if intent.Outcome != personalassistant.FolderSoftwareCreate {
		return hire, ""
	}
	return hire, personalassistant.FolderDefaultBrief(row.Shape, roster.Goal)
}
