package foldersetup

import (
	"fmt"
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
	// Grouped says the workspace joins a Home (a blueprint that requires one, or
	// the user already has one); HomeExists says that Home is already there, so
	// nothing is created for it. HomeTemplate is the exact template a new Home is
	// created from.
	Grouped      bool
	HomeExists   bool
	HomeTemplate string
	// SharingOff says the existing Home's shared assistant was switched off;
	// Set up switches it back on, so the plan says so.
	SharingOff bool
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
	workspaceName := "Creates a " + facts.BlueprintLabel + " workspace named " + facts.WorkspaceName
	intent.Placement = "standalone"
	if facts.Grouped {
		intent.Placement = "grouped"
		intent.HomeTemplate = facts.HomeTemplate
		if facts.HomeExists {
			workspaceName += " in your Home"
		} else {
			intent.CreatesHome = true
			lines = append(lines, personalassistant.FolderPlanLine{
				Kind: personalassistant.FolderPlanHome, Name: "Creates the Home this workspace belongs to",
				Detail: "Your later projects of this kind join it.",
			})
			workspaceName += " in that Home"
		}
	}
	mode := personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanMode, Name: "Uses File-only mode"}
	if facts.AppInstalled {
		mode.Detail = "Ori does not control " + facts.AppName + "."
	} else {
		// One honest line: nothing here needs the application, and none of it
		// promises a live connection.
		mode.Detail = facts.AppName + " itself is not installed here; File-only mode works without it."
	}
	agents := personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanAgents, Name: "Adds the agents this blueprint requires"}
	if facts.Grouped {
		// D9: in a Home the project assistant is hired once and shared, and this
		// Set up is the consent that covers the later projects too.
		later := "the later " + pluralLabel(facts.BlueprintLabel) + " you open in this Home"
		agents.Detail = "Its assistant is shared: the same one joins " + later + "."
		if facts.SharingOff {
			agents.Detail = "Its assistant is shared, and this turns adding it to " + later + " back on."
		}
	}
	lines = append(lines,
		personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanWorkspace, Name: workspaceName},
		personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanFolder, Name: "Links " + facts.WorkspaceName + " where it is", Detail: "Nothing is moved or copied."},
		mode,
		agents,
		personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanTask, Name: "Queues a first read-only task for when you open it"},
	)
	plan := personalassistant.NewFolderSetupPlan(lines)
	plan.Intent = intent
	return plan
}

// PortfolioFacts are what a collection's plan is built from, read by the host
// from canonical state.
type PortfolioFacts struct {
	// FolderName is the collection folder's name; Projects how many project
	// folders the scan saw in it, and CollectionNoun what to call them.
	FolderName     string
	Projects       int
	CollectionNoun string
	// HomeName names the Home; HomeExists says it is already there and
	// HomeTemplate is the exact template a new Home is created from.
	HomeName     string
	HomeExists   bool
	HomeTemplate string
	// HomeStaffed says the Home's required roles are already filled.
	HomeStaffed bool
	Provider    Plugin
	// Integration is the reviewed integration some projects need; nil when no
	// project in the collection is one it can set up (IntegrationProjects == 0).
	Integration         *Plugin
	IntegrationProjects int
	AppName             string
	// ProjectLabel is the project blueprint's name for one project.
	ProjectLabel string
	// AssistantName is the shared assistant's role label when the installed
	// blueprint declares it; empty before the integration is installed.
	AssistantName string
	// Sharing is the Home's consent for the shared assistant: "" (none), "on" or
	// "off" (switched off on the Home).
	Sharing string
	// SongDetails is the existing Home's song-details consent: "" (none), "on"
	// or "off". A Home the plan creates gets it; an existing Home never does.
	SongDetails string
}

// Sharing states of an existing Home's consent.
const (
	SharingOn  = "on"
	SharingOff = "off"
)

// SongDetailsOn is an existing Home whose song-details switch is on.
const SongDetailsOn = "on"

// The library line's detail: today's, and the one a Home that reads song facts
// shows (libraryDetailSongDetails). Each is part of the plan digest, so a card
// showing the other is refused as plan_changed.
const libraryDetailNamesOnly = "Names and project files only. Nothing is opened, moved or copied."

// libraryDetailSongDetails names the application whose project files are read
// ("Reads each <app> project's tempo, …"), as the card names it elsewhere.
func libraryDetailSongDetails(appName string) string {
	project := "project"
	if app := strings.TrimSpace(appName); app != "" {
		project = app + " project"
	}
	return "Reads each " + project + "'s tempo, length and track count. Nothing is moved, copied or changed."
}

