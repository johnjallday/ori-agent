package settingsreset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

// seedWorkspaceDirectoryExtras writes a Skills folder (one skill, one other
// file) and a plugin list into the fixture's Workspace Directory.
func seedWorkspaceDirectoryExtras(t *testing.T, f *resetfixture.Fixture) (skills, list string) {
	t.Helper()
	workspaces, err := filepath.Rel(f.Paths().Root, f.Paths().Workspaces)
	mustPreview(t, err)
	for relative, content := range map[string]string{
		filepath.Join(workspaces, "Skills", "find-skills", "SKILL.md"):               "---\nname: find-skills\n---\n",
		filepath.Join(workspaces, "Skills", "find-skills", ".ori-skill-source.json"): "{}",
		filepath.Join(workspaces, "Skills", "NOTES.txt"):                             "mine",
		filepath.Join(workspaces, "Plugins.json"):                                    `{"schema_version":1,"plugins":[],"removed":[]}`,
	} {
		mustPreview(t, f.WriteFile(relative, []byte(content)))
	}
	skills, err = resolvePath(filepath.Join(f.Paths().Workspaces, "Skills"))
	mustPreview(t, err)
	list, err = resolvePath(filepath.Join(f.Paths().Workspaces, "Plugins.json"))
	mustPreview(t, err)
	return skills, list
}

func prepareStartFresh(t *testing.T, f *resetfixture.Fixture, owners *Owners) {
	t.Helper()
	root := f.Paths().DataDir
	mustPreview(t, owners.Config.SetTemplatesRoot(filepath.Join(root, "templates")))
	mustPreview(t, owners.Config.Save())
	t.Setenv("ORI_TEMPLATES_DIR", filepath.Join(root, "templates"))
	t.Setenv("WORKFLOW_TEMPLATES_DIR", filepath.Join(root, "workflow_templates"))
	owners.FreshTargets = fixtureFreshTargets(root)
}

// FR 42: Start Fresh names the Skills folder and the plugin list beside the
// agents folder, under the same second confirmation; a selected Agents reset
// leaves both alone.
func TestStartFreshNamesTheSkillsFolderAndPluginListWithTheAgentsFolder(t *testing.T) {
	f, owners, c, _ := coordinatorFixture(t)
	prepareStartFresh(t, f, owners)
	skills, list := seedWorkspaceDirectoryExtras(t, f)

	preview, err := c.planner.Create(t.Context(), IntentStartFresh, nil)
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("blockers = %+v", preview.Blockers)
	}
	review := preview.AgentsFolder
	if review == nil || !slices.Equal(review.AlsoRemoved, []string{skills, list}) || review.Notice != AgentsFolderFreshNotice {
		t.Fatalf("agents folder review = %+v", review)
	}
	var removed []string
	for _, category := range preview.Categories {
		for _, location := range category.Removed {
			removed = append(removed, location.DisplayPath)
		}
	}
	if !slices.Contains(removed, skills) || !slices.Contains(removed, list) {
		t.Fatalf("removed locations = %v", removed)
	}

	selected, err := c.planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAgents})
	mustPreview(t, err)
	if selected.AgentsFolder == nil || len(selected.AgentsFolder.AlsoRemoved) != 0 || selected.AgentsFolder.Notice != AgentsFolderNotice {
		t.Fatalf("a selected Agents reset reaches beyond the agents folder: %+v", selected.AgentsFolder)
	}

	// The second confirmation still names only the agents folder, and without
	// it nothing is staged.
	request := ExecuteRequest{PreviewID: preview.ID, RequestID: "fresh-unconfirmed", Confirmation: "RESET"}
	if _, err := c.Stage(t.Context(), request); !errors.Is(err, ErrAgentsFolderUnconfirmed) {
		t.Fatalf("Stage without the second confirmation = %v", err)
	}
}

