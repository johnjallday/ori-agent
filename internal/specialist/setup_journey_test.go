package specialist

import (
	"encoding/json"
	"strings"
	"testing"
)

func validSetupJourney() SetupJourney {
	return SetupJourney{
		SchemaVersion:              SetupJourneySchemaVersion,
		Version:                    1,
		ID:                         "example_setup",
		Title:                      "Set up an example",
		Description:                "Review each step before anything changes.",
		IntegrationKey:             "example_integration",
		ExpectedBlueprintID:        "example-blueprint",
		ExpectedAssistantProgramID: "example-program",
		Steps: []SetupJourneyStep{
			{ID: "integration", Kind: SetupStepIntegrationInstall, Title: "Review integration", Description: "Review what the integration needs."},
			{ID: "project", Kind: SetupStepProjectConnect, Title: "Connect a project", Description: "Choose an existing or new project."},
			{ID: "workspace", Kind: SetupStepWorkspaceSetup, Title: "Choose workspace setup", Description: "Choose how the workspace should work."},
			{ID: "staffing", Kind: SetupStepAssistantProgramStaffing, Title: "Add a team", Description: "Review the roles for this workspace."},
			{ID: "summary", Kind: SetupStepSummary, Title: "Review setup", Description: "Review the canonical setup results."},
		},
	}
}

func TestParseSetupJourneyStrictlyNormalizesValidDeclaration(t *testing.T) {
	input := validSetupJourney()
	input.ID = "  EXAMPLE_SETUP "
	input.Steps[0].ID = " INTEGRATION "
	input.Steps[0].Kind = " INTEGRATION_INSTALL "
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSetupJourney(data)
	if err != nil {
		t.Fatalf("ParseSetupJourney: %v", err)
	}
	if got.ID != "example_setup" || got.Steps[0].ID != "integration" || got.Steps[0].Kind != SetupStepIntegrationInstall {
		t.Fatalf("declaration was not normalized: %+v", got)
	}
}

func TestParseSetupJourneyRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	valid := validSetupJourney()
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}

	object["action"] = "caller_selected"
	withUnknown, _ := json.Marshal(object)
	if _, err := ParseSetupJourney(withUnknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("top-level unknown field error = %v", err)
	}

	delete(object, "action")
	steps := object["steps"].([]any)
	steps[0].(map[string]any)["adapter"] = "caller_selected"
	withNestedUnknown, _ := json.Marshal(object)
	if _, err := ParseSetupJourney(withNestedUnknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("nested unknown field error = %v", err)
	}

	if _, err := ParseSetupJourney(append(data, []byte(` {"again":true}`)...)); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing value error = %v", err)
	}
}

func TestNormalizeSetupJourneyRejectsInvalidShape(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SetupJourney)
	}{
		{name: "schema", mutate: func(j *SetupJourney) { j.SchemaVersion = 2 }},
		{name: "version zero", mutate: func(j *SetupJourney) { j.Version = 0 }},
		{name: "version too large", mutate: func(j *SetupJourney) { j.Version = MaxSetupJourneyVersion + 1 }},
		{name: "invalid id", mutate: func(j *SetupJourney) { j.ID = "bad/id" }},
		{name: "missing integration", mutate: func(j *SetupJourney) { j.IntegrationKey = "" }},
		{name: "missing blueprint", mutate: func(j *SetupJourney) { j.ExpectedBlueprintID = "" }},
		{name: "missing program", mutate: func(j *SetupJourney) { j.ExpectedAssistantProgramID = "" }},
		{name: "missing step", mutate: func(j *SetupJourney) { j.Steps = j.Steps[:4] }},
		{name: "extra step", mutate: func(j *SetupJourney) { j.Steps = append(j.Steps, j.Steps[4]) }},
		{name: "duplicate normalized step id", mutate: func(j *SetupJourney) { j.Steps[1].ID = " INTEGRATION " }},
		{name: "wrong order", mutate: func(j *SetupJourney) { j.Steps[0], j.Steps[1] = j.Steps[1], j.Steps[0] }},
		{name: "unknown kind", mutate: func(j *SetupJourney) { j.Steps[2].Kind = "custom" }},
		{name: "blank title", mutate: func(j *SetupJourney) { j.Title = "   " }},
		{name: "oversized title", mutate: func(j *SetupJourney) { j.Title = strings.Repeat("a", MaxSetupJourneyTitleBytes+1) }},
		{name: "oversized description", mutate: func(j *SetupJourney) { j.Description = strings.Repeat("a", MaxSetupJourneyTextBytes+1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			declaration := validSetupJourney()
			test.mutate(&declaration)
			if _, err := NormalizeSetupJourney(declaration); err == nil {
				t.Fatal("invalid declaration was accepted")
			}
		})
	}
}

