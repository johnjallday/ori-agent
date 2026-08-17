package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validRuntimeRequirementsManifest = `{
	"name": "Runtime Demo",
	"description": "The rest of the blueprint still loads.",
	"agents": [{"name": "Runtime Lead"}],
	"runtime_requirements": {
		"schema_version": 1,
		"operating_modes": [
			{
				"id": " File_Only ",
				"label": "  File-only  ",
				"description": "  Edit the project files without live control.  "
			},
			{
				"id": " Assisted ",
				"label": "  Assisted  ",
				"description": "  Control the external application after setup.  ",
				"requires": [" REAPER_LIVE_CONTROL "]
			}
		],
		"requirements": [
			{
				"key": " REAPER_LIVE_CONTROL ",
				"label": "  Local REAPER control  ",
				"description": "  Lets approved tasks control the open workspace project.  ",
				"disclosure": "  The selected agent may use loopback and Ori's dedicated runner exchange.  ",
				"adapter": " REAPER_LIVE_CONTROL "
			}
		]
	}
}`

// TestNewTemplate_RuntimeRequirementsPublicJSONContract pins the public,
// domain-neutral manifest/API shape before the implementation exists. The
// declaration contains only stable IDs, text, references, and a compiled
// adapter lookup key; none of these fields can supply behavior.
func TestNewTemplate_RuntimeRequirementsPublicJSONContract(t *testing.T) {
	tpl := loadTemplateWithManifest(t, validRuntimeRequirementsManifest)

	if tpl.RuntimeRequirementsError != "" {
		t.Fatalf("valid runtime requirements reported an error: %s", tpl.RuntimeRequirementsError)
	}
	if !tpl.HasRuntimeRequirements() || tpl.HasInvalidRuntimeRequirements() {
		t.Fatalf("HasRuntimeRequirements=%v HasInvalidRuntimeRequirements=%v", tpl.HasRuntimeRequirements(), tpl.HasInvalidRuntimeRequirements())
	}
	contract := tpl.RuntimeRequirements
	if contract.SchemaVersion != RuntimeRequirementsSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", contract.SchemaVersion, RuntimeRequirementsSchemaVersion)
	}
	if len(contract.OperatingModes) != 2 || len(contract.Requirements) != 1 {
		t.Fatalf("runtime contract shape changed: %+v", contract)
	}

	limited := contract.OperatingModes[0]
	if limited.ID != "file_only" || limited.Label != "File-only" || limited.Description != "Edit the project files without live control." {
		t.Fatalf("limited mode was not normalized as inert text: %+v", limited)
	}
	if len(limited.Requires) != 0 {
		t.Fatalf("limited mode unexpectedly requires runtime setup: %v", limited.Requires)
	}

	assisted := contract.OperatingModes[1]
	if assisted.ID != "assisted" || assisted.Label != "Assisted" || assisted.Description != "Control the external application after setup." {
		t.Fatalf("assisted mode was not normalized: %+v", assisted)
	}
	if len(assisted.Requires) != 1 || assisted.Requires[0] != "reaper_live_control" {
		t.Fatalf("mode-to-requirement reference changed: %v", assisted.Requires)
	}

	requirement := contract.Requirements[0]
	if requirement.Key != "reaper_live_control" || requirement.Adapter != "reaper_live_control" {
		t.Fatalf("stable requirement/adapter keys changed: %+v", requirement)
	}
	if requirement.Label != "Local REAPER control" || requirement.Description != "Lets approved tasks control the open workspace project." {
		t.Fatalf("requirement text was not normalized: %+v", requirement)
	}
	if requirement.Disclosure != "The selected agent may use loopback and Ori's dedicated runner exchange." {
		t.Fatalf("requirement disclosure changed: %q", requirement.Disclosure)
	}

	// Template is the list/detail API representation. Marshal it to pin the
	// external field names, including the distinction between operating modes,
	// their references, and requirement metadata.
	encoded, err := json.Marshal(tpl)
	if err != nil {
		t.Fatalf("marshal normalized template: %v", err)
	}
	var public struct {
		RuntimeRequirements struct {
			SchemaVersion  int `json:"schema_version"`
			OperatingModes []struct {
				ID          string   `json:"id"`
				Label       string   `json:"label"`
				Description string   `json:"description"`
				Requires    []string `json:"requires"`
			} `json:"operating_modes"`
			Requirements []struct {
				Key         string `json:"key"`
				Label       string `json:"label"`
				Description string `json:"description"`
				Disclosure  string `json:"disclosure"`
				Adapter     string `json:"adapter"`
			} `json:"requirements"`
		} `json:"runtime_requirements"`
	}
	if err := json.Unmarshal(encoded, &public); err != nil {
		t.Fatalf("decode public template JSON: %v", err)
	}
	if public.RuntimeRequirements.SchemaVersion != 1 || len(public.RuntimeRequirements.OperatingModes) != 2 || len(public.RuntimeRequirements.Requirements) != 1 {
		t.Fatalf("public JSON omitted the runtime contract: %s", encoded)
	}
	if got := public.RuntimeRequirements.OperatingModes[1].Requires; len(got) != 1 || got[0] != "reaper_live_control" {
		t.Fatalf("public JSON lost mode references: %s", encoded)
	}
	if got := public.RuntimeRequirements.Requirements[0]; got.Key != "reaper_live_control" || got.Adapter != "reaper_live_control" || got.Disclosure == "" {
		t.Fatalf("public JSON lost requirement metadata: %s", encoded)
	}
}