func TestStartFreshRemovesSkillsAndThePluginListButKeepsOtherFiles(t *testing.T) {
	f, owners, c, lifecycle := coordinatorFixture(t)
	prepareStartFresh(t, f, owners)
	skills, list := seedWorkspaceDirectoryExtras(t, f)
	root := f.Paths().DataDir

	preview, err := c.planner.Create(t.Context(), IntentStartFresh, nil)
	mustPreview(t, err)
	lifecycle.drain = func(context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}
	if _, err := c.Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: "fresh-skills", Confirmation: "RESET", ConfirmAgentsFolder: confirmedAgentsFolder(preview),
	}); err != nil {
		t.Fatalf("Stage = %v", err)
	}
	if _, err := os.Stat(filepath.Join(skills, "find-skills", "SKILL.md")); err != nil {
		t.Fatal("the accepting process already removed the skill")
	}
	if err := RecoverBeforeStores(t.Context(), c.lease, RecoveryOptions{DataDir: root, SecretStore: f.Secrets()}); err != nil {
		failed, _ := c.Status(t.Context(), preview.OperationID)
		t.Fatalf("recover Start Fresh: %v: %+v", err, failed)
	}
	operation, err := c.Status(t.Context(), preview.OperationID)
	mustPreview(t, err)
	if !operation.VerifiedComplete() {
		t.Fatalf("Start Fresh did not verify complete: %+v", operation)
	}
	if _, err := os.Stat(filepath.Join(skills, "find-skills")); !os.IsNotExist(err) {
		t.Errorf("the skill survived Start Fresh: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(skills, "NOTES.txt")); err != nil || string(data) != "mine" {
		t.Errorf("an unrelated file in Skills was not kept: %q %v", data, err)
	}
	if _, err := os.Stat(list); !os.IsNotExist(err) {
		t.Errorf("the plugin list survived Start Fresh: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.Paths().Workspaces, "fixture-project", "workspace.json")); err != nil {
		t.Errorf("the retained workspace was touched: %v", err)
	}
}

// FR 41: a plugin reset makes this machine fresh; it never changes the list
// of plugins the Workspace Directory wants.
func TestAPluginResetNeverChangesThePluginList(t *testing.T) {
	f, _, c, _ := pluginFixture(t, demoSeeds()...)
	_, list := seedWorkspaceDirectoryExtras(t, f)
	before, err := os.ReadFile(list)
	mustPreview(t, err)
	stagePluginReset(t, c)
	mustPreview(t, RecoverBeforeStores(t.Context(), c.lease, recoveryOptions(f)))
	after, err := os.ReadFile(list)
	if err != nil || string(after) != string(before) {
		t.Fatalf("a plugin reset changed Plugins.json: %q %v", after, err)
	}
}

func TestAJournalCannotPointTheSkillsFolderElsewhere(t *testing.T) {
	agentsOnly := []CategoryID{CategoryAgents}
	folder := filepath.Join(compatRoot, "retained-projects", "Agents")
	skills, list := siblingFreshTargets(folder)

	// Older receipts name neither and keep validating.
	if err := validateJournal(agentsJournal(agentsOnly, folder, true)); err != nil {
		t.Fatalf("a receipt without Skills evidence was refused: %v", err)
	}
	// Selected-data receipts may not carry them at all.
	withSkills := agentsJournal(agentsOnly, folder, true)
	withSkills.Plan.Evidence.SkillsFolder = skills
	if err := validateJournal(withSkills); !errors.Is(err, ErrJournalInvalid) {
		t.Errorf("a selected Agents receipt with a Skills folder = %v", err)
	}
	for name, evidence := range map[string]resolvedEvidence{
		"skills elsewhere": {AgentsFolder: folder, SkillsFolder: filepath.Join(compatRoot, "Skills")},
		"list elsewhere":   {AgentsFolder: folder, PluginList: filepath.Join(compatRoot, "Plugins.json")},
		"no agents folder": {SkillsFolder: skills},
	} {
		if validFreshSiblings(evidence) {
			t.Errorf("%s was accepted", name)
		}
	}
	if !validFreshSiblings(resolvedEvidence{AgentsFolder: folder, SkillsFolder: skills, PluginList: list}) {
		t.Error("the siblings of the reviewed agents folder were refused")
	}
}

func TestRetainedDigestsSkipTheSkillsFolderAndPluginList(t *testing.T) {
	root := t.TempDir()
	skills := filepath.Join(root, "Skills")
	list := filepath.Join(root, "Plugins.json")
	mustPreview(t, os.MkdirAll(filepath.Join(skills, "find-skills"), 0o750))
	mustPreview(t, os.WriteFile(list, []byte("{}"), 0o600))
	mustPreview(t, os.WriteFile(filepath.Join(root, "workspace.json"), []byte("a"), 0o600))
	evidence := resolvedEvidence{AgentsFolder: filepath.Join(root, "Agents"), SkillsFolder: skills, PluginList: list}
	before, err := evidence.digestRetained(t.Context(), root)
	mustPreview(t, err)
	// Start Fresh removes each skill folder (Skills itself stays) and the list.
	mustPreview(t, os.RemoveAll(filepath.Join(skills, "find-skills")))
	mustPreview(t, os.Remove(list))
	if after, _ := evidence.digestRetained(t.Context(), root); after != before {
		t.Error("removing the skills and the plugin list changed the retained digest")
	}
	mustPreview(t, os.WriteFile(filepath.Join(root, "workspace.json"), []byte("b"), 0o600))
	if after, _ := evidence.digestRetained(t.Context(), root); after == before {
		t.Error("a change outside what Start Fresh removes was not detected")
	}
	// Without a skipped file, the digest is exactly the older one.
	older, _ := digestProtectedPath(t.Context(), root, evidence.AgentsFolder)
	agentsOnly := resolvedEvidence{AgentsFolder: evidence.AgentsFolder}
	if same, _ := agentsOnly.digestRetained(t.Context(), root); same != older {
		t.Error("an agents-only receipt no longer digests the way it did")
	}
}
