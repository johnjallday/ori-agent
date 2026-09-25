package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestFreshJourneyProjectRefusesHistoricalAndUnfinishedRuns(t *testing.T) {
	accepted := time.Now().UTC()
	completed := accepted.Add(time.Minute)
	projection := &setupjourney.JourneyProjection{
		RunID: "new-run", Journey: setupjourney.DeclarationProjection{Source: setupjourney.QuestSourcePlugin},
		Lifecycle: setupjourney.LifecycleReady, FirstCompletedAt: &completed,
		Receipts: setupjourney.ResourceProjection{ProjectWorkspaceID: "created"},
	}
	if !freshJourneyProject(projection, "new-run", accepted) {
		t.Fatal("ready plugin quest was not accepted")
	}
	before := accepted.Add(-time.Minute)
	for name, mutate := range map[string]func(*setupjourney.JourneyProjection){
		"old ready run": func(p *setupjourney.JourneyProjection) { p.FirstCompletedAt = &before },
		"wrong run":     func(p *setupjourney.JourneyProjection) { p.RunID = "other" },
		"host quest":    func(p *setupjourney.JourneyProjection) { p.Journey.Source = setupjourney.QuestSourceHost },
		"unfinished":    func(p *setupjourney.JourneyProjection) { p.Lifecycle = setupjourney.LifecycleInProgress },
		"no project":    func(p *setupjourney.JourneyProjection) { p.Receipts.ProjectWorkspaceID = "" },
	} {
		copy := *projection
		mutate(&copy)
		if freshJourneyProject(&copy, "new-run", accepted) {
			t.Errorf("%s accepted as new project", name)
		}
	}
}

func TestJourneyProjectMatchesOnlyTheShownFolderAndReviewedBlueprint(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Album"})
	project.OwnerUserID = "local"
	project.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "plugin:reaper-plugin:reaper-song", PluginOwner: &workspace.PluginTemplateOwner{PluginID: "reaper-plugin", BlueprintID: "reaper-song"}})
	project.SharedData = map[string]any{}
	if err := project.AddDirectoryReference(workspace.DirectoryReference{ID: "chosen", Name: "Album", Path: root}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SetProjectEntryLocator(project.SharedData, workspace.ProjectEntryLocator{
		SchemaVersion: workspace.ProjectEntryLocatorSchemaVersion,
		Kind:          workspace.ProjectEntryDirectoryReference, DirectoryReferenceID: "chosen",
		RelativePath: "Song.rpp",
	}); err != nil {
		t.Fatal(err)
	}
	matches := func(owner, plugin, blueprint, path string) bool {
		return journeyProjectMatchesFolder(project, owner, plugin, blueprint, path)
	}
	if !matches("local", "reaper-plugin", "reaper-song", filepath.Join(root, ".")) {
		t.Fatal("canonical project connection was not accepted")
	}
	for _, tc := range []struct{ owner, plugin, blueprint, path string }{
		{"other", "reaper-plugin", "reaper-song", root},
		{"local", "other-plugin", "reaper-song", root},
		{"local", "reaper-plugin", "other-blueprint", root},
		{"local", "reaper-plugin", "reaper-song", other},
	} {
		if matches(tc.owner, tc.plugin, tc.blueprint, tc.path) {
			t.Errorf("accepted foreign journey project: %+v", tc)
		}
	}
	if err := workspace.SetProjectEntryLocator(project.SharedData, workspace.ProjectEntryLocator{
		SchemaVersion: workspace.ProjectEntryLocatorSchemaVersion,
		Kind:          workspace.ProjectEntryManagedWorkspace, RelativePath: "Song.rpp",
	}); err != nil {
		t.Fatal(err)
	}
	if matches("local", "reaper-plugin", "reaper-song", root) {
		t.Fatal("managed project was not attached to the shown folder")
	}
}