func TestNewTemplate_RuntimeRequirementsSchemaAndReferencesFailClosed(t *testing.T) {
	cases := []struct {
		name     string
		contract string
		contains string
	}{
		{
			name:     "missing schema version",
			contract: `{"operating_modes":[{"id":"default","label":"Default","description":"Use the blueprint."}],"requirements":[]}`,
			contains: "schema_version is required",
		},
		{
			name:     "unsupported schema version",
			contract: `{"schema_version":2,"operating_modes":[{"id":"default","label":"Default","description":"Use the blueprint."}],"requirements":[]}`,
			contains: "unsupported schema_version 2",
		},
		{
			name:     "no operating modes",
			contract: `{"schema_version":1,"operating_modes":[],"requirements":[]}`,
			contains: "at least one operating mode",
		},
		{
			name: "duplicate normalized mode id",
			contract: `{"schema_version":1,"operating_modes":[
				{"id":"file_only","label":"File-only","description":"Edit files."},
				{"id":" File_Only ","label":"Duplicate","description":"Duplicate."}],"requirements":[]}`,
			contains: `duplicate operating mode id "file_only"`,
		},
		{
			name:     "path-shaped mode id",
			contract: `{"schema_version":1,"operating_modes":[{"id":"../live","label":"Live","description":"Control live."}],"requirements":[]}`,
			contains: "must be lower-case letters",
		},
		{
			name:     "blank requirement key",
			contract: `{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":"Use it."}],"requirements":[{"key":" ","label":"Runtime","description":"Configure it.","adapter":"reaper_live_control"}]}`,
			contains: "requirement key is required",
		},
		{
			name: "duplicate normalized requirement key",
			contract: `{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":"Use it.","requires":["runtime"]}],"requirements":[
				{"key":"runtime","label":"Runtime","description":"Configure it.","adapter":"reaper_live_control"},
				{"key":" Runtime ","label":"Again","description":"Again.","adapter":"reaper_live_control"}]}`,
			contains: `duplicate runtime requirement key "runtime"`,
		},
		{
			name:     "undeclared mode reference",
			contract: `{"schema_version":1,"operating_modes":[{"id":"live","label":"Live","description":"Control live.","requires":["missing"]}],"requirements":[]}`,
			contains: `references undeclared runtime requirement "missing"`,
		},
		{
			name:     "unknown adapter",
			contract: `{"schema_version":1,"operating_modes":[{"id":"live","label":"Live","description":"Control live.","requires":["runtime"]}],"requirements":[{"key":"runtime","label":"Runtime","description":"Configure it.","adapter":"dynamic/module"}]}`,
			contains: `unregistered adapter "dynamic/module"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl := loadTemplateWithManifest(t, fmt.Sprintf(`{
				"name":"Still visible",
				"description":"Still loads",
				"agents":[{"name":"Lead"}],
				"runtime_requirements":%s
			}`, tc.contract))

			if tpl.RuntimeRequirements != nil {
				t.Fatalf("an invalid declaration must yield no partial contract: %+v", tpl.RuntimeRequirements)
			}
			if !tpl.HasInvalidRuntimeRequirements() {
				t.Fatal("an invalid declaration must be reported so creation can fail closed")
			}
			if !strings.Contains(tpl.RuntimeRequirementsError, tc.contains) {
				t.Fatalf("diagnostic %q does not mention %q", tpl.RuntimeRequirementsError, tc.contains)
			}
			if tpl.Name != "Still visible" || tpl.Description != "Still loads" || len(tpl.Agents) != 1 {
				t.Fatalf("one bad runtime block discarded unrelated manifest data: %+v", tpl)
			}
		})
	}
}

func TestNewTemplate_RuntimeRequirementsRejectBehaviorBearingAndUnknownFields(t *testing.T) {
	// FR-9's complete forbidden surface: a declaration can select one compiled
	// adapter and provide text, but cannot supply any executable behavior or
	// runtime location. DisallowUnknownFields makes these fail closed instead of
	// silently dropping what an author expected Ori to execute.
	forbidden := map[string]string{
		"script":           `"print('owned')"`,
		"command":          `"rm -rf /"`,
		"executable_path":  `"/bin/sh"`,
		"filesystem_path":  `"/tmp/runner"`,
		"url":              `"https://example.test/check"`,
		"host":             `"127.0.0.1"`,
		"port":             `8080`,
		"request_method":   `"POST"`,
		"headers":          `{"Authorization":"secret"}`,
		"payload":          `{"action":"run"}`,
		"module_path":      `"internal/adapter"`,
		"html":             `"<button>Run</button>"`,
		"custom_component": `"dangerous-check"`,
	}
	for field, value := range forbidden {
		t.Run(field, func(t *testing.T) {
			manifest := fmt.Sprintf(`{
				"name":"Forbidden",
				"agents":[{"name":"Lead"}],
				"runtime_requirements":{
					"schema_version":1,
					"operating_modes":[{"id":"assisted","label":"Assisted","description":"Use runtime.","requires":["runtime"]}],
					"requirements":[{"key":"runtime","label":"Runtime","description":"Configure it.","adapter":"reaper_live_control",%q:%s}]
				}
			}`, field, value)
			tpl := loadTemplateWithManifest(t, manifest)
			if !tpl.HasInvalidRuntimeRequirements() || tpl.RuntimeRequirements != nil {
				t.Fatalf("field %q must invalidate the entire contract: %+v", field, tpl.RuntimeRequirements)
			}
			if !strings.Contains(tpl.RuntimeRequirementsError, fmt.Sprintf(`unknown field %q`, field)) {
				t.Fatalf("field %q diagnostic = %q", field, tpl.RuntimeRequirementsError)
			}
		})
	}

	for _, tc := range []struct {
		name     string
		contract string
		contains string
	}{
		{name: "contract unknown field", contract: `{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":"Use it."}],"requirements":[],"on_ready":"run"}`, contains: `unknown field "on_ready"`},
		{name: "mode unknown field", contract: `{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":"Use it.","probe":"now"}],"requirements":[]}`, contains: `unknown field "probe"`},
		{name: "blank mode label", contract: `{"schema_version":1,"operating_modes":[{"id":"default","label":" ","description":"Use it."}],"requirements":[]}`, contains: `label is required`},
		{name: "blank mode description", contract: `{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":" "}],"requirements":[]}`, contains: `description is required`},
		{name: "blank requirement label", contract: `{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":"Use it."}],"requirements":[{"key":"runtime","label":" ","description":"Configure it.","adapter":"reaper_live_control"}]}`, contains: `label is required`},
		{name: "blank requirement description", contract: `{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":"Use it."}],"requirements":[{"key":"runtime","label":"Runtime","description":" ","adapter":"reaper_live_control"}]}`, contains: `description is required`},
		{name: "missing adapter", contract: `{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":"Use it."}],"requirements":[{"key":"runtime","label":"Runtime","description":"Configure it."}]}`, contains: `must name a registered adapter`},
		{name: "duplicate mode reference", contract: `{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":"Use it.","requires":["runtime"," RUNTIME "]}],"requirements":[{"key":"runtime","label":"Runtime","description":"Configure it.","adapter":"reaper_live_control"}]}`, contains: `more than once`},
		{name: "control character", contract: "{\"schema_version\":1,\"operating_modes\":[{\"id\":\"default\",\"label\":\"Default\",\"description\":\"bad\\u0007text\"}],\"requirements\":[]}", contains: `control character`},
		{name: "wrong field type", contract: `{"schema_version":1,"operating_modes":"default","requirements":[]}`, contains: `cannot unmarshal`},
		{name: "not an object", contract: `"enabled"`, contains: `cannot unmarshal`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tpl := loadTemplateWithManifest(t, fmt.Sprintf(`{"name":"Invalid","agents":[{"name":"Lead"}],"runtime_requirements":%s}`, tc.contract))
			if !tpl.HasInvalidRuntimeRequirements() || tpl.RuntimeRequirements != nil {
				t.Fatalf("invalid contract must fail closed: %+v / %q", tpl.RuntimeRequirements, tpl.RuntimeRequirementsError)
			}
			if !strings.Contains(tpl.RuntimeRequirementsError, tc.contains) {
				t.Fatalf("diagnostic %q does not contain %q", tpl.RuntimeRequirementsError, tc.contains)
			}
		})
	}
}

func TestValidateRuntimeRequirements_ErrorsAreIdentifiable(t *testing.T) {
	err := validateRuntimeRequirements(&runtimeRequirementsDecl{SchemaVersion: 9})
	if !errors.Is(err, ErrInvalidRuntimeRequirements) {
		t.Fatalf("error %v should wrap ErrInvalidRuntimeRequirements", err)
	}
	if err := validateRuntimeRequirements(nil); err != nil {
		t.Fatalf("an absent declaration is valid: %v", err)
	}
}

func TestNewTemplate_RuntimeRequirementCountsAreBounded(t *testing.T) {
	mode := func(i int) string {
		return fmt.Sprintf(`{"id":"mode_%d","label":"Mode %d","description":"Mode %d behavior."}`, i, i, i)
	}
	requirement := func(i int) string {
		return fmt.Sprintf(`{"key":"runtime_%d","label":"Runtime %d","description":"Configure runtime %d.","adapter":"reaper_live_control"}`, i, i, i)
	}

	modes := make([]string, maxRuntimeOperatingModes)
	for i := range modes {
		modes[i] = mode(i)
	}
	requirements := make([]string, maxRuntimeRequirements)
	refs := make([]string, maxRuntimeRequirements)
	for i := range requirements {
		requirements[i] = requirement(i)
		refs[i] = fmt.Sprintf(`"runtime_%d"`, i)
	}

	atModeLimit := loadTemplateWithManifest(t, fmt.Sprintf(`{"name":"Modes","agents":[{"name":"Lead"}],"runtime_requirements":{"schema_version":1,"operating_modes":[%s],"requirements":[]}}`, strings.Join(modes, ",")))
	if atModeLimit.RuntimeRequirementsError != "" || len(atModeLimit.RuntimeRequirements.OperatingModes) != maxRuntimeOperatingModes {
		t.Fatalf("the documented mode limit should be accepted: %q / %+v", atModeLimit.RuntimeRequirementsError, atModeLimit.RuntimeRequirements)
	}
	tooManyModes := append(append([]string(nil), modes...), mode(maxRuntimeOperatingModes))
	overModeLimit := loadTemplateWithManifest(t, fmt.Sprintf(`{"name":"Modes","agents":[{"name":"Lead"}],"runtime_requirements":{"schema_version":1,"operating_modes":[%s],"requirements":[]}}`, strings.Join(tooManyModes, ",")))
	if !strings.Contains(overModeLimit.RuntimeRequirementsError, fmt.Sprintf("exceeds the maximum of %d", maxRuntimeOperatingModes)) {
		t.Fatalf("over-limit modes must fail with a bound: %q", overModeLimit.RuntimeRequirementsError)
	}

	atRequirementLimit := loadTemplateWithManifest(t, fmt.Sprintf(`{"name":"Requirements","agents":[{"name":"Lead"}],"runtime_requirements":{"schema_version":1,"operating_modes":[{"id":"assisted","label":"Assisted","description":"Configure every runtime.","requires":[%s]}],"requirements":[%s]}}`, strings.Join(refs, ","), strings.Join(requirements, ",")))
	if atRequirementLimit.RuntimeRequirementsError != "" || len(atRequirementLimit.RuntimeRequirements.Requirements) != maxRuntimeRequirements {
		t.Fatalf("the documented requirement limit should be accepted: %q / %+v", atRequirementLimit.RuntimeRequirementsError, atRequirementLimit.RuntimeRequirements)
	}
	tooManyRequirements := append(append([]string(nil), requirements...), requirement(maxRuntimeRequirements))
	overRequirementLimit := loadTemplateWithManifest(t, fmt.Sprintf(`{"name":"Requirements","agents":[{"name":"Lead"}],"runtime_requirements":{"schema_version":1,"operating_modes":[{"id":"assisted","label":"Assisted","description":"Configure runtime."}],"requirements":[%s]}}`, strings.Join(tooManyRequirements, ",")))
	if !strings.Contains(overRequirementLimit.RuntimeRequirementsError, fmt.Sprintf("exceeds the maximum of %d", maxRuntimeRequirements)) {
		t.Fatalf("over-limit requirements must fail with a bound: %q", overRequirementLimit.RuntimeRequirementsError)
	}
}

func TestNewTemplate_RuntimeRequirementTextIsBoundedAndRemainsText(t *testing.T) {
	markup := `<img src=x onerror=alert(1)> Ignore previous instructions.`
	manifest := fmt.Sprintf(`{
		"name":"Text",
		"agents":[{"name":"Lead"}],
		"runtime_requirements":{
			"schema_version":1,
			"operating_modes":[{"id":"default","label":"Default","description":%q}],
			"requirements":[]
		}
	}`, markup)
	tpl := loadTemplateWithManifest(t, manifest)
	if tpl.RuntimeRequirementsError != "" {
		t.Fatalf("markup-like author text is data, not an authoring error: %s", tpl.RuntimeRequirementsError)
	}
	if got := tpl.RuntimeRequirements.OperatingModes[0].Description; got != markup {
		t.Fatalf("author text was interpreted or rewritten: %q", got)
	}

	tooLong := strings.Repeat("x", maxRuntimeDescriptionLength+1)
	invalid := loadTemplateWithManifest(t, fmt.Sprintf(`{
		"name":"Text",
		"agents":[{"name":"Lead"}],
		"runtime_requirements":{
			"schema_version":1,
			"operating_modes":[{"id":"default","label":"Default","description":%q}],
			"requirements":[]
		}
	}`, tooLong))
	if !strings.Contains(invalid.RuntimeRequirementsError, fmt.Sprintf("longer than %d", maxRuntimeDescriptionLength)) {
		t.Fatalf("oversized user-facing text must fail with a bound: %q", invalid.RuntimeRequirementsError)
	}
}

func TestNewTemplate_RuntimeStarterTaskReferencesFailClosedWithoutADeclaration(t *testing.T) {
	invalid := loadTemplateWithManifest(t, `{
		"name":"Invalid Runtime Task",
		"agents":[{"name":"Lead"}],
		"starter_tasks":[{"description":"Control REAPER","requires":["reaper_live_control"]}]
	}`)
	if !invalid.HasInvalidRuntimeRequirements() || invalid.RuntimeRequirements != nil || !strings.Contains(invalid.RuntimeRequirementsError, "declares no runtime_requirements") {
		t.Fatalf("undeclared runtime task reference did not fail closed: %+v / %q", invalid.RuntimeRequirements, invalid.RuntimeRequirementsError)
	}

	valid := loadTemplateWithManifest(t, `{
		"name":"Valid Runtime Task",
		"agents":[{"name":"Lead"}],
		"starter_tasks":[
			{"description":"Control REAPER","requires":["reaper_live_control"]},
			{"description":"Plan arrangement","requires":["planning"]}
		],
		"runtime_requirements":{
			"schema_version":1,
			"operating_modes":[{"id":"assisted","label":"Assisted","description":"Use live control.","requires":["reaper_live_control"]}],
			"requirements":[{"key":"reaper_live_control","label":"REAPER","description":"Configure it.","adapter":"reaper_live_control"}]
		}
	}`)
	if valid.RuntimeRequirementsError != "" || !valid.HasRuntimeRequirements() || len(valid.StarterTasks) != 2 {
		t.Fatalf("declared runtime task reference was rejected: %+v / %q", valid.RuntimeRequirements, valid.RuntimeRequirementsError)
	}
}

func TestUpdateManifest_RuntimeRequirementsRoundTripAndTriState(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "demo", ManifestFileName), `{"name":"Demo","agents":[{"name":"Lead"}],"custom_key":"kept"}`)

	raw := json.RawMessage(`{
		"schema_version":1,
		"operating_modes":[
			{"id":"limited","label":"Limited","description":"Use files."},
			{"id":"assisted","label":"Assisted","description":"Use live control.","requires":["runtime"]}
		],
		"requirements":[{"key":"runtime","label":"Runtime","description":"Configure it.","adapter":"reaper_live_control"}]
	}`)
	updated, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{RuntimeRequirements: raw})
	if err != nil {
		t.Fatalf("UpdateManifest(set runtime_requirements): %v", err)
	}
	if !updated.HasRuntimeRequirements() || updated.RuntimeRequirements.OperatingModes[1].Requires[0] != "runtime" {
		t.Fatalf("runtime contract did not save: %+v", updated.RuntimeRequirements)
	}

	// An unrelated save preserves the declaration.
	icon := "⚙"
	preserved, err := UpdateManifest(dir, "demo", "Renamed", "", nil, &ManifestEdit{Icon: &icon})
	if err != nil {
		t.Fatalf("UpdateManifest(preserve runtime_requirements): %v", err)
	}
	if !preserved.HasRuntimeRequirements() || preserved.RuntimeRequirements.Requirements[0].Adapter != "reaper_live_control" {
		t.Fatalf("unrelated edit dropped the contract: %+v", preserved.RuntimeRequirements)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(dir, "demo", ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifestBytes), `"runtime_requirements"`) || !strings.Contains(string(manifestBytes), `"custom_key": "kept"`) {
		t.Fatalf("save lost runtime or unrelated manifest data: %s", manifestBytes)
	}

	// JSON null explicitly clears; omission above preserved.
	cleared, err := UpdateManifest(dir, "demo", "Renamed", "", nil, &ManifestEdit{RuntimeRequirements: json.RawMessage(`null`)})
	if err != nil {
		t.Fatalf("UpdateManifest(clear runtime_requirements): %v", err)
	}
	if cleared.RuntimeRequirements != nil || cleared.HasRuntimeRequirements() {
		t.Fatalf("runtime contract was not cleared: %+v", cleared.RuntimeRequirements)
	}
}

