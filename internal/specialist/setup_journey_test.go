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
			{ID: "project", Kind: SetupStepProjectConnect, Title: "Connect a project", Description: "Choose an existing or new project."},
			{ID: "workspace", Kind: SetupStepWorkspaceSetup, Title: "Choose workspace setup", Description: "Choose how the workspace should work."},
			{ID: "staffing", Kind: SetupStepAssistantProgramStaffing, Title: "Add a team", Description: "Review the roles for this workspace."},
			{ID: "summary", Kind: SetupStepSummary, Title: "Review setup", Description: "Review the canonical setup results."},
		},
		WorkspaceLaunch: &WorkspaceLaunchCopy{GroupTitle: "Build your group", GroupName: "Example group"},
	}
}

func TestParseSetupJourneyStrictlyNormalizesValidDeclaration(t *testing.T) {
	input := validSetupJourney()
	input.ID = "  EXAMPLE_SETUP "
	input.Steps[0].ID = " PROJECT "
	input.Steps[0].Kind = " PROJECT_CONNECT "
	input.WorkspaceLaunch.GroupName = "  Example group  "
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSetupJourney(data)
	if err != nil {
		t.Fatalf("ParseSetupJourney: %v", err)
	}
	if got.ID != "example_setup" || got.Steps[0].ID != "project" || got.Steps[0].Kind != SetupStepProjectConnect ||
		got.Shape() != SetupJourneyShapeProjectSetup || got.WorkspaceLaunch.GroupName != "Example group" {
		t.Fatalf("declaration was not normalized: %+v", got)
	}
}

// retiredFiveStepDeclaration is the published REAPER plugin v0.5.2 quest: five
// steps with an install step and runtime launch copy.
const retiredFiveStepDeclaration = `{
  "schema_version": 1, "version": 1, "id": "reaper_setup", "title": "Set up REAPER",
  "description": "Connect a REAPER project and choose how Ori can help.",
  "integration_key": "ori_reaper", "expected_blueprint_id": "reaper-song",
  "expected_assistant_program_id": "music-producer-assistant",
  "workspace_launch": {
    "group_title": "Build Your Music Production Group", "group_name": "Music Production",
    "runtime_title": "Set Up REAPER", "runtime_instructions": "Open REAPER, then enable the Web browser interface."
  },
  "steps": [
    {"id": "integration", "kind": "integration_install", "title": "Install Ori REAPER Plugin", "description": "Install the plugin."},
    {"id": "project", "kind": "project_connect", "title": "Connect a project", "description": "Connect a project."},
    {"id": "workspace", "kind": "workspace_setup", "title": "Choose how Ori works", "description": "Choose a mode."},
    {"id": "staffing", "kind": "assistant_program_staffing", "title": "Add your studio team", "description": "Add roles."},
    {"id": "summary", "kind": "summary", "title": "Review setup", "description": "Review setup."}
  ]
}`

