package foldersetup

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// SoftwareFacts are what a software project's setup plan is built from. The
// host reads every field from canonical state: the selection the server holds,
// the workspace store, the installed blueprint and its roster plan, and the
// stored review. The builder only words them; it decides nothing.
type SoftwareFacts struct {
	Recipe        string
	RecipeVersion int
	// ProjectName is the folder's own name; WorkspaceName is the name the
	// workspace will have (the suggestion, or the user's change to it).
	ProjectName   string
	WorkspaceName string
	// BlueprintLabel names the workspace blueprint ("Code project").
	BlueprintLabel   string
	Template         string
	TemplateRevision string
	// SubjectKey and SubjectIdentity witness the one directory under review.
	SubjectKey      string
	SubjectIdentity string
	// Linked says the folder's version-control marker is a file: one checkout
	// of a repository that has others.
	Linked bool
	// Existing is the user's workspace already linked to this exact folder some
	// other way, which the setup completes instead of creating another.
	Existing *SoftwareExisting
	// Assistant is the development assistant the roster plan resolves to.
	Assistant SoftwareAssistant
	// Guides are the project's own instruction files, as an inspection found
	// them; EvidenceRevision is that inspection.
	Guides           []string
	EvidenceRevision string
	Destination      *personalassistant.FolderSetupDestination
	Conversation     string
	Prerequisites    []personalassistant.FolderPrerequisite
}

// SoftwareExisting is a workspace the setup would reuse, as it stands now.
type SoftwareExisting struct {
	WorkspaceID string
	Name        string
	Version     int64
	// EntryAgent is its entry agent, empty when it has none.
	EntryAgent string
	// HasFirstLook says it already has a first look task, which is kept.
	HasFirstLook bool
}

// SoftwareAssistant is the development assistant a plan promises.
type SoftwareAssistant struct {
	Name string
	// Action is create (a new saved agent) or reuse (a saved agent of that
	// name, used exactly as saved).
	Action         string
	Provider       string
	Model          string
	RosterRevision string
}