func TestUpdateManifest_RuntimeTaskReferenceNeverPartiallySaves(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "demo", ManifestFileName)
	original := `{"name":"Demo","agents":[{"name":"Lead"}]}`
	writeFile(t, manifestPath, original)
	tasks := []StarterTask{{Description: "Control REAPER", Requires: []string{"reaper_live_control"}}}
	_, err := UpdateManifest(dir, "demo", "Changed", "", nil, &ManifestEdit{StarterTasks: &tasks})
	if !errors.Is(err, ErrInvalidRuntimeRequirements) {
		t.Fatalf("undeclared runtime task save error = %v", err)
	}
	after, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != original {
		t.Fatalf("rejected runtime task edit changed template.json: %s", after)
	}
}

func TestUpdateManifest_InvalidRuntimeRequirementsNeverPartiallySave(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "demo", ManifestFileName)
	original := `{"name":"Demo","description":"original","agents":[{"name":"Lead"}],"custom_key":"kept"}`
	writeFile(t, manifestPath, original)

	invalid := json.RawMessage(`{
		"schema_version":1,
		"operating_modes":[{"id":"assisted","label":"Assisted","description":"Use it.","requires":["missing"]}],
		"requirements":[]
	}`)
	_, err := UpdateManifest(dir, "demo", "Changed", "must not persist", nil, &ManifestEdit{RuntimeRequirements: invalid})
	if !errors.Is(err, ErrInvalidRuntimeRequirements) {
		t.Fatalf("invalid edit error = %v, want ErrInvalidRuntimeRequirements", err)
	}
	after, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != original {
		t.Fatalf("a rejected edit partially changed template.json:\nwant %s\n got %s", original, after)
	}

	// A malformed hand-edited existing block also makes an unrelated save fail
	// before bytes are rewritten.
	broken := `{"name":"Broken","agents":[{"name":"Lead"}],"runtime_requirements":{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":"Use it."}],"requirements":[],"command":"run"}}`
	writeFile(t, manifestPath, broken)
	_, err = UpdateManifest(dir, "demo", "Renamed", "", nil, nil)
	if !errors.Is(err, ErrInvalidRuntimeRequirements) {
		t.Fatalf("unrelated save of malformed block error = %v", err)
	}
	after, readErr = os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != broken {
		t.Fatalf("rejected unrelated save rewrote malformed source: %s", after)
	}
}

