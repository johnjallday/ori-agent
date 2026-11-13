package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// assistant-reads-projects 4.1, 4.2: an app plugin's project reader, declared
// in its manifest and called by the host for the files the host names.

// projectFactsOperationJSON is the reader operation the cross-repository
// contract describes (tasks/companion-reaper-plugin-assistant-reads-projects.md):
// a batch of files the host resolved in, one bounded result per file out.
const projectFactsOperationJSON = `{
  "id": "project.facts.read",
  "input_schema": {
    "type": "object",
    "properties": {
      "schema_version": {"type": "integer", "minimum": 1, "maximum": 1},
      "names": {"type": "boolean"},
      "max_file_bytes": {"type": "integer", "minimum": 1, "maximum": 33554432},
      "files": {
        "type": "array", "minItems": 1, "maxItems": 8,
        "items": {
          "type": "object",
          "properties": {
            "id": {"type": "integer", "minimum": 0, "maximum": 7},
            "format": {"type": "string", "minLength": 1, "maxLength": 64},
            "path": {"type": "string", "minLength": 1, "maxLength": 4096},
            "size": {"type": "integer", "minimum": 0}
          },
          "required": ["id", "format", "path", "size"],
          "additionalProperties": false
        }
      }
    },
    "required": ["schema_version", "names", "max_file_bytes", "files"],
    "additionalProperties": false
  },
  "output_schema": {
    "type": "object",
    "properties": {
      "schema_version": {"type": "integer", "minimum": 1, "maximum": 1},
      "files": {
        "type": "array", "maxItems": 8,
        "items": {
          "type": "object",
          "properties": {
            "id": {"type": "integer", "minimum": 0, "maximum": 7},
            "result": {"type": "string", "enum": ["read", "too_large", "unreadable", "failed"], "maxLength": 16},
            "tempo_bpm": {"type": "number", "minimum": 0, "maximum": 960},
            "tempo_varies": {"type": "boolean"},
            "length_seconds": {"type": "number", "minimum": 0, "maximum": 360000},
            "track_count": {"type": "integer", "minimum": 0, "maximum": 10000},
            "tracks": {"type": "array", "maxItems": 128, "items": {"type": "string", "minLength": 1, "maxLength": 80}},
            "tracks_left_out": {"type": "integer", "minimum": 0, "maximum": 1048576},
            "markers": {
              "type": "array", "maxItems": 128,
              "items": {
                "type": "object",
                "properties": {
                  "name": {"type": "string", "maxLength": 80},
                  "at": {"type": "number", "minimum": 0, "maximum": 360000},
                  "end": {"type": "number", "minimum": 0, "maximum": 360000}
                },
                "required": ["at"],
                "additionalProperties": false
              }
            },
            "markers_left_out": {"type": "integer", "minimum": 0, "maximum": 1048576}
          },
          "required": ["id", "result"],
          "additionalProperties": false
        }
      }
    },
    "required": ["schema_version", "files"],
    "additionalProperties": false
  },
  "max_output_bytes": 262144,
  "timeout_class": "long",
  "policy": "read_only",
  "scopes": []
}`

