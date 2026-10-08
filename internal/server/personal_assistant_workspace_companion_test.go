package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// This catalog admits only the already reviewed blueprint's runtime references.
// It neither registers nor executes a service or capability.
type workspaceAwarenessCandidateCatalog struct{}

func (workspaceAwarenessCandidateCatalog) HasCapability(id string) bool {
	return id == "reaper-live-control"
}
func (workspaceAwarenessCandidateCatalog) HasRuntimeAdapter(id string) bool {
	return id == "plugin:reaper-plugin:reaper-runtime"
}

// Read-only, explicit-source contract check; no download, plugin install, build,
// source edits or runtime. Normal CI does not depend on companion checkouts.
// Supply separately verified clean Git source paths, never installed copies.
func TestAssistantWorkspaceCompanion_ExplicitSourceReadContract(t *testing.T) {
	reaperSource := os.Getenv("ORI_AWARENESS_REAPER_SOURCE")
	musicSource := os.Getenv("ORI_AWARENESS_MUSIC_SOURCE")
	if reaperSource == "" || musicSource == "" {
		t.Skip("exact companion source paths not supplied; integration evidence is separate")
	}
	if !filepath.IsAbs(reaperSource) || !filepath.IsAbs(musicSource) {
		t.Fatal("companion paths must be absolute verified source paths")
	}
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		t.Fatal("missing host registry")
	}
	var manifest struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		Blueprints []struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"blueprints"`
	}
	read := func(path string, destination any) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, destination); err != nil {
			t.Fatal(err)
		}
	}
	read(filepath.Join(reaperSource, ".ori-plugin", "plugin.json"), &manifest)
	if manifest.Name != entry.PluginID || !reviewedintegration.AtLeast(manifest.Version, entry.MinimumVersion) || len(manifest.Blueprints) != 1 ||
		manifest.Blueprints[0].ID != entry.ExpectedBlueprintID || manifest.Blueprints[0].Version < entry.MinimumBlueprintVersion {
		t.Fatalf("REAPER source does not meet the host floor: %+v", manifest)
	}
	blueprintRoot := filepath.Join(reaperSource, "blueprints", "reaper-song")
	blueprint, _, err := projecttemplates.LoadPluginBlueprint(filepath.Join(blueprintRoot, "template.json"), filepath.Join(blueprintRoot, "project"), workspaceAwarenessCandidateCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	if blueprint.ProjectEntry == nil || blueprint.ProjectEntry.RelativePath != "{{name}}.rpp" || blueprint.ProjectConnection == nil ||
		blueprint.ProjectConnection.AttachExisting == nil || len(blueprint.ProjectConnection.AttachExisting.EntryExtensions) != 1 ||
		blueprint.ProjectConnection.AttachExisting.EntryExtensions[0] != ".rpp" || blueprint.AssistantProject == nil || blueprint.GroupRequirement == nil {
		t.Fatal("missing exact file-only entry/connection and Home policy contract")
	}
	var music struct {
		Name    string                                  `json:"name"`
		Version string                                  `json:"version"`
		Homes   []projecttemplates.AssistantProgramHome `json:"assistant_program_homes"`
	}
	read(filepath.Join(musicSource, ".ori-plugin", "plugin.json"), &music)
	if music.Name != "music-project-management" || !reviewedintegration.AtLeast(music.Version, "0.1.0") || len(music.Homes) != 1 {
		t.Fatal("Music source is not the independent Home provider")
	}
	home := music.Homes[0]
	if err := projecttemplates.NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatal(err)
	}
	project := blueprint.AssistantProject
	if project.Home.ProviderPluginID != music.Name || project.Home.ProgramID != home.ID || project.Home.HomeSchemaVersion != home.SchemaVersion ||
		home.Version < project.Home.MinHomeVersion || home.Version > project.Home.MaxHomeVersion {
		t.Fatal("Home/project compatibility differs from the reviewed contract")
	}
	allowed := false
	for _, link := range home.AllowedProjectAttachments {
		if link.ProviderPluginID == manifest.Name && link.BlueprintID == manifest.Blueprints[0].ID && link.ProjectTeamID == project.ID &&
			link.ProjectTeamSchemaVersion == project.SchemaVersion && project.Version >= link.MinProjectTeamVersion && project.Version <= link.MaxProjectTeamVersion {
			allowed = true
		}
	}
	if !allowed {
		t.Fatal("Home does not reciprocally authorize this exact project declaration")
	}
	t.Logf("read-only source contract: REAPER %s blueprint %d, Music %s Home schema/version %d/%d; not installed/runtime/release evidence", manifest.Version, manifest.Blueprints[0].Version, music.Version, home.SchemaVersion, home.Version)
}
