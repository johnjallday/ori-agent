package specialist

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
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

func validAccountLinkSetupJourney() SetupJourney {
	return SetupJourney{
		SchemaVersion:       SetupJourneySchemaVersion,
		Version:             1,
		ID:                  "example_account_setup",
		Title:               "Set up an account workspace",
		Description:         "Create a workspace, connect an account, and link it.",
		ExpectedBlueprintID: "example-blueprint",
		Steps: []SetupJourneyStep{
			{ID: "team", Kind: SetupStepWorkspaceCreate, Title: "Review your team", Description: "Create the workspace after reviewing its team."},
			{ID: "connect", Kind: SetupStepAccountConnect, Title: "Connect the account", Description: "Connect the account in Settings."},
			{ID: "mailbox", Kind: SetupStepAccountLink, Title: "Link the account", Description: "Confirm linking the account to this workspace."},
			{ID: "summary", Kind: SetupStepSummary, Title: "Ready", Description: "Review what is ready."},
		},
	}
}

func TestNormalizeSetupJourneyAcceptsAccountLinkShape(t *testing.T) {
	input := validAccountLinkSetupJourney()
	input.Steps[1].Kind = " ACCOUNT_CONNECT "
	got, err := NormalizeSetupJourney(input)
	if err != nil {
		t.Fatalf("NormalizeSetupJourney: %v", err)
	}
	if got.Shape() != SetupJourneyShapeAccountLink {
		t.Fatalf("shape = %q, want %q", got.Shape(), SetupJourneyShapeAccountLink)
	}
	if got.Steps[1].Kind != SetupStepAccountConnect || got.IntegrationKey != "" || got.ExpectedAssistantProgramID != "" || got.WorkspaceLaunch != nil {
		t.Fatalf("account-link declaration was not normalized: %+v", got)
	}
}

func TestNormalizeSetupJourneyRejectsInvalidAccountLinkShape(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SetupJourney)
		message string
	}{
		{name: "integration key set", mutate: func(j *SetupJourney) { j.IntegrationKey = "example_integration" }, message: "integration_key must be empty"},
		{name: "assistant program set", mutate: func(j *SetupJourney) { j.ExpectedAssistantProgramID = "example-program" }, message: "expected_assistant_program_id must be empty"},
		{name: "workspace launch present", mutate: func(j *SetupJourney) {
			j.WorkspaceLaunch = &WorkspaceLaunchCopy{GroupTitle: "Group", GroupName: "Group", RuntimeTitle: "Runtime", RuntimeInstructions: "Plain text."}
		}, message: "workspace_launch is not allowed"},
		{name: "missing blueprint", mutate: func(j *SetupJourney) { j.ExpectedBlueprintID = "" }, message: "expected_blueprint_id"},
		{name: "missing step", mutate: func(j *SetupJourney) { j.Steps = j.Steps[:3] }, message: "exactly 4 steps for the account_link shape"},
		{name: "five steps", mutate: func(j *SetupJourney) {
			j.Steps = append(j.Steps, SetupJourneyStep{ID: "extra", Kind: SetupStepSummary, Title: "Extra", Description: "Extra."})
		}, message: "exactly 4 steps"},
		{name: "duplicated summary", mutate: func(j *SetupJourney) {
			j.Steps[2] = SetupJourneyStep{ID: "early", Kind: SetupStepSummary, Title: "Early", Description: "Early."}
		}, message: "kind must be"},
		{name: "reordered kinds", mutate: func(j *SetupJourney) { j.Steps[1], j.Steps[2] = j.Steps[2], j.Steps[1] }, message: "kind must be"},
		{name: "specialist kind inserted", mutate: func(j *SetupJourney) { j.Steps[1].Kind = SetupStepProjectConnect }, message: "kind must be"},
		{name: "unknown kind", mutate: func(j *SetupJourney) { j.Steps[2].Kind = "custom" }, message: "kind must be"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			declaration := validAccountLinkSetupJourney()
			test.mutate(&declaration)
			_, err := NormalizeSetupJourney(declaration)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want it to mention %q", err, test.message)
			}
		})
	}
}

func TestNormalizeSetupJourneyCannotMixShapes(t *testing.T) {
	// Specialist references on an account-link sequence are rejected, and an
	// account-link kind inside the specialist sequence is rejected, so neither
	// shape can borrow the other's steps or fields.
	mixed := validSetupJourney()
	mixed.Steps = validAccountLinkSetupJourney().Steps
	if _, err := NormalizeSetupJourney(mixed); err == nil {
		t.Fatal("account-link steps with specialist references were accepted")
	}
	specialist := validSetupJourney()
	specialist.Steps[2].Kind = SetupStepAccountLink
	if _, err := NormalizeSetupJourney(specialist); err == nil {
		t.Fatal("specialist sequence with an account-link kind was accepted")
	}
	shortSpecialist := validSetupJourney()
	shortSpecialist.Steps = shortSpecialist.Steps[:4]
	if _, err := NormalizeSetupJourney(shortSpecialist); err == nil || err.Error() != "setup journey must contain exactly 5 steps" {
		t.Fatalf("specialist count error changed: %v", err)
	}
}

