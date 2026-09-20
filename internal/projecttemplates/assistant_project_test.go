package projecttemplates

import (
	"encoding/json"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func splitProjectJSON(extra string) json.RawMessage {
	return json.RawMessage(`{
		"schema_version":1,
		"version":3,
		"id":"reaper_team",
		"home":{"provider_plugin_id":"music-project-management","program_id":"music_home","home_schema_version":1,"min_home_version":2,"max_home_version":2},
		"roles":[{"id":"engineer","label":"REAPER Engineer","required":true,"primary":true,"system_prompt":"Operate only the exact linked project.","skills":["reaper-project"]}]` + extra + `}`)
}

func TestNormalizeAssistantProjectKeepsOnlyProjectOwnedAuthority(t *testing.T) {
	project, err := normalizeAssistantProject(splitProjectJSON(""))
	if err != nil {
		t.Fatal(err)
	}
	if project.ID != "reaper_team" || project.Home.ProviderPluginID != "music-project-management" || len(project.Roles) != 1 {
		t.Fatalf("project declaration = %#v", project)
	}
	roles := project.ProgramRoles()
	if len(roles) != 1 || roles[0].Scope != workspace.AssistantRoleScopeProject || roles[0].Skills[0] != "reaper-project" {
		t.Fatalf("project role projection = %#v", roles)
	}
	if AssistantProjectDigest(project) == "" {
		t.Fatal("project declaration has no stable digest")
	}
}

func TestNormalizeAssistantProjectAcceptsMaximumRoleCount(t *testing.T) {
	project := AssistantProjectDeclaration{
		SchemaVersion: AssistantProjectSchemaVersion, Version: 1, ID: "project_team",
		Home: AssistantProjectHomeReference{ProviderPluginID: "music", ProgramID: "music_home", HomeSchemaVersion: 1, MinHomeVersion: 1, MaxHomeVersion: 1},
	}
	for index := 0; index < workspace.AssistantProgramMaxRoles; index++ {
		project.Roles = append(project.Roles, AssistantProjectRole{
			ID: "project_role_" + string(rune('a'+index)), Label: "Project Role", Required: true,
			Primary: index == 0, SystemPrompt: "Work only in this project.",
		})
	}
	raw, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeAssistantProject(raw)
	if err != nil {
		t.Fatalf("maximum project roles rejected: %v", err)
	}
	if len(normalized.Roles) != workspace.AssistantProgramMaxRoles {
		t.Fatalf("role count = %d", len(normalized.Roles))
	}
}

func TestNormalizeAssistantProjectRejectsHomeAuthorityAndRoleAmbiguity(t *testing.T) {
	if _, err := normalizeAssistantProject(splitProjectJSON(`,"station_name":"Unauthorized Home"`)); err == nil {
		t.Fatal("assistant_project accepted Home display authority")
	}
	duplicate := json.RawMessage(`{
		"schema_version":1,"version":1,"id":"reaper_team",
		"home":{"provider_plugin_id":"music","program_id":"music_home","home_schema_version":1,"min_home_version":1,"max_home_version":1},
		"roles":[
			{"id":"engineer","label":"One","required":true,"primary":true,"system_prompt":"First."},
			{"id":"engineer","label":"Two","required":true,"system_prompt":"Second."}
		]
	}`)
	if _, err := normalizeAssistantProject(duplicate); err == nil {
		t.Fatal("assistant_project accepted duplicate role identity")
	}
}

func TestSplitGroupAndStandaloneSchemasBindProjectIdentity(t *testing.T) {
	project, err := normalizeAssistantProject(splitProjectJSON(""))
	if err != nil {
		t.Fatal(err)
	}
	requirement, err := normalizeGroupRequirement(json.RawMessage(`{
		"schema_version":2,"policy":"recommended","assistant_project_id":"reaper_team",
		"missing_home":"offer_create","default_home_name":"Music Production Home"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	connection := &ProjectConnectionDeclaration{SchemaVersion: ProjectConnectionSchemaVersion, SupportedModes: []ProjectConnectionMode{ProjectConnectionNewProject}}
	standalone, err := normalizeStandaloneComposition(json.RawMessage(`{
		"schema_version":2,"project_roles":[{"role_id":"engineer","system_prompt":"Work only in this standalone REAPER project."}]
	}`), nil, project, connection, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateGroupComposition(requirement, standalone, nil, project); err != nil {
		t.Fatal(err)
	}
	template := Template{ID: "plugin:reaper:song", AssistantProject: project, GroupRequirement: requirement, StandaloneComposition: standalone}
	effective, err := StandaloneTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	if effective.AssistantProject != nil || effective.AssistantProgram != nil || effective.GroupRequirement != nil || len(effective.Agents) != 1 ||
		effective.Agents[0].Name != "REAPER Engineer" || effective.Agents[0].Tools.Skills[0] != "reaper-project" {
		t.Fatalf("standalone split transformation = %#v", effective)
	}

	requirement.AssistantProjectID = "other_team"
	if err := validateGroupComposition(requirement, standalone, nil, project); err == nil {
		t.Fatal("schema-2 group requirement accepted a different project team")
	}
}