// BuildSoftwarePlan lists every consequence of Set up for one software project
// and the machine-readable promise the run is held to. There is no integration
// or provider line: nothing is installed. Every string is plain text and names
// no path.
func BuildSoftwarePlan(facts SoftwareFacts) personalassistant.FolderSetupPlan {
	software := &personalassistant.FolderSoftwareIntent{
		Outcome: personalassistant.FolderSoftwareCreate, WorkspaceName: facts.WorkspaceName,
		Template: facts.Template, TemplateRevision: facts.TemplateRevision,
		SubjectKey: facts.SubjectKey, SubjectIdentity: facts.SubjectIdentity,
		Assistant: facts.Assistant.Name, AssistantAction: facts.Assistant.Action,
		Provider: facts.Assistant.Provider, Model: facts.Assistant.Model, RosterRevision: facts.Assistant.RosterRevision,
		Assessment: personalassistant.FolderSoftwareSeed, EvidenceRevision: facts.EvidenceRevision,
		Scope: personalassistant.FolderSoftwareScopeReadOnly, Planning: true, Conversation: facts.Conversation,
	}

	workspace := personalassistant.FolderPlanLine{
		Kind: personalassistant.FolderPlanWorkspace, Name: "Creates a " + facts.BlueprintLabel + " workspace named " + facts.WorkspaceName,
		Detail: "The workspace lives in Ori. Nothing is created in the project folder.",
	}
	folder := personalassistant.FolderPlanLine{
		Kind: personalassistant.FolderPlanFolder, Name: "Links " + facts.ProjectName + " where it is",
		Detail: "Read-only. Nothing is moved, copied or written into it.",
	}
	if facts.Linked {
		folder.Detail += " This is one checkout; other worktrees of the same repository are not included."
	}
	agents := assistantLine(facts.Assistant)
	task := personalassistant.FolderPlanLine{
		Kind: personalassistant.FolderPlanTask, Name: "Queues a first assessment for you to start",
		Detail: "When you press Start it reads this project's README, instructions and build files and reports what it found. Model tokens are spent only then.",
	}

	if existing := facts.Existing; existing != nil {
		software.Outcome, software.WorkspaceID, software.WorkspaceVersion = personalassistant.FolderSoftwareReuse, existing.WorkspaceID, existing.Version
		software.WorkspaceName, software.Template, software.TemplateRevision = existing.Name, "", ""
		workspace = personalassistant.FolderPlanLine{
			Kind: personalassistant.FolderPlanWorkspace, Name: "Uses your existing workspace " + existing.Name,
			Detail: "No second workspace is made. Its notes, tasks and settings stay as they are.",
		}
		folder.Name = "Keeps " + facts.ProjectName + " linked where it is"
		if existing.EntryAgent != "" {
			software.Assistant, software.AssistantAction = existing.EntryAgent, personalassistant.FolderSoftwareKeep
			software.Provider, software.Model, software.RosterRevision = "", "", ""
			agents = personalassistant.FolderPlanLine{
				Kind: personalassistant.FolderPlanAgents, Name: "Keeps " + existing.EntryAgent + " as this workspace's assistant",
				Detail: "Its prompt, model and tools are not changed.",
			}
		}
		if existing.HasFirstLook {
			software.Assessment = personalassistant.FolderSoftwareKeep
			task = personalassistant.FolderPlanLine{
				Kind: personalassistant.FolderPlanTask, Name: "Keeps the first look this workspace already has",
				Detail: "Nothing is queued and nothing is started.",
			}
		}
	}

	mode := personalassistant.FolderPlanLine{
		Kind: personalassistant.FolderPlanMode, Name: "Starts with understanding and planning",
		Detail: "Setup runs no code, installs nothing and changes nothing in the project. The project's own conventions apply.",
	}
	if guides := joinAnd(facts.Guides); guides != "" {
		mode.Detail += " Its instructions were found in " + guides + "."
	}

	plan := personalassistant.NewFolderSetupPlan([]personalassistant.FolderPlanLine{workspace, folder, agents, mode, task})
	plan.Intent = personalassistant.FolderSetupIntent{
		Placement: "standalone", Recipe: facts.Recipe, RecipeVersion: facts.RecipeVersion, Software: software,
	}
	if facts.Destination != nil && facts.Destination.Status == "existing" {
		plan.Intent.Placement = "grouped"
	}
	plan.Destination = facts.Destination
	plan.Prerequisites = append([]personalassistant.FolderPrerequisite(nil), facts.Prerequisites...)
	return plan.Stamped()
}

// assistantLine words the assistant a new workspace gets. A saved agent of the
// same name is used as it is, and the line says so: its prompt, model and
// tools are never replaced by the blueprint's.
func assistantLine(assistant SoftwareAssistant) personalassistant.FolderPlanLine {
	if assistant.Action == personalassistant.FolderSoftwareReuse {
		return personalassistant.FolderPlanLine{
			Kind: personalassistant.FolderPlanAgents, Name: "Uses your saved agent " + assistant.Name + " as the development assistant",
			Detail: "Its saved prompt, model and tools are used exactly as they are." + modelSentence(assistant, " It"),
		}
	}
	return personalassistant.FolderPlanLine{
		Kind: personalassistant.FolderPlanAgents, Name: "Adds one development assistant, " + assistant.Name,
		Detail: strings.TrimSpace(modelSentence(assistant, "It")),
	}
}

func modelSentence(assistant SoftwareAssistant, subject string) string {
	model := strings.TrimSpace(assistant.Model)
	if model == "" {
		return subject + " has no model yet, so it cannot run until you choose one."
	}
	if provider := strings.TrimSpace(assistant.Provider); provider != "" {
		return subject + " uses " + model + " through " + provider + "."
	}
	return subject + " uses " + model + "."
}
