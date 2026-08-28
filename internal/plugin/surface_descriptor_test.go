package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func canonicalSurfaceFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "workspace-surface-v1", "valid-contribution.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseSurfaceContributionAcceptsCanonicalFixtureAndBuildsInertPublicProjection(t *testing.T) {
	contribution, err := ParseSurfaceContribution(canonicalSurfaceFixture(t))
	if err != nil {
		t.Fatalf("ParseSurfaceContribution() error = %v", err)
	}
	if contribution.SchemaVersion != 1 || contribution.Protocol.Min != 1 || contribution.Protocol.Max != 1 {
		t.Fatalf("protocol = %+v", contribution.Protocol)
	}
	if len(contribution.Capabilities) != 1 || len(contribution.Services) != 1 || len(contribution.Blueprints) != 1 {
		t.Fatalf("contribution components = %+v", contribution)
	}
	publicJSON, err := json.Marshal(contribution.Public())
	if err != nil {
		t.Fatal(err)
	}
	public := string(publicJSON)
	for _, forbidden := range []string{
		"entry_asset", "artifact", "sha256", "input_schema", "output_schema",
		"mcp_stdio", "status.read", "plugin_data_write", "blueprints/demo-workspace",
	} {
		if strings.Contains(public, forbidden) {
			t.Fatalf("public projection leaked %q: %s", forbidden, public)
		}
	}
}

func TestParseSurfaceContributionRejectsUnknownExecutableField(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(canonicalSurfaceFixture(t), &document); err != nil {
		t.Fatal(err)
	}
	document["command"] = "/tmp/plugin-service"
	data, _ := json.Marshal(document)
	if _, err := ParseSurfaceContribution(data); !ContributionErrorIs(err, CodeContributionInvalid) {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestSurfaceContributionStableValidationCodes(t *testing.T) {
	valid := func(t *testing.T) map[string]any {
		t.Helper()
		var document map[string]any
		if err := json.Unmarshal(canonicalSurfaceFixture(t), &document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
		code   ContributionErrorCode
	}{
		{
			name: "protocol range",
			mutate: func(document map[string]any) {
				document["protocol"] = map[string]any{"min": 2, "max": 1}
			},
			code: CodeProtocolRangeInvalid,
		},
		{
			name: "unsupported protocol",
			mutate: func(document map[string]any) {
				document["protocol"] = map[string]any{"min": 2, "max": 3}
			},
			code: CodeProtocolIncompatible,
		},
		{
			name: "duplicate capability",
			mutate: func(document map[string]any) {
				capabilities := document["capabilities"].([]any)
				document["capabilities"] = append(capabilities, capabilities[0])
			},
			code: CodeComponentDuplicate,
		},
		{
			name: "unsafe entry asset",
			mutate: func(document map[string]any) {
				capability := document["capabilities"].([]any)[0].(map[string]any)
				surface := capability["surfaces"].([]any)[0].(map[string]any)
				surface["entry_asset"] = "../../outside.html"
			},
			code: CodeAssetPathInvalid,
		},
		{
			name: "open input schema",
			mutate: func(document map[string]any) {
				service := document["services"].([]any)[0].(map[string]any)
				operation := service["operations"].([]any)[0].(map[string]any)
				schema := operation["input_schema"].(map[string]any)
				schema["additionalProperties"] = true
			},
			code: CodeOperationSchemaInvalid,
		},
		{
			name: "raw scope",
			mutate: func(document map[string]any) {
				service := document["services"].([]any)[0].(map[string]any)
				operation := service["operations"].([]any)[0].(map[string]any)
				operation["scopes"] = []any{"/Users/example/Music"}
			},
			code: CodeScopeUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := valid(t)
			test.mutate(document)
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseSurfaceContribution(data); !ContributionErrorIs(err, test.code) {
				t.Fatalf("error = %v, want code %s", err, test.code)
			}
		})
	}
}

func TestCheckedInProjectEntryProtocolFixtureParses(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "workspace-surface-v1", "valid-project-entry-contribution.json"))
	if err != nil {
		t.Fatal(err)
	}
	contribution, err := ParseSurfaceContribution(data)
	if err != nil {
		t.Fatal(err)
	}
	surface := contribution.Capabilities[0].Surfaces[0]
	if surface.Placement != "project_entry" || surface.DefaultTaskTemplate != "survey" || len(surface.TaskTemplates) != 1 {
		t.Fatalf("project entry fixture = %+v", surface)
	}
}