// FR 2, FR 5: the retired five-step declaration is rejected with an error that
// names setup_quests_v2 and the four-step order, with or without its runtime
// launch copy. The runtime fields alone fail strict decoding.
func TestRetiredFiveStepDeclarationNamesTheV2Contract(t *testing.T) {
	const wantMessage = "setup journey must contain exactly 4 steps (project_connect, workspace_setup, assistant_program_staffing, summary) as required by setup_quests_v2"
	if _, err := ParseSetupJourney([]byte(retiredFiveStepDeclaration)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("runtime launch copy error = %v, want strict unknown field", err)
	}

	var object map[string]any
	if err := json.Unmarshal([]byte(retiredFiveStepDeclaration), &object); err != nil {
		t.Fatal(err)
	}
	launch := object["workspace_launch"].(map[string]any)
	delete(launch, "runtime_title")
	delete(launch, "runtime_instructions")
	withoutRuntime, _ := json.Marshal(object)
	if _, err := ParseSetupJourney(withoutRuntime); err == nil || err.Error() != wantMessage {
		t.Fatalf("five-step error = %v, want %q", err, wantMessage)
	}

	// Any other five-step or three-step authored sequence gets the same guidance.
	fiveProjectSteps := validSetupJourney()
	fiveProjectSteps.Steps = append(fiveProjectSteps.Steps, SetupJourneyStep{ID: "extra", Kind: SetupStepSummary, Title: "Extra", Description: "Extra."})
	short := validSetupJourney()
	short.Steps = short.Steps[:3]
	for name, declaration := range map[string]SetupJourney{"five project steps": fiveProjectSteps, "three steps": short} {
		if _, err := NormalizeSetupJourney(declaration); err == nil || err.Error() != wantMessage {
			t.Fatalf("%s error = %v, want %q", name, err, wantMessage)
		}
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
		{name: "missing step", mutate: func(j *SetupJourney) { j.Steps = j.Steps[:3] }},
		{name: "extra step", mutate: func(j *SetupJourney) { j.Steps = append(j.Steps, j.Steps[3]) }},
		{name: "duplicate normalized step id", mutate: func(j *SetupJourney) { j.Steps[1].ID = " PROJECT " }},
		{name: "blank group title", mutate: func(j *SetupJourney) { j.WorkspaceLaunch.GroupTitle = " " }},
		{name: "markup group name", mutate: func(j *SetupJourney) { j.WorkspaceLaunch.GroupName = "<b>Group</b>" }},
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

func TestRegistryEntryWithoutIntegrationKeepsNoKey(t *testing.T) {
	entries := mustNormalizeRegistry([]Entry{{Slug: "plain"}})
	if len(entries) != 1 || entries[0].IntegrationKey != "" {
		t.Fatalf("entry without an integration changed: %+v", entries)
	}
	normalized := mustNormalizeRegistry([]Entry{{Slug: "keyed", IntegrationKey: "  ORI_EXAMPLE "}})
	if normalized[0].IntegrationKey != "ori_example" {
		t.Fatalf("integration key = %q", normalized[0].IntegrationKey)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a malformed built-in integration key did not fail startup")
		}
	}()
	mustNormalizeRegistry([]Entry{{Slug: "bad", IntegrationKey: "bad/key"}})
}

// FR 19: the music specialist names its reviewed integration rather than
// embedding a setup declaration.
func TestBuiltInSpecialistNamesItsIntegration(t *testing.T) {
	entry, ok := Get("music_production")
	if !ok || entry.IntegrationKey != "ori_reaper" {
		t.Fatalf("music specialist integration key = %q", entry.IntegrationKey)
	}
}

func TestRegistryReturnsDeepCopies(t *testing.T) {
	entries := All()
	if len(entries) == 0 {
		t.Fatal("expected built-in entries")
	}
	entries[0].AppPatterns[0][0] = "mutated"
	entries[0].CapabilityOrder[0] = "mutated"
	entries[0].IntegrationKey = "mutated"

	fresh, ok := Get("music_production")
	if !ok {
		t.Fatal("expected built-in entry")
	}
	if fresh.AppPatterns[0][0] == "mutated" || fresh.CapabilityOrder[0] == "mutated" || fresh.IntegrationKey == "mutated" {
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
			j.WorkspaceLaunch = &WorkspaceLaunchCopy{GroupTitle: "Group", GroupName: "Group"}
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
		{name: "project kind inserted", mutate: func(j *SetupJourney) { j.Steps[1].Kind = SetupStepProjectConnect }, message: "kind must be"},
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

func validIntegrationInstallSetupJourney() SetupJourney {
	return SetupJourney{
		SchemaVersion:              SetupJourneySchemaVersion,
		Version:                    1,
		ID:                         "install_example_integration",
		Title:                      "Install Example Plugin",
		Description:                "Install the Example plugin before setting it up.",
		IntegrationKey:             "example_integration",
		ExpectedBlueprintID:        "example-blueprint",
		ExpectedAssistantProgramID: "example-program",
		Steps: []SetupJourneyStep{
			{ID: "integration", Kind: SetupStepIntegrationInstall, Title: "Install Example Plugin", Description: "Review and install the plugin."},
			{ID: "summary", Kind: SetupStepSummary, Title: "Example plugin ready", Description: "Setup continues in the plugin's own quest."},
		},
	}
}

func TestNormalizeSetupJourneyAcceptsIntegrationInstallShape(t *testing.T) {
	input := validIntegrationInstallSetupJourney()
	input.Steps[0].Kind = " INTEGRATION_INSTALL "
	got, err := NormalizeSetupJourney(input)
	if err != nil {
		t.Fatalf("NormalizeSetupJourney: %v", err)
	}
	if got.Shape() != SetupJourneyShapeIntegrationInstall {
		t.Fatalf("shape = %q, want %q", got.Shape(), SetupJourneyShapeIntegrationInstall)
	}
	if got.Steps[0].Kind != SetupStepIntegrationInstall || got.WorkspaceLaunch != nil || got.IntegrationKey != "example_integration" {
		t.Fatalf("install declaration was not normalized: %+v", got)
	}
}

func TestNormalizeSetupJourneyRejectsInvalidIntegrationInstallShape(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SetupJourney)
		message string
	}{
		{name: "missing integration key", mutate: func(j *SetupJourney) { j.IntegrationKey = "" }, message: "integration_key"},
		{name: "missing blueprint", mutate: func(j *SetupJourney) { j.ExpectedBlueprintID = "" }, message: "expected_blueprint_id"},
		{name: "missing assistant program", mutate: func(j *SetupJourney) { j.ExpectedAssistantProgramID = "" }, message: "expected_assistant_program_id"},
		{name: "workspace launch present", mutate: func(j *SetupJourney) {
			j.WorkspaceLaunch = &WorkspaceLaunchCopy{GroupTitle: "Group", GroupName: "Group"}
		}, message: "workspace_launch is not allowed for the integration_install shape"},
		// The first kind selects the candidate shape, so a pair that does not
		// start with the install kind is checked against project_setup.
		{name: "summary first", mutate: func(j *SetupJourney) { j.Steps[0], j.Steps[1] = j.Steps[1], j.Steps[0] }, message: "exactly 4 steps"},
		{name: "second step not summary", mutate: func(j *SetupJourney) { j.Steps[1].Kind = SetupStepProjectConnect }, message: "kind must be"},
		{name: "account kind", mutate: func(j *SetupJourney) { j.Steps[1].Kind = SetupStepAccountLink }, message: "kind must be"},
		{name: "duplicate step id", mutate: func(j *SetupJourney) { j.Steps[1].ID = j.Steps[0].ID }, message: "duplicated"},
		{name: "markup title", mutate: func(j *SetupJourney) { j.Steps[0].Title = "<b>Install</b>" }, message: "plain display text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			declaration := validIntegrationInstallSetupJourney()
			test.mutate(&declaration)
			_, err := NormalizeSetupJourney(declaration)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want it to mention %q", err, test.message)
			}
		})
	}
}