func TestNormalizeSetupJourneyRejectsMarkupURLsAndControls(t *testing.T) {
	values := []string{
		"Open <strong>setup</strong>",
		"Open **setup**",
		"Open [setup](somewhere)",
		"Open https://example.invalid",
		"Open file:secret",
		"Open\x00setup",
		"Open\u202esetup",
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			declaration := validSetupJourney()
			declaration.Steps[0].Description = value
			if _, err := NormalizeSetupJourney(declaration); err == nil {
				t.Fatalf("unsafe display value %q was accepted", value)
			}
		})
	}
}

func TestParseSetupJourneyEnforcesSerializedSize(t *testing.T) {
	data := []byte(`{"schema_version":1,"padding":"` + strings.Repeat("x", MaxSetupJourneyBytes) + `"}`)
	if _, err := ParseSetupJourney(data); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized declaration error = %v", err)
	}
}

func TestRegistryEntryWithoutJourneyKeepsNoJourney(t *testing.T) {
	entries := mustNormalizeRegistry([]Entry{{Slug: "plain"}})
	if len(entries) != 1 || entries[0].SetupJourney != nil {
		t.Fatalf("entry without journey changed: %+v", entries)
	}
}

func TestBuiltInSetupJourneyContract(t *testing.T) {
	entry, ok := Get("music_production")
	if !ok || entry.SetupJourney == nil {
		t.Fatal("music specialist must expose setup journey")
	}
	journey := entry.SetupJourney
	if journey.SchemaVersion != 1 || journey.Version != 1 || journey.ID != "reaper_setup" {
		t.Fatalf("unexpected journey identity: %+v", journey)
	}
	if journey.IntegrationKey != "ori_reaper" || journey.ExpectedBlueprintID != "reaper-song" || journey.ExpectedAssistantProgramID != "music-producer-assistant" {
		t.Fatalf("unexpected journey references: %+v", journey)
	}
	const explanation = "Ori's REAPER integration is a local integration for Ori, not an audio plug-in, VST, effect, or instrument. It will not appear in REAPER's FX browser."
	if journey.Steps[0].Description != explanation {
		t.Fatalf("integration explanation = %q", journey.Steps[0].Description)
	}
	for index, kind := range setupJourneyStepOrder {
		if journey.Steps[index].Kind != kind {
			t.Fatalf("step %d kind = %q, want %q", index, journey.Steps[index].Kind, kind)
		}
	}
}

func TestRegistryReturnsDeepCopiesOfSetupJourney(t *testing.T) {
	entries := All()
	if len(entries) == 0 || entries[0].SetupJourney == nil {
		t.Fatal("expected built-in journey")
	}
	entries[0].SetupJourney.Title = "mutated"
	entries[0].SetupJourney.Steps[0].Title = "mutated"
	entries[0].AppPatterns[0][0] = "mutated"
	entries[0].CapabilityOrder[0] = "mutated"

	fresh, ok := Get("music_production")
	if !ok {
		t.Fatal("expected built-in entry")
	}
	if fresh.SetupJourney.Title == "mutated" || fresh.SetupJourney.Steps[0].Title == "mutated" || fresh.AppPatterns[0][0] == "mutated" || fresh.CapabilityOrder[0] == "mutated" {
		t.Fatal("registry state was mutated through a returned entry")
	}
}
