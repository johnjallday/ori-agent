package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// factsOperationJSON is the facts operation a project plugin declares: one
// boolean in, closed bounded facts out.
const factsOperationJSON = `{
  "id": "profile.read",
  "input_schema": {
    "type": "object",
    "properties": {"include_templates": {"type": "boolean"}},
    "required": ["include_templates"],
    "additionalProperties": false
  },
  "output_schema": {
    "type": "object",
    "properties": {
      "app": {"type": "string", "minLength": 1, "maxLength": 80},
      "installed": {"type": "boolean"},
      "version": {"type": "string", "maxLength": 40},
      "templates_available": {"type": "boolean"},
      "templates": {
        "type": "array",
        "maxItems": 64,
        "items": {
          "type": "object",
          "properties": {
            "name": {"type": "string", "minLength": 1, "maxLength": 120},
            "kind": {"type": "string", "enum": ["project", "track"], "maxLength": 16},
            "file": {"type": "string", "minLength": 1, "maxLength": 255},
            "modified_at": {"type": "string", "maxLength": 40}
          },
          "required": ["name", "kind", "file"],
          "additionalProperties": false
        }
      },
      "truncated": {"type": "boolean"}
    },
    "required": ["app", "installed", "templates_available", "truncated"],
    "additionalProperties": false
  },
  "max_output_bytes": 32768,
  "timeout_class": "fast",
  "policy": "read_only"
}`