func TestProjectEntryTaskTemplatesAreFixedClosedAndInertPublicly(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(canonicalSurfaceFixture(t), &document); err != nil {
		t.Fatal(err)
	}
	surface := document["capabilities"].([]any)[0].(map[string]any)["surfaces"].([]any)[0].(map[string]any)
	surface["placement"] = "project_entry"
	surface["default_task_template"] = "survey"
	surface["task_templates"] = []any{map[string]any{
		"id": "survey", "label": "Run survey", "description": "Create the fixed survey task.",
		"title": "Survey project", "instructions": "Run the canonical survey workflow.",
		"required_capabilities": []any{"demo_runtime"}, "auto_start": true,
		"input_schema": map[string]any{
			"type": "object", "properties": map[string]any{}, "required": []any{}, "additionalProperties": false,
		},
	}}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	contribution, err := ParseSurfaceContribution(data)
	if err != nil {
		t.Fatalf("project entry contribution error = %v", err)
	}
	got := contribution.Capabilities[0].Surfaces[0]
	if got.Placement != "project_entry" || got.DefaultTaskTemplate != "survey" || len(got.TaskTemplates) != 1 || !got.TaskTemplates[0].AutoStart {
		t.Fatalf("task template = %+v", got)
	}
	public, _ := json.Marshal(contribution.Public())
	for _, secret := range []string{"Run the canonical survey workflow", "required_capabilities", "auto_start", "input_schema"} {
		if strings.Contains(string(public), secret) {
			t.Fatalf("public contribution leaked task authority %q: %s", secret, public)
		}
	}

	invalidCases := map[string]func(map[string]any){
		"file fallback":      func(template map[string]any) { template["file_fallback_for"] = []any{"demo_runtime"} },
		"arbitrary assignee": func(template map[string]any) { template["assignee"] = "plugin-picked" },
		"foreign capability": func(template map[string]any) { template["required_capabilities"] = []any{"admin_runtime"} },
		"open variables": func(template map[string]any) {
			template["input_schema"].(map[string]any)["additionalProperties"] = true
		},
	}
	for name, mutate := range invalidCases {
		t.Run(name, func(t *testing.T) {
			var clone map[string]any
			if err := json.Unmarshal(data, &clone); err != nil {
				t.Fatal(err)
			}
			template := clone["capabilities"].([]any)[0].(map[string]any)["surfaces"].([]any)[0].(map[string]any)["task_templates"].([]any)[0].(map[string]any)
			mutate(template)
			encoded, _ := json.Marshal(clone)
			if _, err := ParseSurfaceContribution(encoded); err == nil {
				t.Fatalf("invalid task template was accepted")
			}
		})
	}
}

func TestValidateContributionIdentityRequiresExactPortableIdentity(t *testing.T) {
	contribution, err := ParseSurfaceContribution(canonicalSurfaceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	valid := []ManifestIdentity{
		{Format: FormatClaude, Name: "workspace-surface-demo", Version: "0.1.0"},
		{Format: FormatCodex, Name: "WORKSPACE-SURFACE-DEMO", Version: "0.1.0"},
	}
	if err := ValidateContributionIdentity(contribution, valid); err != nil {
		t.Fatalf("valid identity error = %v", err)
	}
	if err := ValidateContributionIdentity(contribution, nil); !ContributionErrorIs(err, CodeBaseManifestRequired) {
		t.Fatalf("missing base error = %v", err)
	}
	valid[1].Version = "0.2.0"
	if err := ValidateContributionIdentity(contribution, valid); !ContributionErrorIs(err, CodeIdentityMismatch) {
		t.Fatalf("version mismatch error = %v", err)
	}
}

func TestWorkspaceSurfaceJSONSchemaIsValidAndClosedAtTopLevel(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("schema", "workspace-surface-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("schema JSON is invalid: %v", err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["additionalProperties"] != false {
		t.Fatalf("schema root is not draft 2020-12 and closed: %+v", schema)
	}
}