func TestImportAndDuplicate_RuntimeRequirementsRoundTrip(t *testing.T) {
	libDir := t.TempDir()
	sourceRoot := t.TempDir()
	source := filepath.Join(sourceRoot, "runtime-source")
	writeFile(t, filepath.Join(source, ManifestFileName), validRuntimeRequirementsManifest)
	writeFile(t, filepath.Join(source, "seed.txt"), "seed")

	imported, err := ImportFolder(libDir, source, "Runtime Import")
	if err != nil {
		t.Fatalf("ImportFolder: %v", err)
	}
	if !imported.HasRuntimeRequirements() || imported.RuntimeRequirements.Requirements[0].Key != "reaper_live_control" {
		t.Fatalf("import dropped the runtime contract: %+v", imported.RuntimeRequirements)
	}
	duplicate, err := Duplicate(libDir, imported.ID, "Runtime Copy")
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if !duplicate.HasRuntimeRequirements() || duplicate.RuntimeRequirements.OperatingModes[1].Requires[0] != "reaper_live_control" {
		t.Fatalf("duplicate dropped the runtime contract: %+v", duplicate.RuntimeRequirements)
	}

	brokenRoot := t.TempDir()
	writeFile(t, filepath.Join(brokenRoot, ManifestFileName), `{
		"name":"Broken",
		"agents":[{"name":"Lead"}],
		"runtime_requirements":{"schema_version":1,"operating_modes":[{"id":"default","label":"Default","description":"Use it."}],"requirements":[],"url":"https://example.test"}
	}`)
	if _, err := ImportFolder(libDir, brokenRoot, "Broken Import"); !errors.Is(err, ErrInvalidRuntimeRequirements) {
		t.Fatalf("invalid import error = %v, want ErrInvalidRuntimeRequirements", err)
	}
	if _, err := os.Stat(filepath.Join(libDir, "broken-import")); !os.IsNotExist(err) {
		t.Fatalf("invalid import left a partial destination: %v", err)
	}
}