func TestNormalizeSetupJourneyCannotMixShapes(t *testing.T) {
	// Project-setup references on an account-link sequence are rejected, and an
	// account-link or install kind inside the project-setup sequence is
	// rejected, so no shape can borrow another's steps or fields.
	mixed := validSetupJourney()
	mixed.WorkspaceLaunch = nil
	mixed.Steps = validAccountLinkSetupJourney().Steps
	if _, err := NormalizeSetupJourney(mixed); err == nil {
		t.Fatal("account-link steps with project-setup references were accepted")
	}
	withAccountKind := validSetupJourney()
	withAccountKind.Steps[2].Kind = SetupStepAccountLink
	if _, err := NormalizeSetupJourney(withAccountKind); err == nil {
		t.Fatal("project-setup sequence with an account-link kind was accepted")
	}
	withInstallKind := validSetupJourney()
	withInstallKind.Steps[1].Kind = SetupStepIntegrationInstall
	if _, err := NormalizeSetupJourney(withInstallKind); err == nil {
		t.Fatal("project-setup sequence with an install kind was accepted")
	}
	installWithLaunch := validIntegrationInstallSetupJourney()
	installWithLaunch.WorkspaceLaunch = &WorkspaceLaunchCopy{GroupTitle: "Group", GroupName: "Group"}
	if _, err := NormalizeSetupJourney(installWithLaunch); err == nil {
		t.Fatal("install sequence with launch copy was accepted")
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
	object["shape"] = "project_setup"
	withShape, _ := json.Marshal(object)
	if _, err := ParseSetupJourney(withShape); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("authored shape field error = %v", err)
	}
}

