package specialist

import (
	"encoding/json"
	"testing"
)

func FuzzParseSetupJourneyFailsClosed(f *testing.F) {
	valid := SetupJourney{
		SchemaVersion:              SetupJourneySchemaVersion,
		Version:                    1,
		ID:                         "fixture_setup",
		Title:                      "Fixture setup",
		Description:                "Connect one fixture through reviewed local steps.",
		IntegrationKey:             "fixture_integration",
		ExpectedBlueprintID:        "fixture_project",
		ExpectedAssistantProgramID: "fixture_assistant",
		Steps: []SetupJourneyStep{
			{ID: "integration", Kind: SetupStepIntegrationInstall, Title: "Integration", Description: "Review the integration."},
			{ID: "project", Kind: SetupStepProjectConnect, Title: "Project", Description: "Connect the project."},
			{ID: "workspace", Kind: SetupStepWorkspaceSetup, Title: "Workspace", Description: "Choose a mode."},
			{ID: "staffing", Kind: SetupStepAssistantProgramStaffing, Title: "Staffing", Description: "Review staffing."},
			{ID: "summary", Kind: SetupStepSummary, Title: "Summary", Description: "Review completion."},
		},
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{"schema_version":1,"version":1}`))
	f.Add([]byte("not-json"))

	f.Fuzz(func(t *testing.T, data []byte) {
		declaration, err := ParseSetupJourney(data)
		if err != nil {
			return
		}
		if declaration.SchemaVersion != SetupJourneySchemaVersion {
			t.Fatalf("accepted schema version %d", declaration.SchemaVersion)
		}
		if len(declaration.Steps) != SetupJourneyRequiredSteps {
			t.Fatalf("accepted %d steps", len(declaration.Steps))
		}
		for index, step := range declaration.Steps {
			if step.Kind != setupJourneyStepOrder[index] {
				t.Fatalf("accepted step %d kind %q", index, step.Kind)
			}
		}
	})
}