func TestParseSetupJourneyRejectsAuthoredShapeField(t *testing.T) {
	data, err := json.Marshal(validAccountLinkSetupJourney())
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["shape"] = "specialist"
	withShape, _ := json.Marshal(object)
	if _, err := ParseSetupJourney(withShape); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("authored shape field error = %v", err)
	}
}

func TestSetupJourneyShapeIsInferredFromExactSequence(t *testing.T) {
	specialist := validSetupJourney()
	if got := specialist.Shape(); got != SetupJourneyShapeSpecialist {
		t.Fatalf("specialist shape = %q", got)
	}
	account := validAccountLinkSetupJourney()
	if got := account.Shape(); got != SetupJourneyShapeAccountLink {
		t.Fatalf("account-link shape = %q", got)
	}
	account.Steps = account.Steps[:3]
	if got := account.Shape(); got != "" {
		t.Fatalf("truncated sequence shape = %q, want none", got)
	}
	var missing *SetupJourney
	if got := missing.Shape(); got != "" {
		t.Fatalf("nil declaration shape = %q", got)
	}
	steps := SetupJourneyShapeSteps(SetupJourneyShapeSpecialist)
	steps[0] = "mutated"
	if SetupJourneyShapeSteps(SetupJourneyShapeSpecialist)[0] != SetupStepIntegrationInstall {
		t.Fatal("shape steps were mutated through a returned slice")
	}
	if SetupJourneyShapeSteps("custom") != nil {
		t.Fatal("unknown shape returned steps")
	}
}

func TestSetupStepKindsListsEveryCompiledKindOnce(t *testing.T) {
	want := []SetupStepKind{
		SetupStepIntegrationInstall, SetupStepProjectConnect, SetupStepWorkspaceSetup,
		SetupStepAssistantProgramStaffing, SetupStepSummary,
		SetupStepWorkspaceCreate, SetupStepAccountConnect, SetupStepAccountLink,
	}
	if got := SetupStepKinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SetupStepKinds() = %v, want %v", got, want)
	}
}

// FR 3: no field may be added to the declaration encoding, which is what keeps
// every existing declaration's normalized bytes identical across the widening.
func TestNormalizeSetupJourneyDeclaresNoNewFields(t *testing.T) {
	tags := func(value any) []string {
		kind := reflect.TypeOf(value)
		result := make([]string, 0, kind.NumField())
		for field := range kind.Fields() {
			result = append(result, field.Tag.Get("json"))
		}
		sort.Strings(result)
		return result
	}
	wantJourney := []string{
		"-", "description", "expected_assistant_program_id", "expected_blueprint_id", "id",
		"integration_key", "schema_version", "steps", "title", "version", "workspace_launch,omitempty",
	}
	sort.Strings(wantJourney)
	if got := tags(SetupJourney{}); !reflect.DeepEqual(got, wantJourney) {
		t.Fatalf("SetupJourney JSON tags = %v, want %v", got, wantJourney)
	}
	wantStep := []string{"description", "id", "kind", "title"}
	if got := tags(SetupJourneyStep{}); !reflect.DeepEqual(got, wantStep) {
		t.Fatalf("SetupJourneyStep JSON tags = %v, want %v", got, wantStep)
	}
}

func TestCompatibilityDeclarationNormalizesToItself(t *testing.T) {
	data, err := legacySetupDeclarations.ReadFile("compatibility/reaper-setup.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw SetupJourney
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	normalized, err := ParseSetupJourney(data)
	if err != nil {
		t.Fatalf("compatibility declaration no longer parses: %v", err)
	}
	if normalized.Shape() != SetupJourneyShapeSpecialist {
		t.Fatalf("compatibility shape = %q", normalized.Shape())
	}
	want, _ := json.Marshal(raw)
	got, _ := json.Marshal(normalized)
	if !bytes.Equal(got, want) {
		t.Fatalf("compatibility declaration changed under normalization:\n got %s\nwant %s", got, want)
	}
	again, err := NormalizeSetupJourney(*normalized)
	if err != nil {
		t.Fatal(err)
	}
	if encoded, _ := json.Marshal(again); !bytes.Equal(encoded, got) {
		t.Fatal("normalization is not idempotent for the compatibility declaration")
	}
}
