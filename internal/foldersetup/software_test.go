package foldersetup

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func softwareFacts() SoftwareFacts {
	return SoftwareFacts{
		Recipe: "software_project", RecipeVersion: 1,
		ProjectName: "widget", WorkspaceName: "widget", BlueprintLabel: "Code project",
		Template: "code-project", TemplateRevision: "rev-template",
		SubjectKey: strings.Repeat("a", 64), SubjectIdentity: "dev:1:ino:2",
		Assistant: SoftwareAssistant{
			Name: "Development Assistant", Action: personalassistant.FolderSoftwareCreate,
			Provider: "codex", Model: "some-model", RosterRevision: "rev-roster",
		},
		Guides: []string{"AGENTS.md"}, EvidenceRevision: strings.Repeat("b", 32),
		Workflow: "software_project/planner/step_through/documents_in_ori",
	}
}

func TestBuildSoftwarePlanListsEveryConsequenceAndInstallsNothing(t *testing.T) {
	plan := BuildSoftwarePlan(softwareFacts())
	if got := kindsOf(plan); got != "workspace,folder,agents,mode,task" {
		t.Fatalf("lines = %s", got)
	}
	for _, absent := range []string{personalassistant.FolderPlanIntegration, personalassistant.FolderPlanProvider} {
		if line(plan, absent).Kind != "" {
			t.Errorf("a software plan lists a %s line", absent)
		}
	}
	if got := line(plan, personalassistant.FolderPlanWorkspace).Name; got != "Creates a Code project workspace named widget" {
		t.Errorf("workspace line = %q", got)
	}
	if got := line(plan, personalassistant.FolderPlanFolder); got.Name != "Links widget where it is" || strings.Contains(got.Detail, "worktrees") {
		t.Errorf("folder line = %+v", got)
	}
	if got := line(plan, personalassistant.FolderPlanAgents); got.Name != "Adds one development assistant, Development Assistant" ||
		got.Detail != "It uses some-model through codex." {
		t.Errorf("agents line = %+v", got)
	}
	if got := line(plan, personalassistant.FolderPlanMode).Detail; !strings.Contains(got, "runs no code") || !strings.HasSuffix(got, "Its instructions were found in AGENTS.md.") {
		t.Errorf("mode detail = %q", got)
	}
	if got := line(plan, personalassistant.FolderPlanTask); !strings.Contains(got.Name, "for you to start") || !strings.Contains(got.Detail, "only then") {
		t.Errorf("task line = %+v", got)
	}

	intent := plan.Intent
	if intent.Recipe != "software_project" || intent.RecipeVersion != 1 || intent.Placement != "standalone" || intent.Software == nil {
		t.Fatalf("intent = %+v", intent)
	}
	if intent.Integration != "" || intent.IntegrationPlugin != "" || intent.Provider != "" || intent.ProviderPlugin != "" {
		t.Errorf("a software intent names something to install: %+v", intent)
	}
	software := *intent.Software
	if err := software.Validate(); err != nil {
		t.Fatalf("the intent the builder makes is not valid: %v", err)
	}
	if software.Outcome != personalassistant.FolderSoftwareCreate || software.Assessment != personalassistant.FolderSoftwareSeed ||
		software.Scope != personalassistant.FolderSoftwareScopeReadOnly || !software.Planning ||
		software.AssistantAction != personalassistant.FolderSoftwareCreate || software.RosterRevision != "rev-roster" ||
		software.EvidenceRevision != strings.Repeat("b", 32) || software.TemplateRevision != "rev-template" {
		t.Errorf("software intent = %+v", software)
	}
	if plan.Digest == "" {
		t.Fatal("the plan carries no digest")
	}
}

func TestBuildSoftwarePlanSaysWhatItCannotPromise(t *testing.T) {
	facts := softwareFacts()
	facts.Assistant.Provider, facts.Assistant.Model = "", ""
	facts.Linked = true
	facts.Guides = nil
	plan := BuildSoftwarePlan(facts)
	if got := line(plan, personalassistant.FolderPlanAgents).Detail; got != "It has no model yet. Setup pauses, before anything is created, for you to choose one." {
		t.Errorf("agents detail without a model = %q", got)
	}
	if got := line(plan, personalassistant.FolderPlanFolder).Detail; !strings.HasSuffix(got, "other worktrees of the same repository are not included.") {
		t.Errorf("a linked checkout's folder detail = %q", got)
	}
	if got := line(plan, personalassistant.FolderPlanMode).Detail; strings.Contains(got, "instructions were found") {
		t.Errorf("mode detail names instructions that were not found: %q", got)
	}

	saved := softwareFacts()
	saved.Assistant = SoftwareAssistant{Name: "Pat", Action: personalassistant.FolderSoftwareReuse, Model: "their-model"}
	got := line(BuildSoftwarePlan(saved), personalassistant.FolderPlanAgents)
	if got.Name != "Uses your saved agent Pat as the development assistant" ||
		got.Detail != "Its saved prompt, model and tools are used exactly as they are. It uses their-model." {
		t.Errorf("a saved agent's line = %+v", got)
	}
}