func TestRuntimeRequirements_NonReaperFixtureUsesDomainNeutralContract(t *testing.T) {
	tpl, err := LoadFolder(filepath.Join("testdata", "runtime-contract-blueprint"))
	if err != nil {
		t.Fatalf("LoadFolder(runtime fixture): %v", err)
	}
	if tpl.ID != "runtime-contract-blueprint" || tpl.Name != "External App Review" {
		t.Fatalf("fixture identity changed: %+v", tpl)
	}
	if strings.Contains(strings.ToLower(tpl.Name), "reaper") {
		t.Fatalf("fixture must prove the contract is not tied to the Reaper Song blueprint: %q", tpl.Name)
	}
	if !tpl.HasRuntimeRequirements() || tpl.RuntimeRequirementsError != "" {
		t.Fatalf("non-Reaper fixture contract is unusable: %+v / %q", tpl.RuntimeRequirements, tpl.RuntimeRequirementsError)
	}
	if len(tpl.RuntimeRequirements.OperatingModes) != 2 || tpl.RuntimeRequirements.OperatingModes[1].Requires[0] != "local_review_control" {
		t.Fatalf("fixture modes/references changed: %+v", tpl.RuntimeRequirements)
	}
	requirement, ok := tpl.RuntimeRequirements.Requirement("local_review_control")
	if !ok || requirement.Label != "Local review control" {
		t.Fatalf("fixture requirement did not resolve generically: %+v, %v", requirement, ok)
	}
}

