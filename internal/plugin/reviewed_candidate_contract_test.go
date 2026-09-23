package plugin

import (
	"os"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// TestReviewedCandidateHostContract is opt-in because it downloads an external
// source and its declared service artifact. It exercises the real host
// normalizers against an exact candidate or published commit.
func TestReviewedCandidateHostContract(t *testing.T) {
	source := os.Getenv("ORI_REVIEWED_PLUGIN_CANDIDATE")
	if source == "" {
		t.Skip("external reviewed candidate checkout not supplied")
	}
	descriptor, err := Load(source, t.TempDir(), FormatClaude)
	if err != nil {
		t.Fatalf("load reviewed candidate: %v", err)
	}
	if err := prepareTrustedBlueprints(&descriptor); err != nil {
		t.Fatalf("resolve reviewed candidate blueprints: %v", err)
	}
	// The version is a floor, not a pin. This package cannot import the
	// registry, so TestReviewedCandidateMeetsTheFloor in
	// internal/reviewedintegration checks it against the reviewed minimum.
	if descriptor.Name != "reaper-plugin" || descriptor.Version == "" ||
		descriptor.WorkspaceSurfaces == nil ||
		!slices.Contains(descriptor.WorkspaceSurfaces.RequiresHostFeatures, HostFeatureIndependentProgramHomesV1) ||
		!slices.Contains(descriptor.WorkspaceSurfaces.RequiresHostFeatures, HostFeatureSpecialistSetupJourneyV1) ||
		!slices.Contains(descriptor.WorkspaceSurfaces.RequiresHostFeatures, HostFeatureSetupQuestsV2) {
		t.Fatalf("candidate identity/host contract = %#v", descriptor)
	}
	if len(descriptor.ResolvedBlueprints) != 1 {
		t.Fatalf("candidate blueprints = %#v", descriptor.ResolvedBlueprints)
	}
	blueprint := descriptor.ResolvedBlueprints[0]
	connection := blueprint.Template.ProjectConnection
	project := blueprint.Template.AssistantProject
	// A floor, like the plugin version: the reviewed blueprint may move forward,
	// and v9 is where it adopted the independent Home contract.
	if blueprint.ID != "reaper-song" || blueprint.Version < 9 || connection == nil ||
		!slices.Equal(connection.SupportedModes, []projecttemplates.ProjectConnectionMode{
			projecttemplates.ProjectConnectionExistingProject,
			projecttemplates.ProjectConnectionNewProject,
		}) || connection.AttachExisting == nil ||
		!slices.Equal(connection.AttachExisting.EntryExtensions, []string{".rpp"}) {
		t.Fatalf("candidate project connection = blueprint:%#v connection:%#v", blueprint, connection)
	}
	if blueprint.Template.AssistantProgram != nil || project == nil ||
		project.SchemaVersion != projecttemplates.AssistantProjectSchemaVersion ||
		project.ID != "reaper-song-team" || project.Home.ProviderPluginID != "music-project-management" ||
		project.Home.ProgramID != "music-producer-assistant" ||
		project.Home.HomeSchemaVersion != projecttemplates.AssistantProgramHomeSchemaVersion ||
		project.Home.MinHomeVersion != 1 || project.Home.MaxHomeVersion != 1 ||
		len(project.Roles) != 3 || project.Roles[0].ID != "producer" ||
		project.Roles[1].ID != "engineer" || project.Roles[2].ID != "songwriter" {
		t.Fatalf("candidate assistant project = %#v", project)
	}
	// The candidate's typed inputs must survive the real loader. An unusable
	// declaration would leave Inputs nil and InputsError set, which is exactly
	// the outcome LoadPluginBlueprint turns into an unavailable blueprint — the
	// failure this contract exists to catch before a release is offered.
	inputs := blueprint.Template.Inputs
	if blueprint.Template.InputsError != "" || inputs == nil || len(inputs.Fields) != 2 ||
		inputs.Fields[0].ID != "tempo" || inputs.Fields[0].Type != projecttemplates.InputFieldNumber ||
		inputs.Fields[1].ID != "time_signature" || inputs.Fields[1].Type != projecttemplates.InputFieldSelect {
		t.Fatalf("candidate inputs = %#v err=%q", inputs, blueprint.Template.InputsError)
	}
	if !slices.Contains(descriptor.WorkspaceSurfaces.RequiresHostFeatures, HostFeatureBlueprintInputsV1) {
		t.Fatal("a candidate that declares inputs must require blueprint_inputs_v1, or an older host would reject its whole blueprint")
	}
	values, err := projecttemplates.ResolveInputValues(inputs, nil)
	if err != nil || values["tempo"] != "120" || values["time_signature"] != "4 4" {
		t.Fatalf("candidate declared defaults = %v err=%v", values, err)
	}

	report := BuildTrustReport(descriptor)
	if len(report.Artifacts) != 1 || report.Artifacts[0].SHA256 == "" ||
		report.Artifacts[0].Size <= 0 || len(report.Services) == 0 {
		t.Fatalf("candidate trust material incomplete: %#v", report)
	}
}
