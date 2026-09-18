package plugin

import (
	"os"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestReviewedCandidateHostContract is opt-in because the external candidate is
// a separate ignored Git checkout. Its wrapper temporarily points the manifest
// at locally built bytes, then this test exercises the real host normalizers.
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
		!slices.Contains(descriptor.WorkspaceSurfaces.RequiresHostFeatures, HostFeatureSpecialistSetupJourneyV1) ||
		!slices.Contains(descriptor.WorkspaceSurfaces.RequiresHostFeatures, HostFeatureSetupQuestsV2) {
		t.Fatalf("candidate identity/host contract = %#v", descriptor)
	}
	if len(descriptor.ResolvedBlueprints) != 1 {
		t.Fatalf("candidate blueprints = %#v", descriptor.ResolvedBlueprints)
	}
	blueprint := descriptor.ResolvedBlueprints[0]
	connection := blueprint.Template.ProjectConnection
	program := blueprint.Template.AssistantProgram
	// A floor, like the plugin version: the reviewed blueprint may move forward,
	// and v8 is where it began asking for typed inputs.
	if blueprint.ID != "reaper-song" || blueprint.Version < 8 || connection == nil ||
		!slices.Equal(connection.SupportedModes, []projecttemplates.ProjectConnectionMode{
			projecttemplates.ProjectConnectionExistingProject,
			projecttemplates.ProjectConnectionNewProject,
		}) || connection.AttachExisting == nil ||
		!slices.Equal(connection.AttachExisting.EntryExtensions, []string{".rpp"}) {
		t.Fatalf("candidate project connection = blueprint:%#v connection:%#v", blueprint, connection)
	}
	if program == nil || program.SchemaVersion != workspace.AssistantProgramSchemaVersion ||
		len(program.Roles) != 5 || program.Roles[0].Scope != workspace.AssistantRoleScopeHome ||
		program.Roles[1].Scope != workspace.AssistantRoleScopeProject ||
		program.Roles[4].CapabilityID != "sample-library" {
		t.Fatalf("candidate scoped assistant program = %#v", program)
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
