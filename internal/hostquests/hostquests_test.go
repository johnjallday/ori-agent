package hostquests

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

func TestEmailOpsSetupQuestIsCompiledInAccountLinkShape(t *testing.T) {
	quest, ok := Get(EmailOpsSetupQuestID)
	if !ok {
		t.Fatal("email ops setup quest is not compiled in")
	}
	if quest.Shape() != specialist.SetupJourneyShapeAccountLink || quest.ExpectedBlueprintID != "email-ops" ||
		quest.IntegrationKey != "" || quest.ExpectedAssistantProgramID != "" || quest.WorkspaceLaunch != nil {
		t.Fatalf("unexpected email ops quest: %+v", quest)
	}
	wantSteps := []string{"team", "connect", "mailbox", "summary"}
	for index, id := range wantSteps {
		if quest.Steps[index].ID != id {
			t.Fatalf("step %d id = %q, want %q", index, quest.Steps[index].ID, id)
		}
	}
	// FR 22: plain host copy never promises the workspace works alone.
	var copyText strings.Builder
	copyText.WriteString(strings.ToLower(quest.Title + " " + quest.Description))
	for _, step := range quest.Steps {
		copyText.WriteString(" " + strings.ToLower(step.Title+" "+step.Description))
	}
	for _, forbidden := range []string{"automatically send", "sends for you", "reaper"} {
		if strings.Contains(copyText.String(), forbidden) {
			t.Fatalf("host quest copy contains %q", forbidden)
		}
	}
	if EmailOpsSetupQuestURL != "/?setup=quest&source=host&quest=email_ops_setup" {
		t.Fatalf("launch URL = %q", EmailOpsSetupQuestURL)
	}
}

func TestAllAndGetReturnIndependentCopies(t *testing.T) {
	all := All()
	if len(all) != 1 || all[0].ID != EmailOpsSetupQuestID {
		t.Fatalf("All() = %+v", all)
	}
	all[0].Title = "mutated"
	all[0].Steps[0].Title = "mutated"
	fresh, _ := Get(EmailOpsSetupQuestID)
	if fresh.Title == "mutated" || fresh.Steps[0].Title == "mutated" {
		t.Fatal("host quest state was mutated through a returned copy")
	}
	if _, ok := Get("missing"); ok {
		t.Fatal("Get returned an unknown quest")
	}
}

func TestInvalidEmbeddedDeclarationFailsStartup(t *testing.T) {
	valid, err := declarationFiles.ReadFile("email-ops-setup.json")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]fstest.MapFS{
		"malformed":        {"bad.json": {Data: []byte(`{"schema_version":1`)}},
		"unknown field":    {"bad.json": {Data: []byte(strings.Replace(string(valid), `"version": 1,`, `"version": 1, "adapter": "run",`, 1))}},
		"specialist shape": {"bad.json": {Data: []byte(specialistDeclarationJSON)}},
		"duplicate id":     {"a.json": {Data: valid}, "b.json": {Data: valid}},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := load(files); err == nil {
				t.Fatal("invalid host declaration loaded")
			}
			defer func() {
				if recover() == nil {
					t.Fatal("mustLoad did not fail startup")
				}
			}()
			mustLoad(files)
		})
	}
}

const specialistDeclarationJSON = `{
  "schema_version": 1, "version": 1, "id": "not_host", "title": "Not host", "description": "Specialist shape.",
  "integration_key": "ori_reaper", "expected_blueprint_id": "reaper-song", "expected_assistant_program_id": "music-producer-assistant",
  "steps": [
    {"id": "integration", "kind": "integration_install", "title": "Integration", "description": "Install."},
    {"id": "project", "kind": "project_connect", "title": "Project", "description": "Connect."},
    {"id": "workspace", "kind": "workspace_setup", "title": "Workspace", "description": "Choose."},
    {"id": "staffing", "kind": "assistant_program_staffing", "title": "Staffing", "description": "Staff."},
    {"id": "summary", "kind": "summary", "title": "Summary", "description": "Review."}
  ]
}`
