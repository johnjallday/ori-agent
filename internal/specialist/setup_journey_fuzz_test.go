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
	account := SetupJourney{
		SchemaVersion:       SetupJourneySchemaVersion,
		Version:             1,
		ID:                  "fixture_account_setup",
		Title:               "Fixture account setup",
		Description:         "Create a workspace and link one account.",
		ExpectedBlueprintID: "fixture_project",
		Steps: []SetupJourneyStep{
			{ID: "team", Kind: SetupStepWorkspaceCreate, Title: "Team", Description: "Create the workspace."},
			{ID: "connect", Kind: SetupStepAccountConnect, Title: "Connect", Description: "Connect the account."},
			{ID: "link", Kind: SetupStepAccountLink, Title: "Link", Description: "Link the account."},
			{ID: "summary", Kind: SetupStepSummary, Title: "Summary", Description: "Review completion."},
		},
	}
	for _, seed := range []SetupJourney{valid, account} {
		encoded, err := json.Marshal(seed)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(encoded)
	}
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
		shape := declaration.Shape()
		expected := SetupJourneyShapeSteps(shape)
		if shape == "" || len(declaration.Steps) != len(expected) {
			t.Fatalf("accepted %d steps with shape %q", len(declaration.Steps), shape)
		}
		for index, step := range declaration.Steps {
			if step.Kind != expected[index] {
				t.Fatalf("accepted step %d kind %q for shape %q", index, step.Kind, shape)
			}
		}
		switch shape {
		case SetupJourneyShapeSpecialist:
			if len(declaration.Steps) != SetupJourneyRequiredSteps || declaration.IntegrationKey == "" ||
				declaration.ExpectedAssistantProgramID == "" {
				t.Fatalf("accepted specialist declaration without its references: %+v", declaration)
			}
		case SetupJourneyShapeAccountLink:
			if declaration.IntegrationKey != "" || declaration.ExpectedAssistantProgramID != "" ||
				declaration.WorkspaceLaunch != nil || declaration.ExpectedBlueprintID == "" {
				t.Fatalf("accepted account-link declaration with specialist fields: %+v", declaration)
			}
		}
	})
}