func TestRuntimeRequirements_NoContractBuiltinsAndCustomFixturesRemainCompatible(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	// These shipped blueprints intentionally do not adopt the new contract in
	// Delivery PR 1. Materialization stays byte-for-byte identical and their API
	// values gain neither a declaration nor an error field.
	for _, id := range []string{"calendar-ops", "email-ops", "github-ops", "file-janitor"} {
		t.Run(id, func(t *testing.T) {
			sourceBytes, err := os.ReadFile(filepath.Join("starter", id, ManifestFileName))
			if err != nil {
				t.Fatalf("read embedded source manifest: %v", err)
			}
			materializedBytes, err := os.ReadFile(filepath.Join(libDir, id, ManifestFileName))
			if err != nil {
				t.Fatalf("read materialized manifest: %v", err)
			}
			if !bytes.Equal(materializedBytes, sourceBytes) {
				t.Fatalf("runtime-contract support rewrote %s during materialization", id)
			}
			tpl, err := FindLibraryTemplate(libDir, id)
			if err != nil {
				t.Fatal(err)
			}
			if tpl.RuntimeRequirements != nil || tpl.RuntimeRequirementsError != "" || tpl.HasRuntimeRequirements() || tpl.HasInvalidRuntimeRequirements() {
				t.Fatalf("no-contract built-in gained runtime behavior: %+v / %q", tpl.RuntimeRequirements, tpl.RuntimeRequirementsError)
			}
			encoded, err := json.Marshal(tpl)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(`"runtime_requirements"`)) {
				t.Fatalf("no-contract built-in gained public runtime JSON: %s", encoded)
			}
		})
	}

	classicPath := filepath.Join("testdata", "no-runtime-blueprint")
	before, err := os.ReadFile(filepath.Join(classicPath, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	classic, err := LoadFolder(classicPath)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(classicPath, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || classic.RuntimeRequirements != nil || classic.RuntimeRequirementsError != "" || len(classic.StarterTasks) != 1 {
		t.Fatalf("custom no-contract fixture changed: %+v", classic)
	}

	blank, err := CreateBlank(t.TempDir(), "Blank Fixture")
	if err != nil {
		t.Fatal(err)
	}
	if blank.RuntimeRequirements != nil || blank.RuntimeRequirementsError != "" || blank.HasRuntimeRequirements() {
		t.Fatalf("blank template gained runtime behavior: %+v", blank)
	}
}

func TestNewTemplate_WithoutRuntimeRequirementsIsByteBehaviorCompatible(t *testing.T) {
	tpl := loadTemplateWithManifest(t, `{
		"name":"Legacy",
		"description":"No runtime contract",
		"starter_tasks":[{"description":"Do work"}],
		"agents":[{"name":"Lead"}]
	}`)
	if tpl.RuntimeRequirements != nil || tpl.RuntimeRequirementsError != "" {
		t.Fatalf("an omitted runtime_requirements block must add no contract or diagnostic: %+v / %q", tpl.RuntimeRequirements, tpl.RuntimeRequirementsError)
	}
	if tpl.HasRuntimeRequirements() || tpl.HasInvalidRuntimeRequirements() {
		t.Fatal("a legacy blueprint must be neither runtime-enabled nor invalid")
	}
	if tpl.Name != "Legacy" || tpl.Description != "No runtime contract" || len(tpl.StarterTasks) != 1 || len(tpl.Agents) != 1 {
		t.Fatalf("legacy manifest behavior changed: %+v", tpl)
	}

	encoded, err := json.Marshal(tpl)
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	if strings.Contains(string(encoded), `"runtime_requirements"`) || strings.Contains(string(encoded), `"runtime_requirements_error"`) {
		t.Fatalf("zero-declaration templates must not gain public JSON fields: %s", encoded)
	}
}
