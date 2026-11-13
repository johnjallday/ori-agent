package server

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// assistant-reads-projects: the folder card names the Home's shared assistant
// from the app plugin's song blueprint, and the run hires it.

func songBlueprint(id string, resource bool) plugin.ResolvedBlueprint {
	project := &projecttemplates.AssistantProjectDeclaration{SchemaVersion: 1, Version: 1, ID: id + "-team",
		Roles: []projecttemplates.AssistantProjectRole{{ID: "app-assistant", Label: "App Assistant",
			Description: "Handles everything in the app for this project. It keeps going.", Primary: true, Role: "orchestrator",
			SystemPrompt: "SECRET PROMPT"}}}
	if resource {
		project.WorkspaceKind = workspace.AssistantWorkspaceKindResource
	}
	return plugin.ResolvedBlueprint{ID: id, Version: 1, Template: projecttemplates.Template{AssistantProject: project}}
}

func TestTeamFromBlueprints(t *testing.T) {
	blueprints := []plugin.ResolvedBlueprint{songBlueprint("studio", true), songBlueprint("song", false)}
	team, ok := teamFromBlueprints("app-plugin", "song", blueprints)
	if !ok || team.PluginID != "app-plugin" || team.BlueprintID != "song" || len(team.Roles) != 1 || team.Roles[0].ID != "app-assistant" {
		t.Fatalf("team = %+v ok=%v", team, ok)
	}
	if team.Digest != projecttemplates.AssistantProjectDigest(blueprints[1].Template.AssistantProject) || len(team.Digest) != 64 {
		t.Fatalf("digest = %q", team.Digest)
	}
	if _, ok := teamFromBlueprints("app-plugin", "studio", blueprints); ok {
		t.Fatal("a resource team was read as a song team")
	}
	if _, ok := teamFromBlueprints("app-plugin", "missing", blueprints); ok {
		t.Fatal("a missing blueprint gave a team")
	}
}

// The reviewed integration's installed copy names the assistant before the
// Home exists, and nothing is written to read it.
func TestFolderAgentAssistantFacts_FromTheInstalledAppPlugin(t *testing.T) {
	releases := hireReleases(t)
	b, files, plugins := hiresBuilder(t, releases)
	b.projectStaffing = projectstaffing.New(files, b.st)
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		t.Fatal("the reviewed integration fixture is missing")
	}

	// Not installed and no release to inspect: the card says nothing.
	if _, ok := folderAgentAssistantFacts(t.Context(), b, "local", releases.provider.Key, entry.Key); ok {
		t.Fatal("an unreadable team named an assistant")
	}

	if err := plugin.NewStore(plugins).Put(plugin.InstalledPlugin{Name: entry.PluginID, Version: "0.11.0", Enabled: true,
		ResolvedBlueprints: []plugin.ResolvedBlueprint{songBlueprint(entry.ExpectedBlueprintID, false)}}); err != nil {
		t.Fatal(err)
	}
	facts, ok := folderAgentAssistantFacts(t.Context(), b, "local", releases.provider.Key, entry.Key)
	if !ok || facts.State != folderlook.AssistantHire || facts.Name != "App Assistant" || facts.Label != "App Assistant" ||
		!strings.HasPrefix(facts.Job, "Handles everything") || strings.Contains(facts.Job, "SECRET") {
		t.Fatalf("facts = %+v ok=%v", facts, ok)
	}
	if ids, err := files.List(); err != nil || len(ids) != 0 {
		t.Fatalf("naming the assistant created a workspace: %v %v", ids, err)
	}

	// One of the owner's agents holds the name: the hire takes the next one.
	b.st.(*journeyAgentStore).agents["App Assistant"] = nil
	facts, _ = folderAgentAssistantFacts(t.Context(), b, "local", releases.provider.Key, entry.Key)
	if facts.Name != "App Assistant 2" || facts.Taken != "App Assistant" {
		t.Fatalf("taken = %+v", facts)
	}
}

// FR12: with no song team to read, the assistant's line fails with a reason
// and the run is told to stop; nothing is granted.
func TestHireFolderAgentAssistant_NoTeamFailsItsLineOnly(t *testing.T) {
	b := &ServerBuilder{}
	var lines []string
	setLine := func(name, state, progress string) { lines = append(lines, name+"|"+state+"|"+progress) }
	name, stopped := b.hireFolderAgentAssistant(t.Context(), folderagent.Record{WorkspaceID: "ws"},
		folderAgentAssistantRun{line: "Hires App Assistant", homeID: "home"}, true, setLine)
	if name != "" || stopped == nil || stopped.reason != personalassistant.FolderStopAssistantUnavailable {
		t.Fatalf("name %q stop %+v", name, stopped)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Hires App Assistant|failed|Its plugin is not installed") {
		t.Fatalf("lines = %q", lines)
	}
}