// factsContributionJSON is the canonical fixture as a project plugin that
// declares a facts operation.
func factsContributionJSON(t *testing.T, mutate func(root map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(canonicalSurfaceFixture(t), &root); err != nil {
		t.Fatal(err)
	}
	var operation map[string]any
	if err := json.Unmarshal([]byte(factsOperationJSON), &operation); err != nil {
		t.Fatal(err)
	}
	service := root["services"].([]any)[0].(map[string]any)
	service["operations"] = append(service["operations"].([]any), operation)
	features, _ := root["requires_host_features"].([]any)
	root["requires_host_features"] = append(features, "home_profile_v1")
	root["home_profile_facts"] = map[string]any{"service_id": "demo-service", "operation": "profile.read"}
	if mutate != nil {
		mutate(root)
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestHomeProfileFactsReferenceIsAccepted(t *testing.T) {
	contribution, err := ParseSurfaceContribution(factsContributionJSON(t, nil))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ref := contribution.HomeProfileFacts
	if ref == nil || ref.ServiceID != "demo-service" || ref.Operation != "profile.read" {
		t.Fatalf("facts reference = %+v", ref)
	}
	// A plugin that names no facts operation is unchanged.
	plain, err := ParseSurfaceContribution(canonicalSurfaceFixture(t))
	if err != nil || plain.HomeProfileFacts != nil {
		t.Fatalf("canonical fixture facts = %+v err = %v", plain.HomeProfileFacts, err)
	}
	if encoded, _ := json.Marshal(plain); strings.Contains(string(encoded), "home_profile_facts") {
		t.Fatalf("a plugin without the key encodes it: %s", encoded)
	}
}

func TestHomeProfileFactsReferenceFailsClosed(t *testing.T) {
	ref := func(root map[string]any) map[string]any { return root["home_profile_facts"].(map[string]any) }
	tests := map[string]struct {
		mutate func(root map[string]any)
		code   ContributionErrorCode
	}{
		"feature omitted": {func(root map[string]any) {
			features := root["requires_host_features"].([]any)
			root["requires_host_features"] = features[:len(features)-1]
		}, CodeHostFeatureUnsupported},
		"unknown service":   {func(root map[string]any) { ref(root)["service_id"] = "other-service" }, CodeComponentUnknown},
		"unknown operation": {func(root map[string]any) { ref(root)["operation"] = "profile.write" }, CodeComponentUnknown},
		// A facts read never changes anything: an operation that can is refused.
		"not read_only": {func(root map[string]any) { ref(root)["operation"] = "runtime.repair" }, CodeOperationPolicyInvalid},
		"unknown key":   {func(root map[string]any) { ref(root)["path"] = "/Applications" }, CodeContributionInvalid},
		"empty":         {func(root map[string]any) { root["home_profile_facts"] = map[string]any{} }, CodeComponentUnknown},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSurfaceContribution(factsContributionJSON(t, tc.mutate))
			var contributionErr *ContributionError
			if !errors.As(err, &contributionErr) || contributionErr.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

// factsProcess is a plugin service that answers the facts operation.
type factsProcess struct {
	output json.RawMessage
	err    error
	calls  []map[string]any
	specs  []workspacesurface.ServiceSpec
}

func (p *factsProcess) Start(context.Context) error { return nil }
func (p *factsProcess) Stop(context.Context) error  { return nil }
func (p *factsProcess) Healthy() bool               { return true }
func (p *factsProcess) Call(_ context.Context, operation string, arguments map[string]any) (json.RawMessage, error) {
	p.calls = append(p.calls, map[string]any{"operation": operation, "arguments": arguments})
	return p.output, p.err
}

func factsLifecycle(t *testing.T, process *factsProcess) (*SurfaceLifecycle, InstalledPlugin) {
	t.Helper()
	contribution, err := ParseSurfaceContribution(factsContributionJSON(t, nil))
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

const factsOutput = `{"app":"REAPER","installed":true,"version":"7.28","templates_available":true,` +
	`"templates":[{"name":"Band Session","kind":"project","file":"Band Session.RPP","modified_at":"2026-10-01T10:00:00Z"}],"truncated":false}`

func TestHomeProfileFactsCallsTheDeclaredOperationOnceWithItsLimits(t *testing.T) {
	process := &factsProcess{output: json.RawMessage(factsOutput)}
	lifecycle, installed := factsLifecycle(t, process)
	output, err := lifecycle.HomeProfileFacts(context.Background(), installed, json.RawMessage(`{"include_templates":true}`))
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if string(output) != factsOutput {
		t.Fatalf("output = %s", output)
	}
	if len(process.calls) != 1 || process.calls[0]["operation"] != "profile.read" {
		t.Fatalf("calls = %+v", process.calls)
	}
	arguments := process.calls[0]["arguments"].(map[string]any)
	if arguments["operation_id"] != "profile.read" || arguments["protocol_version"] != workspacesurface.ProtocolVersion {
		t.Fatalf("arguments = %+v", arguments)
	}
	if input := arguments["input"].(map[string]any); input["include_templates"] != true || len(input) != 1 {
		t.Fatalf("input = %+v", input)
	}
	// A machine-level call: no workspace, project, data root or scope.
	callContext := arguments["context"].(map[string]any)
	for _, key := range []string{"workspace_id", "workspace_root", "project_entry", "plugin_data_root"} {
		if callContext[key] != "" {
			t.Fatalf("the facts call carried %s = %v", key, callContext[key])
		}
	}
	if scopes, _ := callContext["scopes"].([]string); len(scopes) != 0 {
		t.Fatalf("the facts call carried scopes %v", scopes)
	}
	// It runs the plugin's own registered service artifact.
	if len(process.specs) != 1 || process.specs[0].ServiceID != "demo-service" || process.specs[0].Command != "/managed/artifacts/demo-service" ||
		process.specs[0].PluginGeneration != installed.Generation {
		t.Fatalf("service spec = %+v", process.specs)
	}
}

func TestHomeProfileFactsRefusesBadInputAndBadOutput(t *testing.T) {
	process := &factsProcess{output: json.RawMessage(factsOutput)}
	lifecycle, installed := factsLifecycle(t, process)
	for _, input := range []string{`{}`, `{"include_templates":"yes"}`, `{"include_templates":true,"path":"/"}`, `[]`, ``} {
		if _, err := lifecycle.HomeProfileFacts(context.Background(), installed, json.RawMessage(input)); err == nil {
			t.Fatalf("input %q was sent", input)
		}
	}
	if len(process.calls) != 0 {
		t.Fatalf("a refused input reached the plugin: %+v", process.calls)
	}
	outputs := map[string]string{
		"an extra key":       `{"app":"REAPER","installed":true,"templates_available":false,"truncated":false,"path":"/Users/me"}`,
		"a missing key":      `{"app":"REAPER","installed":true}`,
		"an unknown kind":    `{"app":"REAPER","installed":true,"templates_available":true,"truncated":false,"templates":[{"name":"A","kind":"preset","file":"A.RPP"}]}`,
		"too many templates": `{"app":"REAPER","installed":true,"templates_available":true,"truncated":false,"templates":[` + strings.TrimSuffix(strings.Repeat(`{"name":"A","kind":"project","file":"A.RPP"},`, 65), ",") + `]}`,
		"not JSON":           `REAPER 7.28`,
		"too large":          `{"app":"REAPER","installed":true,"templates_available":false,"truncated":false,"version":"` + strings.Repeat("7", 40000) + `"}`,
	}
	for name, output := range outputs {
		process.output = json.RawMessage(output)
		if _, err := lifecycle.HomeProfileFacts(context.Background(), installed, json.RawMessage(`{"include_templates":true}`)); !errors.Is(err, workspacesurface.ErrServiceUnavailable) {
			t.Errorf("%s: err = %v, want the output refused", name, err)
		}
	}
	// A failing plugin is an error, not an empty answer.
	process.output, process.err = nil, errors.New("exit status 1")
	if _, err := lifecycle.HomeProfileFacts(context.Background(), installed, json.RawMessage(`{"include_templates":false}`)); err == nil {
		t.Fatal("a failed call returned no error")
	}
}

func TestHomeProfileFactsIsUnavailableWithoutAUsableDeclaration(t *testing.T) {
	process := &factsProcess{output: json.RawMessage(factsOutput)}
	lifecycle, installed := factsLifecycle(t, process)
	input := json.RawMessage(`{"include_templates":false}`)

	disabled := installed
	disabled.Enabled = false
	undeclared := installedSurfacePlugin(t, true, true)
	noArtifact := installed
	noArtifact.ResolvedArtifacts = append([]ResolvedArtifact(nil), installed.ResolvedArtifacts...)
	noArtifact.ResolvedArtifacts[0].Available = false

	for name, candidate := range map[string]InstalledPlugin{
		"disabled": disabled, "no facts operation": undeclared, "artifact unavailable": noArtifact, "MCP-only": {Name: "plain", Enabled: true},
	} {
		if _, err := lifecycle.HomeProfileFacts(context.Background(), candidate, input); !errors.Is(err, ErrHomeProfileFactsUnavailable) {
			t.Errorf("%s: err = %v, want unavailable", name, err)
		}
	}
	if DeclaresHomeProfileFacts(disabled) || DeclaresHomeProfileFacts(undeclared) || !DeclaresHomeProfileFacts(installed) {
		t.Fatal("DeclaresHomeProfileFacts disagrees with the call")
	}
	if len(process.calls) != 0 {
		t.Fatalf("an unavailable plugin was called: %+v", process.calls)
	}
}
