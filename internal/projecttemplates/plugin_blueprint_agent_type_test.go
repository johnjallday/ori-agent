package projecttemplates

import (
	"os"
	"path/filepath"
	"testing"
)

// pluginBlueprintManifestWithAgentType mirrors the shape of an installed plugin
// blueprint (the REAPER plugin's roles spell the retired key "tool_calling").
// extraRoleKey, when set, adds one more key to the first role.
func pluginBlueprintManifestWithAgentType(extraRoleKey string) string {
	extra := ""
	if extraRoleKey != "" {
		extra = `"` + extraRoleKey + `": "x",`
	}
	return `{
  "name": "Typed Blueprint",
  "description": "A plugin blueprint that still declares the retired agent type.",
  "agents": [
    {"name": "Lead", "role": "orchestrator", "type": "research", "system_prompt": "Lead the project."}
  ],
  "assistant_program": {
    "schema_version": 2,
    "id": "typed-program",
    "station_name": "Typed Home",
    "default_primary_name": "Coordinator",
    "hire_title": "Staff the program",
    "roles": [
      {
        "id": "coordinator", "label": "Coordinator", "scope": "home",
        "required": true, "primary": true, "role": "orchestrator",
        ` + extra + `
        "type": "tool_calling",
        "system_prompt": "Coordinate the Home."
      },
      {
        "id": "project_lead", "label": "Project Lead", "scope": "project",
        "required": true, "primary": true, "role": "orchestrator",
        "type": "general",
        "system_prompt": "Lead one project."
      }
    ],
    "stages": [{"id": "helper", "label": "Helper", "accepted_completion_threshold": 0}],
    "reflection": {
      "minimum_projects": 3, "cadence_hours": 24, "max_projects": 8,
      "max_events_per_project": 8, "max_candidates": 4, "max_evidence": 4,
      "rubric": "Find stable preferences in reviewed evidence."
    }
  }
}`
}

func writePluginBlueprint(t *testing.T, manifest string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "template.json")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	skeleton := filepath.Join(dir, "project")
	if err := os.MkdirAll(skeleton, 0o755); err != nil {
		t.Fatalf("create skeleton: %v", err)
	}
	return manifestPath, skeleton
}

func TestLoadPluginBlueprint_AcceptsRetiredAgentTypeKey(t *testing.T) {
	manifestPath, skeleton := writePluginBlueprint(t, pluginBlueprintManifestWithAgentType(""))

	tpl, _, err := LoadPluginBlueprint(manifestPath, skeleton, defaultRuntimeCatalog())
	if err != nil {
		t.Fatalf("a blueprint declaring the retired type key must still load: %v", err)
	}
	if len(tpl.Agents) != 1 || tpl.Agents[0].Name != "Lead" {
		t.Fatalf("agents = %#v", tpl.Agents)
	}
	if tpl.AssistantProgram == nil || len(tpl.AssistantProgram.Roles) != 2 {
		t.Fatalf("assistant program = %#v", tpl.AssistantProgram)
	}
}

// The test above only proves something if the decoder is still strict: an
// unknown key that is not the retired type must be rejected.
func TestLoadPluginBlueprint_StillRejectsUnknownRoleKeys(t *testing.T) {
	manifestPath, skeleton := writePluginBlueprint(t, pluginBlueprintManifestWithAgentType("not_a_real_field"))

	if _, _, err := LoadPluginBlueprint(manifestPath, skeleton, defaultRuntimeCatalog()); err == nil {
		t.Fatal("expected the strict assistant-program decoder to reject an unknown role key")
	}
}
