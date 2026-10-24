package foldersetup

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// InboxFacts are what an inbox folder's setup plan is built from: the host's
// built-in recipe, the folder and workspace names, and the reviewed File
// Janitor team plan. The host reads every field from canonical state; the
// builder only words them.
type InboxFacts struct {
	Recipe        string
	RecipeVersion int
	// BlueprintLabel names the workspace blueprint ("File Janitor").
	BlueprintLabel string
	// Intent is the promise the run is held to, the agent's create or reuse
	// included.
	Intent personalassistant.FolderInboxIntent
}

// Inbox plan lines, in the order the card shows them and the run reports them.
const (
	InboxFolderLine = "Linked where it is"
	// InboxFolderDetail is the blueprint's access disclosure, shortened but
	// never weakened: what is read, what is created, and that nothing moves
	// without approval.
	InboxFolderDetail    = "Ori lists the files directly inside it and reads their names, types, sizes and dates. It creates a Filed folder inside it. Nothing moves until you approve it in the review."
	InboxFirstLookLine   = "A first look: a metadata-only scan that proposes the first filing batch"
	InboxFirstLookDetail = "No model is called, and no file is opened, moved or deleted. You review the batch in File Janitor."
)

// BuildInboxPlan lists every consequence of Hire and start for an inbox folder
// and the machine-readable promise the run is held to. Nothing is installed
// and no model is needed: the requirements say so, and the plan carries no
// model witness to wait on. Every string is plain text and names no path.
func BuildInboxPlan(facts InboxFacts) personalassistant.FolderSetupPlan {
	intent := facts.Intent
	agent := strings.TrimSpace(intent.Agent.Name)
	agents := personalassistant.FolderPlanLine{
		Kind: personalassistant.FolderPlanAgents, Name: "Hire " + agent,
		Detail: "A new agent from the " + facts.BlueprintLabel + " blueprint. It answers questions in the workspace; the first batch does not need it.",
	}
	if intent.Agent.Action == personalassistant.FolderSoftwareReuse {
		agents = personalassistant.FolderPlanLine{
			Kind: personalassistant.FolderPlanAgents, Name: "Assign " + agent,
			Detail: "Your saved agent joins the workspace exactly as it is saved. No second " + agent + " is made.",
		}
	}
	lines := []personalassistant.FolderPlanLine{
		agents,
		{
			Kind: personalassistant.FolderPlanWorkspace, Name: "A " + facts.BlueprintLabel + " workspace named " + intent.WorkspaceName,
			Detail: "It lives in Ori as a separate workspace, in no Home.",
		},
		{Kind: personalassistant.FolderPlanFolder, Name: InboxFolderLine, Detail: InboxFolderDetail},
		{Kind: personalassistant.FolderPlanTask, Name: InboxFirstLookLine, Detail: InboxFirstLookDetail},
	}
	plan := personalassistant.NewFolderSetupPlan(lines)
	promise := intent
	plan.Intent = personalassistant.FolderSetupIntent{
		Placement: "standalone", Recipe: facts.Recipe, RecipeVersion: facts.RecipeVersion, Inbox: &promise,
	}
	plan.Visual = &personalassistant.FolderSetupVisual{
		Entity: &personalassistant.FolderSetupEntity{Kind: "workspace", Name: intent.WorkspaceName, Operation: personalassistant.FolderSoftwareCreate},
		// Nothing to download, and no model to configure for the first batch.
		RequirementsChecked: true, Builtin: facts.BlueprintLabel,
		Model: &personalassistant.FolderSetupModel{State: personalassistant.FolderSetupModelNotNeeded},
	}
	return plan.Stamped()
}
