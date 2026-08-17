package workspace

import (
	"encoding/json"
	"testing"
)

func TestTemplateProvenance_SetGetIsFrom(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	if ws.GetTemplateProvenance() != nil {
		t.Fatal("new workspace should have no provenance")
	}
	if ws.IsFromTemplate("reaper-song") {
		t.Fatal("unset provenance must not match any template")
	}

	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "  reaper-song  ", Builtin: true, Version: 4})
	p := ws.GetTemplateProvenance()
	if p == nil || p.TemplateID != "reaper-song" {
		t.Fatalf("provenance not stored/trimmed: %+v", p)
	}
	if p.AppliedAt.IsZero() {
		t.Fatal("AppliedAt should default to now")
	}
	if !ws.IsFromTemplate("Reaper-Song") {
		t.Fatal("IsFromTemplate should be case-insensitive")
	}
	if ws.IsFromTemplate("writing-project") {
		t.Fatal("must not match a different template")
	}
}

func TestTemplateProvenance_GetReturnsCopy(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "reaper-song"})
	got := ws.GetTemplateProvenance()
	got.TemplateID = "mutated"
	if ws.GetTemplateProvenance().TemplateID != "reaper-song" {
		t.Fatal("GetTemplateProvenance must return a copy, not the internal pointer")
	}
}

func TestTemplateProvenance_JSONRoundTrip(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "reaper-song", TemplateName: "Reaper Song", Builtin: true, Version: 4})
	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatal(err)
	}
	var back Workspace
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !back.IsFromTemplate("reaper-song") {
		t.Fatalf("provenance did not survive JSON round-trip: %+v", back.TemplateProvenance)
	}
}

// wizardProvenance is a provenance record shaped like a real blueprint
// snapshot: a wizard plus every requirement kind its steps reference.
func wizardProvenance() *TemplateProvenance {
	return &TemplateProvenance{
		TemplateID:             "downloads-janitor",
		TemplateName:           "Downloads Janitor",
		Builtin:                true,
		Version:                2,
		DirectoryRequirements:  []DirectoryRequirement{{Key: "downloads-root", Label: "Downloads folder"}},
		AutomationRecipes:      []AutomationRecipe{{DirectoryKey: "downloads-root", Watch: &WatchRecipe{Events: []string{"create"}}}},
		CapabilityRequirements: []CapabilityRequirement{{Key: "calendar", RequiredOperations: []string{"list_events"}}},
		Plugins:                []string{"reaper-plugin"},
		PluginSources:          map[string]string{"reaper-plugin": "https://example.test/reaper-plugin.git"},
		SetupWizard: &SetupWizard{Version: 1, Title: "Set up Downloads Janitor", Steps: []SetupWizardStep{
			{ID: "folder", Kind: SetupStepKindDirectory, RequirementKey: "downloads-root", Required: true},
			{ID: "readiness", Kind: SetupStepKindReadiness, Adapter: "downloads_janitor", Required: true},
		}},
	}
}

// TestTemplateProvenance_SetupWizardSnapshotIsIsolated covers FR-17: the
// workspace's setup is a snapshot, not a live view of the template. Nothing a
// caller still holds — the record it passed in, or the one it reads back — may
// reach the stored copy, or a later blueprint edit could silently rewrite what
// an existing workspace is being asked to do.
func TestTemplateProvenance_SetupWizardSnapshotIsIsolated(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Downloads"})
	source := wizardProvenance()
	ws.SetTemplateProvenance(source)

	// The caller's record keeps changing after the snapshot is taken — the same
	// shape as a template being edited or refreshed after creation.
	source.SetupWizard.Title = "Rewritten"
	source.SetupWizard.Steps[0].RequirementKey = "/etc"
	source.CapabilityRequirements[0].RequiredOperations[0] = "delete_events"
	source.Plugins[0] = "evil-plugin"
	source.PluginSources["reaper-plugin"] = "https://evil.test/x.git"
	source.DirectoryRequirements[0].Key = "elsewhere"
	source.AutomationRecipes[0].Watch.Events[0] = "remove"

	stored := ws.GetTemplateProvenance()
	if stored.SetupWizard.Title != "Set up Downloads Janitor" || stored.SetupWizard.Steps[0].RequirementKey != "downloads-root" {
		t.Fatalf("a later edit to the source reached the stored wizard: %+v", stored.SetupWizard)
	}
	if stored.CapabilityRequirements[0].RequiredOperations[0] != "list_events" {
		t.Fatalf("capability operations were shared, not copied: %+v", stored.CapabilityRequirements)
	}
	if stored.Plugins[0] != "reaper-plugin" || stored.PluginSources["reaper-plugin"] != "https://example.test/reaper-plugin.git" {
		t.Fatalf("plugin declarations were shared, not copied: %v / %v", stored.Plugins, stored.PluginSources)
	}
	if stored.DirectoryRequirements[0].Key != "downloads-root" || stored.AutomationRecipes[0].Watch.Events[0] != "create" {
		t.Fatalf("setup requirements were shared, not copied: %+v", stored)
	}

	// Reads are copies too: mutating what Get returned must not change the next
	// read.
	stored.SetupWizard.Steps[1].Adapter = "evil"
	stored.PluginSources["reaper-plugin"] = "https://evil.test/x.git"
	stored.CapabilityRequirements[0].Key = "mutated"
	again := ws.GetTemplateProvenance()
	if again.SetupWizard.Steps[1].Adapter != "downloads_janitor" {
		t.Fatalf("GetTemplateProvenance handed out the stored wizard: %+v", again.SetupWizard)
	}
	if again.PluginSources["reaper-plugin"] != "https://example.test/reaper-plugin.git" || again.CapabilityRequirements[0].Key != "calendar" {
		t.Fatalf("GetTemplateProvenance handed out stored requirements: %+v", again)
	}
}