func TestSetupJourneyShapeIsInferredFromExactSequence(t *testing.T) {
	project := validSetupJourney()
	if got := project.Shape(); got != SetupJourneyShapeProjectSetup {
		t.Fatalf("project-setup shape = %q", got)
	}
	account := validAccountLinkSetupJourney()
	if got := account.Shape(); got != SetupJourneyShapeAccountLink {
		t.Fatalf("account-link shape = %q", got)
	}
	account.Steps = account.Steps[:3]
	if got := account.Shape(); got != "" {
		t.Fatalf("truncated sequence shape = %q, want none", got)
	}
	install := validIntegrationInstallSetupJourney()
	if got := install.Shape(); got != SetupJourneyShapeIntegrationInstall {
		t.Fatalf("install shape = %q", got)
	}
	// The retired five-step sequence matches no shape.
	var retired SetupJourney
	if err := json.Unmarshal([]byte(retiredFiveStepDeclaration), &retired); err != nil {
		t.Fatal(err)
	}
	if got := retired.Shape(); got != "" {
		t.Fatalf("retired five-step shape = %q, want none", got)
	}
	var missing *SetupJourney
	if got := missing.Shape(); got != "" {
		t.Fatalf("nil declaration shape = %q", got)
	}
	steps := SetupJourneyShapeSteps(SetupJourneyShapeProjectSetup)
	steps[0] = "mutated"
	if SetupJourneyShapeSteps(SetupJourneyShapeProjectSetup)[0] != SetupStepProjectConnect {
		t.Fatal("shape steps were mutated through a returned slice")
	}
	for _, retiredShape := range []SetupJourneyShape{"specialist", "custom"} {
		if SetupJourneyShapeSteps(retiredShape) != nil {
			t.Fatalf("shape %q returned steps", retiredShape)
		}
	}
}

// FR 1: exactly three shapes compile, each with its fixed order.
func TestExactlyThreeShapesCompile(t *testing.T) {
	want := map[SetupJourneyShape][]SetupStepKind{
		SetupJourneyShapeIntegrationInstall: {SetupStepIntegrationInstall, SetupStepSummary},
		SetupJourneyShapeProjectSetup:       {SetupStepProjectConnect, SetupStepWorkspaceSetup, SetupStepAssistantProgramStaffing, SetupStepSummary},
		SetupJourneyShapeAccountLink:        {SetupStepWorkspaceCreate, SetupStepAccountConnect, SetupStepAccountLink, SetupStepSummary},
	}
	if len(compiledSetupJourneyShapes) != len(want) {
		t.Fatalf("compiled shapes = %v", compiledSetupJourneyShapes)
	}
	for _, shape := range compiledSetupJourneyShapes {
		if got := SetupJourneyShapeSteps(shape); !reflect.DeepEqual(got, want[shape]) {
			t.Fatalf("%s steps = %v, want %v", shape, got, want[shape])
		}
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
	// FR 3: launch copy is the group screen only.
	wantLaunch := []string{"group_name", "group_title"}
	if got := tags(WorkspaceLaunchCopy{}); !reflect.DeepEqual(got, wantLaunch) {
		t.Fatalf("WorkspaceLaunchCopy JSON tags = %v, want %v", got, wantLaunch)
	}
}

func TestProjectSetupNormalizationIsIdempotent(t *testing.T) {
	first, err := NormalizeSetupJourney(validSetupJourney())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(first)
	again, err := NormalizeSetupJourney(*first)
	if err != nil {
		t.Fatal(err)
	}
	if reencoded, _ := json.Marshal(again); !bytes.Equal(reencoded, encoded) {
		t.Fatal("project-setup normalization is not idempotent")
	}
}