// BuildPortfolioPlan lists every consequence of Set up on a collection of
// projects, in the order the card shows them, and the intent a run is held to.
// Listing creates no project workspace (D4); a project gets one when it is opened.
func BuildPortfolioPlan(facts PortfolioFacts) personalassistant.FolderSetupPlan {
	intent := personalassistant.FolderSetupIntent{Portfolio: true, Placement: "grouped", HomeTemplate: facts.HomeTemplate}
	provider := pluginLine(personalassistant.FolderPlanProvider, "plugin", facts.Provider)
	if provider.Detail == "Nothing is installed." || provider.Detail == "" {
		provider.Detail = "It provides the Home these projects are listed in."
	}
	lines := []personalassistant.FolderPlanLine{provider}
	intent.Provider, intent.ProviderPlugin, intent.ProviderVersion = facts.Provider.State, facts.Provider.PluginID, facts.Provider.Version
	shared := facts.Integration != nil && facts.IntegrationProjects > 0
	intent.SharedProjects = shared
	if shared {
		line := pluginLine(personalassistant.FolderPlanIntegration, "integration", *facts.Integration)
		line.Detail = strings.TrimSpace(line.Detail + " " + countPhrase(facts.IntegrationProjects, facts.ProjectLabel) + " can use it.")
		lines = append(lines, line)
		intent.Integration, intent.IntegrationPlugin, intent.IntegrationVersion =
			facts.Integration.State, facts.Integration.PluginID, facts.Integration.Version
	}
	if !facts.HomeExists {
		intent.CreatesHome = true
		lines = append(lines, personalassistant.FolderPlanLine{
			Kind: personalassistant.FolderPlanHome, Name: "Creates your " + facts.HomeName,
			Detail: "Your later projects of this kind join it.",
		})
	}
	if !facts.HomeExists || !facts.HomeStaffed {
		intent.StaffsHome = true
		lines = append(lines, personalassistant.FolderPlanLine{
			Kind: personalassistant.FolderPlanAgents, Name: "Adds the agents the Home requires",
			Detail: "They keep track of the whole collection.",
		})
	}
	noun := strings.TrimSpace(facts.CollectionNoun)
	if noun == "" {
		noun = "projects"
	}
	// T6: a Home this Set up creates reads song facts (its consent is this
	// card); an existing Home reads them only when its own switch is on.
	intent.GrantsSongDetails = !facts.HomeExists
	intent.ReadsSongDetails = !facts.HomeExists || facts.SongDetails == SongDetailsOn
	library := libraryDetailNamesOnly
	if intent.ReadsSongDetails {
		library = libraryDetailSongDetails(facts.AppName)
	}
	lines = append(lines, personalassistant.FolderPlanLine{
		Kind:   personalassistant.FolderPlanLibrary,
		Name:   fmt.Sprintf("Lists the %d %s in %s", facts.Projects, noun, facts.FolderName),
		Detail: library,
	})
	if shared {
		one := strings.TrimSpace(facts.ProjectLabel)
		lines = append(lines, personalassistant.FolderPlanLine{
			Kind: personalassistant.FolderPlanSongs, Name: "A " + one + " gets its workspace the first time you open it",
			Detail: "Listing them makes no workspace.",
		})
		if facts.Sharing != SharingOn {
			intent.GrantsConsent = true
			assistant := "Your project assistant"
			if name := strings.TrimSpace(facts.AssistantName); name != "" {
				assistant = "Your " + name
			}
			detail := "File-only: Ori does not control " + facts.AppName + ". You can turn this off on the Home."
			if facts.Sharing == SharingOff {
				detail = "This turns it back on. File-only: Ori does not control " + facts.AppName + "."
			}
			lines = append(lines, personalassistant.FolderPlanLine{
				Kind: personalassistant.FolderPlanAssistant, Name: assistant + " joins each " + one + " you open",
				Detail: detail,
			})
		}
	} else {
		lines = append(lines, personalassistant.FolderPlanLine{
			Kind: personalassistant.FolderPlanSongs, Name: "No workspace or agent is made for these projects",
			Detail: "They are listed in the Home only.",
		})
	}
	plan := personalassistant.NewFolderSetupPlan(lines)
	plan.Intent = intent
	return plan
}

// countPhrase is "1 Studio song" or "200 Studio songs" for a blueprint label.
func countPhrase(n int, label string) string {
	if n == 1 {
		return "1 " + strings.TrimSpace(label)
	}
	return fmt.Sprintf("%d %s", n, pluralLabel(label))
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

// pluralLabel names more than one of a blueprint's projects ("Studio songs").
func pluralLabel(label string) string {
	label = strings.TrimSpace(label)
	switch {
	case label == "":
		return "projects"
	case strings.HasSuffix(label, "s"):
		return label
	default:
		return label + "s"
	}
}

func reviewedSource(source string) string {
	if strings.TrimSpace(source) == "" {
		return "Checked before it is enabled."
	}
	return "From " + strings.TrimSpace(source) + ", checked before it is enabled."
}