func TestTemplateProvenance_RuntimeRequirementsSnapshotIsIsolated(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Runtime"})
	source := &TemplateProvenance{
		TemplateID:          "runtime-demo",
		RuntimeRequirements: validWorkspaceRuntimeContract(),
	}
	ws.SetTemplateProvenance(source)

	// A later source-blueprint edit cannot rewrite the workspace snapshot.
	source.RuntimeRequirements.OperatingModes[1].Requires[0] = "changed"
	source.RuntimeRequirements.Requirements[0].Adapter = "changed"
	stored := ws.RuntimeRequirementsSnapshot()
	if stored == nil || stored.OperatingModes[1].Requires[0] != "reaper_live_control" || stored.Requirements[0].Adapter != "reaper_live_control" {
		t.Fatalf("source mutation reached runtime snapshot: %+v", stored)
	}
	if !ws.HasRuntimeRequirements() {
		t.Fatal("valid snapshotted runtime contract should be reported")
	}

	// Reads are detached as well.
	stored.OperatingModes[1].Requires[0] = "changed-again"
	stored.Requirements[0].Label = "Changed"
	again := ws.RuntimeRequirementsSnapshot()
	if again.OperatingModes[1].Requires[0] != "reaper_live_control" || again.Requirements[0].Label != "Local REAPER control" {
		t.Fatalf("runtime snapshot accessor exposed workspace-owned data: %+v", again)
	}

	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Workspace
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	decodedContract := decoded.RuntimeRequirementsSnapshot()
	if decodedContract == nil || !decodedContract.StructurallyValid() || decodedContract.Requirements[0].Key != "reaper_live_control" {
		t.Fatalf("runtime snapshot did not survive workspace JSON: %s / %+v", data, decodedContract)
	}
}

func TestWorkspace_SetupWizardAccessors(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Plain"})
	if ws.HasSetupWizard() || ws.SetupWizardSnapshot() != nil {
		t.Fatal("a workspace with no provenance has no wizard")
	}
	if _, ok := ws.TemplateCapabilityRequirement("calendar"); ok {
		t.Fatal("a workspace with no provenance declares no capability")
	}

	ws.SetTemplateProvenance(wizardProvenance())
	if !ws.HasSetupWizard() {
		t.Fatal("a wizard snapshot should be reported")
	}
	if got := ws.SetupWizardSnapshot(); got == nil || len(got.Steps) != 2 {
		t.Fatalf("SetupWizardSnapshot = %+v", got)
	}
	req, ok := ws.TemplateCapabilityRequirement("  Calendar  ")
	if !ok || req.Key != "calendar" {
		t.Fatalf("capability lookup should be normalized: %+v %v", req, ok)
	}
	if _, ok := ws.TemplateCapabilityRequirement("email"); ok {
		t.Fatal("an undeclared capability must not resolve")
	}

	// A provenance record without a wizard (every blueprint that predates this
	// feature) reports none rather than an empty one.
	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "writing-project", Builtin: true})
	if ws.HasSetupWizard() {
		t.Fatal("a blueprint with no wizard must not appear wizard-enabled")
	}
}

func TestTemplateProvenance_SetupWizardSurvivesJSONRoundTrip(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Downloads"})
	ws.SetTemplateProvenance(wizardProvenance())

	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatal(err)
	}
	var back Workspace
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	got := back.GetTemplateProvenance()
	if got == nil || got.SetupWizard == nil {
		t.Fatalf("wizard snapshot lost in workspace.json: %+v", got)
	}
	if got.SetupWizard.Title != "Set up Downloads Janitor" || len(got.SetupWizard.Steps) != 2 {
		t.Fatalf("wizard snapshot changed across the round-trip: %+v", got.SetupWizard)
	}
	if got.SetupWizard.Steps[1].Adapter != "downloads_janitor" || !got.SetupWizard.Steps[1].Required {
		t.Fatalf("step detail lost in the round-trip: %+v", got.SetupWizard.Steps[1])
	}
	if len(got.CapabilityRequirements) != 1 || got.CapabilityRequirements[0].RequiredOperations[0] != "list_events" {
		t.Fatalf("capability requirements lost in the round-trip: %+v", got.CapabilityRequirements)
	}
	if len(got.Plugins) != 1 || got.PluginSources["reaper-plugin"] == "" {
		t.Fatalf("plugin declarations lost in the round-trip: %v / %v", got.Plugins, got.PluginSources)
	}
}

func TestTemplateProvenance_ClearWithNil(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "reaper-song"})
	ws.SetTemplateProvenance(nil)
	if ws.GetTemplateProvenance() != nil {
		t.Fatal("nil should clear provenance")
	}
}