func TestBuildSoftwarePlanCompletesAnExistingWorkspaceWithoutReplacingAnything(t *testing.T) {
	facts := softwareFacts()
	facts.Existing = &SoftwareExisting{WorkspaceID: "ws-1", Name: "Old widget", EntryAgent: "Robin", HasFirstLook: true}
	plan := BuildSoftwarePlan(facts)
	if got := kindsOf(plan); got != "workspace,folder,agents,mode,task" {
		t.Fatalf("lines = %s", got)
	}
	for kind, want := range map[string]string{
		personalassistant.FolderPlanWorkspace: "Uses your existing workspace Old widget",
		personalassistant.FolderPlanFolder:    "Keeps widget linked where it is",
		personalassistant.FolderPlanAgents:    "Keeps Robin as this workspace's assistant",
		personalassistant.FolderPlanMode:      "Keeps this workspace's settings as they are",
		personalassistant.FolderPlanTask:      "Keeps the first look this workspace already has",
	} {
		if got := line(plan, kind).Name; got != want {
			t.Errorf("%s line = %q, want %q", kind, got, want)
		}
	}
	software := *plan.Intent.Software
	if err := software.Validate(); err != nil {
		t.Fatalf("the reuse intent is not valid: %v", err)
	}
	if software.Outcome != personalassistant.FolderSoftwareReuse || software.WorkspaceID != "ws-1" ||
		software.Workflow != personalassistant.FolderSoftwareKeep ||
		software.WorkspaceName != "Old widget" || software.Template != "" || software.TemplateRevision != "" ||
		software.Assistant != "Robin" || software.AssistantAction != personalassistant.FolderSoftwareKeep ||
		software.Model != "" || software.RosterRevision != "" || software.Assessment != personalassistant.FolderSoftwareKeep {
		t.Errorf("reuse intent = %+v", software)
	}

	// A workspace with no entry agent and no first look gets both, and says so.
	facts.Existing = &SoftwareExisting{WorkspaceID: "ws-1", Name: "Old widget"}
	bare := BuildSoftwarePlan(facts)
	if got := line(bare, personalassistant.FolderPlanAgents).Name; got != "Adds one development assistant, Development Assistant" {
		t.Errorf("agents line for a workspace with no assistant = %q", got)
	}
	if got := bare.Intent.Software; got.AssistantAction != personalassistant.FolderSoftwareCreate || got.Assessment != personalassistant.FolderSoftwareSeed {
		t.Errorf("intent for a bare workspace = %+v", got)
	}
}

// Consent is to the promise, not to its wording: what the run is held to moves
// the digest even when every line reads the same, and a status row never does.
func TestBuildSoftwarePlanDigestFollowsThePromiseAndNotItsStatusRows(t *testing.T) {
	base := BuildSoftwarePlan(softwareFacts())
	for name, change := range map[string]func(*SoftwareFacts){
		"roster revision":   func(f *SoftwareFacts) { f.Assistant.RosterRevision = "rev-roster-2" },
		"evidence revision": func(f *SoftwareFacts) { f.EvidenceRevision = strings.Repeat("c", 32) },
		"template revision": func(f *SoftwareFacts) { f.TemplateRevision = "rev-template-2" },
		"directory":         func(f *SoftwareFacts) { f.SubjectIdentity = "dev:1:ino:3" },
		"conversation":      func(f *SoftwareFacts) { f.Conversation = "conversation-2" },
		"workflow":          func(f *SoftwareFacts) { f.Workflow = "software_project/autonomous/auto/documents_in_ori" },
	} {
		facts := softwareFacts()
		change(&facts)
		moved := BuildSoftwarePlan(facts)
		if moved.Digest == base.Digest {
			t.Errorf("a changed %s left the digest alone", name)
		}
		if len(moved.Lines) != len(base.Lines) {
			t.Fatalf("a changed %s changed the line count", name)
		}
		for i := range moved.Lines {
			if moved.Lines[i].Name != base.Lines[i].Name || moved.Lines[i].Detail != base.Lines[i].Detail {
				t.Errorf("a changed %s reworded line %d, so the test proves nothing", name, i)
			}
		}
	}

	facts := softwareFacts()
	facts.Prerequisites = []personalassistant.FolderPrerequisite{{ID: "github", Label: "GitHub", State: "absent"}}
	withRows := BuildSoftwarePlan(facts)
	if withRows.Digest != base.Digest {
		t.Error("an optional connection's status moved the digest")
	}
	if len(withRows.Prerequisites) != 1 || len(base.Prerequisites) != 0 {
		t.Errorf("prerequisites = %d and %d", len(withRows.Prerequisites), len(base.Prerequisites))
	}
}