// projectFactsContributionJSON is the canonical fixture as an app plugin that
// declares a project reader for the made-up format "zzfmt".
func projectFactsContributionJSON(t *testing.T, mutate func(root map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(canonicalSurfaceFixture(t), &root); err != nil {
		t.Fatal(err)
	}
	var operation map[string]any
	if err := json.Unmarshal([]byte(projectFactsOperationJSON), &operation); err != nil {
		t.Fatal(err)
	}
	service := root["services"].([]any)[0].(map[string]any)
	service["operations"] = append(service["operations"].([]any), operation)
	features, _ := root["requires_host_features"].([]any)
	root["requires_host_features"] = append(features, HostFeatureProjectFactsV1)
	root["project_facts"] = map[string]any{"service_id": "demo-service", "operation": "project.facts.read", "formats": []any{"zzfmt"}}
	if mutate != nil {
		mutate(root)
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// readerOperation is the reader operation inside a mutated fixture's root.
func readerOperation(root map[string]any) map[string]any {
	for _, raw := range root["services"].([]any)[0].(map[string]any)["operations"].([]any) {
		if operation := raw.(map[string]any); operation["id"] == "project.facts.read" {
			return operation
		}
	}
	return nil
}

func TestProjectFactsReferenceIsAccepted(t *testing.T) {
	contribution, err := ParseSurfaceContribution(projectFactsContributionJSON(t, nil))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ref := contribution.ProjectFacts
	if ref == nil || ref.ServiceID != "demo-service" || ref.Operation != "project.facts.read" || !slices.Equal(ref.Formats, []string{"zzfmt"}) {
		t.Fatalf("reader reference = %+v", ref)
	}
	installed := installedSurfacePlugin(t, true, true)
	installed.WorkspaceSurfaces = contribution
	if !DeclaresProjectFacts(installed) || !slices.Equal(ProjectFactsFormats(installed), []string{"zzfmt"}) || ProjectFactsBatchFiles(installed) != 8 {
		t.Fatalf("declares %v formats %v batch %d", DeclaresProjectFacts(installed), ProjectFactsFormats(installed), ProjectFactsBatchFiles(installed))
	}
	// A plugin that names no reader is unchanged.
	plain, err := ParseSurfaceContribution(canonicalSurfaceFixture(t))
	if err != nil || plain.ProjectFacts != nil {
		t.Fatalf("canonical fixture reader = %+v err = %v", plain.ProjectFacts, err)
	}
	if encoded, _ := json.Marshal(plain); strings.Contains(string(encoded), "project_facts") {
		t.Fatalf("a plugin without the key encodes it: %s", encoded)
	}
	// A smaller output limit leaves room for fewer files per call.
	smaller, err := ParseSurfaceContribution(projectFactsContributionJSON(t, func(root map[string]any) {
		readerOperation(root)["max_output_bytes"] = 65536
	}))
	if err != nil {
		t.Fatal(err)
	}
	installed.WorkspaceSurfaces = smaller
	if got := ProjectFactsBatchFiles(installed); got != 2 {
		t.Fatalf("batch with 64 KiB = %d, want 2", got)
	}
}

func TestProjectFactsReferenceFailsClosed(t *testing.T) {
	ref := func(root map[string]any) map[string]any { return root["project_facts"].(map[string]any) }
	tests := map[string]struct {
		mutate func(root map[string]any)
		code   ContributionErrorCode
	}{
		"feature omitted": {func(root map[string]any) {
			features := root["requires_host_features"].([]any)
			root["requires_host_features"] = features[:len(features)-1]
		}, CodeHostFeatureUnsupported},
		"unknown service":   {func(root map[string]any) { ref(root)["service_id"] = "other-service" }, CodeComponentUnknown},
		"unknown operation": {func(root map[string]any) { ref(root)["operation"] = "project.facts.write" }, CodeComponentUnknown},
		// Reading never changes anything: an operation that can is refused.
		"not read_only":       {func(root map[string]any) { ref(root)["operation"] = "runtime.repair" }, CodeOperationPolicyInvalid},
		"not the long class":  {func(root map[string]any) { readerOperation(root)["timeout_class"] = "fast" }, CodeContributionInvalid},
		"too little output":   {func(root map[string]any) { readerOperation(root)["max_output_bytes"] = 32767 }, CodeContributionInvalid},
		"asks for a scope":    {func(root map[string]any) { readerOperation(root)["scopes"] = []any{"workspace_project_read"} }, CodeContributionInvalid},
		"no formats":          {func(root map[string]any) { ref(root)["formats"] = []any{} }, CodeContributionInvalid},
		"a repeated format":   {func(root map[string]any) { ref(root)["formats"] = []any{"zzfmt", "zzfmt"} }, CodeContributionInvalid},
		"an extension":        {func(root map[string]any) { ref(root)["formats"] = []any{".zz"} }, CodeContributionInvalid},
		"nine formats":        {func(root map[string]any) { ref(root)["formats"] = []any{"a", "b", "c", "d", "e", "f", "g", "h", "i"} }, CodeContributionInvalid},
		"unknown key":         {func(root map[string]any) { ref(root)["path"] = "/Applications" }, CodeContributionInvalid},
		"formats not a list":  {func(root map[string]any) { ref(root)["formats"] = "zzfmt" }, CodeContributionInvalid},
		"formats omitted":     {func(root map[string]any) { delete(ref(root), "formats") }, CodeContributionInvalid},
		"empty declaration":   {func(root map[string]any) { root["project_facts"] = map[string]any{} }, CodeContributionInvalid},
		"reader named by GUI": {func(root map[string]any) { ref(root)["operation"] = "" }, CodeComponentUnknown},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSurfaceContribution(projectFactsContributionJSON(t, tc.mutate))
			var contributionErr *ContributionError
			if !errors.As(err, &contributionErr) || contributionErr.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

// An older host is refused before install, and a release that needs a feature
// this host lacks reads as "needs a newer host" even when it also carries a key
// this host has never heard of, so the resolver steps down to an older release.
func TestProjectFactsNeedsANewerHostOnAnOlderOne(t *testing.T) {
	contribution, err := ParseSurfaceContribution(projectFactsContributionJSON(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	older := slices.DeleteFunc(HostFeatures(), func(feature string) bool { return feature == HostFeatureProjectFactsV1 })
	if err := contribution.ValidateForHost(SurfaceProtocolVersion, older); !ContributionErrorIs(err, CodeHostFeatureUnsupported) {
		t.Fatalf("older host = %v", err)
	}
	future := projectFactsContributionJSON(t, func(root map[string]any) {
		root["requires_host_features"] = append(root["requires_host_features"].([]any), "future_feature_v1")
		root["future_key"] = map[string]any{"anything": true}
	})
	if _, err := ParseSurfaceContribution(future); !ContributionErrorIs(err, CodeHostFeatureUnsupported) {
		t.Fatalf("a release needing a newer host = %v, want host feature unsupported", err)
	}
	// An unknown key with only known features is still a broken manifest.
	broken := projectFactsContributionJSON(t, func(root map[string]any) { root["future_key"] = true })
	if _, err := ParseSurfaceContribution(broken); !ContributionErrorIs(err, CodeContributionInvalid) {
		t.Fatalf("an unknown key = %v, want invalid", err)
	}
}

func TestProjectFactsSchemaListsTheFeatureAndTheKey(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("schema", "workspace-surface-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Enum []string `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(schema.Properties["requires_host_features"].Items.Enum, HostFeatures()) {
		t.Fatalf("schema features %v, host features %v", schema.Properties["requires_host_features"].Items.Enum, HostFeatures())
	}
	if _, ok := schema.Properties["project_facts"]; !ok || schema.Defs["projectFacts"] == nil {
		t.Fatal("the schema does not describe project_facts")
	}
}

const projectFactsInput = `{"schema_version":1,"names":true,"max_file_bytes":33554432,"files":[{"id":0,"format":"zzfmt","path":"/songs/One/One.zz","size":120}]}`

const projectFactsOutput = `{"schema_version":1,"files":[{"id":0,"result":"read","tempo_bpm":120,"tempo_varies":false,"length_seconds":215.5,"track_count":3,` +
	`"tracks":["Kick","Bass","Vox"],"tracks_left_out":0,"markers":[{"name":"Verse","at":10},{"name":"Chorus","at":40,"end":72.25}],"markers_left_out":0}]}`

func projectFactsLifecycle(t *testing.T, process *factsProcess) (*SurfaceLifecycle, InstalledPlugin) {
	t.Helper()
	contribution, err := ParseSurfaceContribution(projectFactsContributionJSON(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	installed := installedSurfacePlugin(t, true, true)
	installed.WorkspaceSurfaces = contribution
	lifecycle := NewSurfaceLifecycle(nil, workspacesurface.NewServiceManager(func(spec workspacesurface.ServiceSpec) workspacesurface.ServiceProcess {
		process.specs = append(process.specs, spec)
		return process
	}))
	return lifecycle, installed
}

func TestProjectFactsCallsTheDeclaredReaderWithItsLimits(t *testing.T) {
	process := &factsProcess{output: json.RawMessage(projectFactsOutput)}
	lifecycle, installed := projectFactsLifecycle(t, process)
	output, err := lifecycle.ProjectFacts(context.Background(), installed, json.RawMessage(projectFactsInput))
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if string(output) != projectFactsOutput {
		t.Fatalf("output = %s", output)
	}
	if len(process.calls) != 1 || process.calls[0]["operation"] != "project.facts.read" {
		t.Fatalf("calls = %+v", process.calls)
	}
	arguments := process.calls[0]["arguments"].(map[string]any)
	callContext := arguments["context"].(map[string]any)
	for _, key := range []string{"workspace_id", "workspace_root", "project_entry", "plugin_data_root"} {
		if callContext[key] != "" {
			t.Fatalf("the reader call carried %s = %v", key, callContext[key])
		}
	}
	if len(process.specs) != 1 || process.specs[0].ServiceID != "demo-service" {
		t.Fatalf("service spec = %+v", process.specs)
	}
}

func TestProjectFactsRefusesBadInputAndBadOutput(t *testing.T) {
	process := &factsProcess{output: json.RawMessage(projectFactsOutput)}
	lifecycle, installed := projectFactsLifecycle(t, process)
	file := `{"id":0,"format":"zzfmt","path":"/a.zz","size":1}`
	for name, input := range map[string]string{
		"no files":       `{"schema_version":1,"names":false,"max_file_bytes":1,"files":[]}`,
		"nine files":     `{"schema_version":1,"names":false,"max_file_bytes":1,"files":[` + strings.TrimSuffix(strings.Repeat(file+",", 9), ",") + `]}`,
		"an extra key":   `{"schema_version":1,"names":false,"max_file_bytes":1,"files":[` + file + `],"workspace":"x"}`,
		"a file over 32": `{"schema_version":1,"names":false,"max_file_bytes":33554433,"files":[` + file + `]}`,
		"not JSON":       ``,
	} {
		if _, err := lifecycle.ProjectFacts(context.Background(), installed, json.RawMessage(input)); err == nil {
			t.Errorf("%s was sent", name)
		}
	}
	if len(process.calls) != 0 {
		t.Fatalf("a refused input reached the plugin: %+v", process.calls)
	}
	for name, output := range map[string]string{
		"an unknown result": `{"schema_version":1,"files":[{"id":0,"result":"maybe"}]}`,
		"a tempo too high":  `{"schema_version":1,"files":[{"id":0,"result":"read","tempo_bpm":2000}]}`,
		"a long name":       `{"schema_version":1,"files":[{"id":0,"result":"read","tracks":["` + strings.Repeat("n", 81) + `"]}]}`,
		"an extra key":      `{"schema_version":1,"files":[{"id":0,"result":"read","path":"/Users/me"}]}`,
		"too large":         `{"schema_version":1,"files":[` + strings.TrimSuffix(strings.Repeat(`{"id":0,"result":"read","tracks":["`+strings.Repeat("n", 80)+`"]},`, 2000), ",") + `]}`,
	} {
		process.output = json.RawMessage(output)
		if _, err := lifecycle.ProjectFacts(context.Background(), installed, json.RawMessage(projectFactsInput)); !errors.Is(err, workspacesurface.ErrServiceUnavailable) {
			t.Errorf("%s: err = %v, want the output refused", name, err)
		}
	}
	// A failing plugin is an error, never "no facts".
	process.output, process.err = nil, errors.New("exit status 1")
	if _, err := lifecycle.ProjectFacts(context.Background(), installed, json.RawMessage(projectFactsInput)); err == nil {
		t.Fatal("a failed call returned no error")
	}
}

func TestProjectFactsIsUnavailableWithoutAUsableDeclaration(t *testing.T) {
	process := &factsProcess{output: json.RawMessage(projectFactsOutput)}
	lifecycle, installed := projectFactsLifecycle(t, process)
	disabled := installed
	disabled.Enabled = false
	undeclared := installedSurfacePlugin(t, true, true)
	noArtifact := installed
	noArtifact.ResolvedArtifacts = append([]ResolvedArtifact(nil), installed.ResolvedArtifacts...)
	noArtifact.ResolvedArtifacts[0].Available = false
	for name, candidate := range map[string]InstalledPlugin{
		"disabled": disabled, "no reader": undeclared, "artifact unavailable": noArtifact, "MCP-only": {Name: "plain", Enabled: true},
	} {
		if _, err := lifecycle.ProjectFacts(context.Background(), candidate, json.RawMessage(projectFactsInput)); !errors.Is(err, ErrProjectFactsUnavailable) {
			t.Errorf("%s: err = %v, want unavailable", name, err)
		}
	}
	if DeclaresProjectFacts(disabled) || DeclaresProjectFacts(undeclared) || ProjectFactsBatchFiles(undeclared) != 0 {
		t.Fatal("the declaration helpers disagree with the call")
	}
	if len(process.calls) != 0 {
		t.Fatalf("an unavailable plugin was called: %+v", process.calls)
	}
}
