package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func independentHomeContributionJSON(t *testing.T) []byte {
	t.Helper()
	return []byte(`{
  "schema_version": 1,
  "name": "music-project-management",
  "version": "0.1.0",
  "protocol": {"min": 1, "max": 1},
  "requires_host_features": ["independent_program_homes_v1"],
  "capabilities": [],
  "services": [],
  "blueprints": [],
  "assistant_program_homes": [{
    "schema_version": 1,
    "version": 1,
    "id": "music-producer-assistant",
    "station_name": "Music Production Home",
    "station_description": "Coordinates reviewed portfolio work.",
    "default_primary_name": "Portfolio Manager",
    "hire_title": "Staff your music production Home",
    "hire_description": "Add Home roles separately.",
    "disabled_message": "Music project management is unavailable.",
    "suggestion_required_capabilities": [],
    "roles": [
      {"id":"portfolio_manager","label":"Music Portfolio Manager","required":true,"primary":true,"role":"orchestrator","system_prompt":"Coordinate only reviewed Home work.","skills":["music-project-management"]},
      {"id":"sample_library_manager","label":"Sample Library Manager","required":false,"capability_id":"sample-library","role":"specialist","system_prompt":"Use only reviewed sample-library operations."}
    ],
    "stages": [{"id":"foundation","label":"Foundation","accepted_completion_threshold":0}],
    "reflection": {"minimum_projects":3,"cadence_hours":168,"max_projects":16,"max_events_per_project":16,"max_candidates":8,"max_evidence":8,"rubric":"Propose bounded improvements from approved evidence."},
    "allowed_project_attachments": [{"provider_plugin_id":"reaper-plugin","blueprint_id":"reaper-song","project_team_id":"reaper-song-team","project_team_schema_version":1,"min_project_team_version":1,"max_project_team_version":1}]
  }]
}`)
}

func TestIndependentAssistantProgramHomeAcceptsContentOnlyContribution(t *testing.T) {
	contribution, err := ParseSurfaceContribution(independentHomeContributionJSON(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(contribution.AssistantProgramHomes) != 1 || len(contribution.Capabilities) != 0 || len(contribution.Services) != 0 || len(contribution.Blueprints) != 0 {
		t.Fatalf("content-only contribution = %+v", contribution)
	}
	home := contribution.AssistantProgramHomes[0]
	declaration := home.AssistantProgram()
	if declaration.SchemaVersion != workspace.AssistantProgramSchemaVersion || len(declaration.Roles) != 2 {
		t.Fatalf("durable declaration = %+v", declaration)
	}
	for _, role := range declaration.Roles {
		if role.Scope != workspace.AssistantRoleScopeHome {
			t.Fatalf("role %q scope = %q", role.ID, role.Scope)
		}
	}
	public := contribution.Public()
	if len(public.AssistantProgramHomes) != 1 || public.AssistantProgramHomes[0].ID != home.ID {
		t.Fatalf("public projection = %+v", public)
	}
	report := BuildTrustReport(PluginDescriptor{Name: contribution.Name, SourceFormat: FormatClaude, WorkspaceSurfaces: contribution})
	if len(report.AssistantProgramHomes) != 1 || !strings.Contains(report.String(), "music-producer-assistant") {
		t.Fatalf("trust report = %+v\n%s", report, report.String())
	}
}

func TestIndependentAssistantProgramHomeFailsClosed(t *testing.T) {
	base := independentHomeContributionJSON(t)
	var raw map[string]any
	if err := json.Unmarshal(base, &raw); err != nil {
		t.Fatal(err)
	}
	homes := raw["assistant_program_homes"].([]any)
	home := homes[0].(map[string]any)

	tests := map[string]func(map[string]any){
		"feature omitted": func(root map[string]any) { root["requires_host_features"] = []any{} },
		"unknown role scope": func(_ map[string]any) {
			home["roles"].([]any)[0].(map[string]any)["scope"] = "home"
		},
		"duplicate home": func(root map[string]any) { root["assistant_program_homes"] = append(homes, home) },
		"invalid attachment range": func(_ map[string]any) {
			attachment := home["allowed_project_attachments"].([]any)[0].(map[string]any)
			attachment["min_project_team_version"] = float64(2)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var candidate map[string]any
			encoded, _ := json.Marshal(raw)
			_ = json.Unmarshal(encoded, &candidate)
			// Mutators close over nested values for concise fixture construction;
			// rebuild those references for the candidate where needed.
			candidateHomes := candidate["assistant_program_homes"].([]any)
			candidateHome := candidateHomes[0].(map[string]any)
			switch name {
			case "unknown role scope":
				candidateHome["roles"].([]any)[0].(map[string]any)["scope"] = "home"
			case "invalid attachment range":
				candidateHome["allowed_project_attachments"].([]any)[0].(map[string]any)["min_project_team_version"] = float64(2)
			default:
				mutate(candidate)
			}
			payload, _ := json.Marshal(candidate)
			if _, err := ParseSurfaceContribution(payload); err == nil {
				t.Fatal("expected strict rejection")
			}
		})
	}
}

func TestIndependentAssistantProgramHomeChangesTrustedFingerprint(t *testing.T) {
	contribution, err := ParseSurfaceContribution(independentHomeContributionJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	original := PluginDescriptor{Name: contribution.Name, WorkspaceSurfaces: contribution}
	changed := original
	cloneJSON, _ := json.Marshal(contribution)
	var clone SurfaceContribution
	if err := json.Unmarshal(cloneJSON, &clone); err != nil {
		t.Fatal(err)
	}
	clone.AssistantProgramHomes[0].AllowedProjectAttachments[0].MaxProjectTeamVersion = 2
	changed.WorkspaceSurfaces = &clone
	if trustedComponentFingerprint(original) == trustedComponentFingerprint(changed) {
		t.Fatal("attachment authorization change did not change trusted fingerprint")
	}
}
