package foldersetup

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// Plugin is where one installable plugin stands and what Set up would do to it.
type Plugin struct {
	// Name is the plain-text name shown to the user.
	Name     string
	PluginID string
	// State is one of personalassistant.FolderInstall*.
	State string
	// Version is the release the plan will have in place; InstalledVersion is
	// what is installed now (an update's "from").
	Version          string
	InstalledVersion string
	// Source is the reviewed source's plain label ("owner/repository").
	Source string
}

// PlanFacts are the facts a plan is built from. Every field is read by the host
// from canonical state; the builder only words them.
type PlanFacts struct {
	// AppName is the application the integration connects to.
	AppName string
	// AppInstalled says whether that application is on this computer.
	AppInstalled bool
	// BlueprintLabel names the workspace blueprint.
	BlueprintLabel string
	WorkspaceName  string
	Integration    Plugin
	// Provider is the plugin that supplies the Home a split blueprint needs; nil
	// when the blueprint has none.
	Provider *Plugin
}

// BuildPlan lists every consequence of one press of Set up, in the order the card
// shows them, and the machine-readable intent a run is held to. Every string is
// plain text and names no path.
func BuildPlan(facts PlanFacts) personalassistant.FolderSetupPlan {
	lines := []personalassistant.FolderPlanLine{pluginLine(personalassistant.FolderPlanIntegration, "integration", facts.Integration)}
	intent := personalassistant.FolderSetupIntent{
		Integration: facts.Integration.State, IntegrationPlugin: facts.Integration.PluginID, IntegrationVersion: facts.Integration.Version,
	}
	if facts.Provider != nil {
		line := pluginLine(personalassistant.FolderPlanProvider, "plugin", *facts.Provider)
		if line.Detail == "" {
			line.Detail = "It provides the Home this workspace belongs to."
		}
		lines = append(lines, line)
		intent.Provider, intent.ProviderPlugin, intent.ProviderVersion = facts.Provider.State, facts.Provider.PluginID, facts.Provider.Version
	}
	mode := personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanMode, Name: "Uses File-only mode"}
	if facts.AppInstalled {
		mode.Detail = "Ori does not control " + facts.AppName + "."
	} else {
		// One honest line: nothing here needs the application, and none of it
		// promises a live connection.
		mode.Detail = facts.AppName + " itself is not installed here; File-only mode works without it."
	}
	lines = append(lines,
		personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanWorkspace, Name: "Creates a " + facts.BlueprintLabel + " workspace named " + facts.WorkspaceName},
		personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanFolder, Name: "Links " + facts.WorkspaceName + " where it is", Detail: "Nothing is moved or copied."},
		mode,
		personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanAgents, Name: "Adds the agents this blueprint requires"},
		personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanTask, Name: "Queues a first read-only task for when you open it"},
	)
	plan := personalassistant.NewFolderSetupPlan(lines)
	plan.Intent = intent
	return plan
}

// pluginLine words one plugin's line; noun is "integration" for the project
// integration and "plugin" for a supporting one.
func pluginLine(kind, noun string, p Plugin) personalassistant.FolderPlanLine {
	subject := "the reviewed " + p.Name + " " + noun
	if p.Version != "" && p.State != personalassistant.FolderInstallReady {
		subject += " " + p.Version
	}
	line := personalassistant.FolderPlanLine{Kind: kind}
	switch p.State {
	case personalassistant.FolderInstallInstall:
		line.Name = "Installs and enables " + subject
		line.Detail = reviewedSource(p.Source)
	case personalassistant.FolderInstallEnable:
		line.Name = "Enables " + subject
		line.Detail = "It is already installed."
	case personalassistant.FolderInstallUpdate:
		line.Name = "Updates the reviewed " + p.Name + " " + noun + " from " + p.InstalledVersion + " to " + p.Version
		line.Detail = reviewedSource(p.Source)
	default:
		line.Name = "Uses the installed reviewed " + p.Name + " " + noun
		line.Detail = "Nothing is installed."
	}
	return line
}

func reviewedSource(source string) string {
	if strings.TrimSpace(source) == "" {
		return "Checked before it is enabled."
	}
	return "From " + strings.TrimSpace(source) + ", checked before it is enabled."
}
