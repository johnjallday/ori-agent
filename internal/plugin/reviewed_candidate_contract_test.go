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
	if descriptor.Name != "reaper-plugin" || descriptor.Version != "0.5.0" ||
		descriptor.WorkspaceSurfaces == nil ||
		!slices.Contains(descriptor.WorkspaceSurfaces.RequiresHostFeatures, HostFeatureSpecialistSetupJourneyV1) {
		t.Fatalf("candidate identity/host contract = %#v", descriptor)
	}
	if len(descriptor.ResolvedBlueprints) != 1 {
		t.Fatalf("candidate blueprints = %#v", descriptor.ResolvedBlueprints)
	}
	blueprint := descriptor.ResolvedBlueprints[0]
	connection := blueprint.Template.ProjectConnection
	program := blueprint.Template.AssistantProgram
	if blueprint.ID != "reaper-song" || blueprint.Version != 4 || connection == nil ||
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
	report := BuildTrustReport(descriptor)
	if len(report.Artifacts) != 1 || report.Artifacts[0].SHA256 == "" ||
		report.Artifacts[0].Size <= 0 || len(report.Services) == 0 {
		t.Fatalf("candidate trust material incomplete: %#v", report)
	}
}
