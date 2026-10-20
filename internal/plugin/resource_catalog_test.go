package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func resourceContractFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "resourcecatalog", "testdata", "contract", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// catalogOperationJSON is a minimal closed read-only operation; the catalog
// contract's wire shapes are checked by internal/resourcecatalog.
func catalogOperationJSON(id string) map[string]any {
	return map[string]any{
		"id": id,
		"input_schema": map[string]any{
			"type": "object", "additionalProperties": false, "required": []any{"schema_version"},
			"properties": map[string]any{"schema_version": map[string]any{"type": "integer", "minimum": 1, "maximum": 1}},
		},
		"output_schema": map[string]any{
			"type": "object", "additionalProperties": false, "required": []any{"schema_version"},
			"properties": map[string]any{"schema_version": map[string]any{"type": "integer", "minimum": 1, "maximum": 1}},
		},
		"max_output_bytes": 32768, "timeout_class": "fast", "policy": "read_only",
	}
}

// resourceCatalogContributionJSON is the canonical fixture as a project
// plugin that also contributes a resource workspace and its catalog.
func resourceCatalogContributionJSON(t *testing.T, mutate func(root map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(canonicalSurfaceFixture(t), &root); err != nil {
		t.Fatal(err)
	}
	service := root["services"].([]any)[0].(map[string]any)
	service["operations"] = append(service["operations"].([]any),
		catalogOperationJSON("studio.sources.read"), catalogOperationJSON("studio.inventory.read"))
	root["blueprints"] = append(root["blueprints"].([]any), map[string]any{
		"id": "reaper-studio", "version": 1, "manifest": "blueprints/reaper-studio/template.json",
		"skeleton": "blueprints/reaper-studio/workspace", "capabilities": []any{},
	})
	features, _ := root["requires_host_features"].([]any)
	root["requires_host_features"] = append(features, HostFeatureResourceWorkspacesV1)
	var catalog map[string]any
	if err := json.Unmarshal(resourceContractFile(t, "declaration.valid.json"), &catalog); err != nil {
		t.Fatal(err)
	}
	catalog["service_id"] = "demo-service"
	root["resource_catalog"] = catalog
	if mutate != nil {
		mutate(root)
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestResourceCatalogContribution_AcceptsTheDeclaredCatalog(t *testing.T) {
	contribution, err := ParseSurfaceContribution(resourceCatalogContributionJSON(t, nil))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	declaration, err := contribution.ResourceCatalogDeclaration()
	if err != nil || declaration == nil || declaration.BlueprintID != "reaper-studio" || len(declaration.Categories) != 3 {
		t.Fatalf("declaration = %+v, %v", declaration, err)
	}
	if !slices.Contains(HostFeatures(), HostFeatureResourceWorkspacesV1) {
		t.Fatal("this host does not advertise resource_workspaces_v1")
	}
}

func TestResourceCatalogContribution_IsRefusedByAnOlderHost(t *testing.T) {
	contribution := &SurfaceContribution{}
	if err := json.Unmarshal(resourceCatalogContributionJSON(t, nil), contribution); err != nil {
		t.Fatal(err)
	}
	older := slices.DeleteFunc(HostFeatures(), func(feature string) bool { return feature == HostFeatureResourceWorkspacesV1 })
	if err := contribution.ValidateForHost(SurfaceProtocolVersion, older); !ContributionErrorIs(err, CodeHostFeatureUnsupported) {
		t.Fatalf("older host error = %v", err)
	}
}

func TestResourceCatalogContribution_RefusesBrokenReferencesAndAuthority(t *testing.T) {
	cases := map[string]struct {
		mutate func(root map[string]any)
		code   ContributionErrorCode
	}{
		"feature not required": {func(root map[string]any) {
			root["requires_host_features"] = []any{}
		}, CodeHostFeatureUnsupported},
		"unknown blueprint": {func(root map[string]any) {
			root["resource_catalog"].(map[string]any)["blueprint_id"] = "reaper-vault"
		}, CodeComponentUnknown},
		"unknown service": {func(root map[string]any) {
			root["resource_catalog"].(map[string]any)["service_id"] = "other-service"
		}, CodeComponentUnknown},
		"undeclared operation": {func(root map[string]any) {
			root["resource_catalog"].(map[string]any)["inventory_operation"] = "studio.inventory.write"
		}, CodeComponentUnknown},
		"operation that changes things": {func(root map[string]any) {
			root["resource_catalog"].(map[string]any)["sources_operation"] = "runtime.repair"
		}, CodeOperationPolicyInvalid},
		"operation with a scope": {func(root map[string]any) {
			operations := root["services"].([]any)[0].(map[string]any)["operations"].([]any)
			last := operations[len(operations)-1].(map[string]any)
			last["scopes"] = []any{"plugin_data_write"}
		}, CodeOperationPolicyInvalid},
		"invalid declaration": {func(root map[string]any) {
			root["resource_catalog"].(map[string]any)["version"] = 2
		}, CodeContributionInvalid},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSurfaceContribution(resourceCatalogContributionJSON(t, test.mutate)); !ContributionErrorIs(err, test.code) {
				t.Fatalf("err = %v, want %s", err, test.code)
			}
		})
	}
}

func TestResourceCatalogContribution_RefusesADuplicateKeyInTheDeclaration(t *testing.T) {
	data := string(resourceCatalogContributionJSON(t, nil))
	data = strings.Replace(data, `"blueprint_id":"reaper-studio"`, `"blueprint_id":"reaper-studio","blueprint_id":"reaper-studio"`, 1)
	if _, err := ParseSurfaceContribution([]byte(data)); !ContributionErrorIs(err, CodeContributionInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestResourceAttachmentFields_RequireTheHostFeature(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(independentHomeContributionJSON(t), &root); err != nil {
		t.Fatal(err)
	}
	home := root["assistant_program_homes"].([]any)[0].(map[string]any)
	var studio map[string]any
	if err := json.Unmarshal(resourceContractFile(t, "home-attachment.valid.json"), &studio); err != nil {
		t.Fatal(err)
	}
	home["allowed_project_attachments"] = append(home["allowed_project_attachments"].([]any), studio)
	encoded, _ := json.Marshal(root)
	if _, err := ParseSurfaceContribution(encoded); !ContributionErrorIs(err, CodeHostFeatureUnsupported) {
		t.Fatalf("without the feature: %v", err)
	}
	root["requires_host_features"] = []any{HostFeatureIndependentProgramHomesV1, HostFeatureResourceWorkspacesV1}
	encoded, _ = json.Marshal(root)
	contribution, err := ParseSurfaceContribution(encoded)
	if err != nil {
		t.Fatalf("with the feature: %v", err)
	}
	attachment, ok := contribution.AssistantProgramHomes[0].ResourceAttachment()
	if !ok || attachment.Label != "REAPER Studio" {
		t.Fatalf("resource attachment = %+v, %v", attachment, ok)
	}
}

func resourceBlueprintDescriptor(t *testing.T, template string, withCatalog bool, features ...string) PluginDescriptor {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "blueprints", "reaper-studio", "template.json"), template)
	writeFile(t, filepath.Join(root, "blueprints", "reaper-studio", "workspace", "README.md"), "# Studio\n")
	contribution := &SurfaceContribution{
		RequiresHostFeatures: features,
		Blueprints: []ContributedBlueprint{{
			ID: "reaper-studio", Version: 1,
			Manifest: "blueprints/reaper-studio/template.json", Skeleton: "blueprints/reaper-studio/workspace",
		}},
	}
	if withCatalog {
		contribution.ResourceCatalog = resourceContractFile(t, "declaration.valid.json")
	}
	return PluginDescriptor{Name: "reaper-plugin", Version: "0.11.0", InstallDir: root, WorkspaceSurfaces: contribution}
}

func TestResolvePluginBlueprints_PairsTheResourceTeamWithItsCatalog(t *testing.T) {
	studio := string(resourceContractFile(t, "studio-template.valid.json"))
	all := []string{HostFeatureTemplateGroupRequirementsV1, HostFeatureIndependentProgramHomesV1, HostFeatureResourceWorkspacesV1}

	resolved, err := ResolvePluginBlueprints(resourceBlueprintDescriptor(t, studio, true, all...))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resolved) != 1 || !resolved[0].Template.AssistantProject.IsResource() {
		t.Fatalf("resolved = %+v", resolved)
	}

	if _, err := ResolvePluginBlueprints(resourceBlueprintDescriptor(t, studio, true, all[:2]...)); err == nil ||
		!strings.Contains(err.Error(), HostFeatureResourceWorkspacesV1) {
		t.Fatalf("a resource team resolved without its host feature: %v", err)
	}
	if _, err := ResolvePluginBlueprints(resourceBlueprintDescriptor(t, studio, false, all...)); err == nil {
		t.Fatal("a resource team resolved without a catalog naming it")
	}
	song := strings.Replace(studio, `"workspace_kind": "resource",`, "", 1)
	if song == studio {
		t.Fatal("fixture no longer declares workspace_kind")
	}
	if _, err := ResolvePluginBlueprints(resourceBlueprintDescriptor(t, song, true, all...)); err == nil {
		t.Fatal("a catalog claimed a project team")
	}
}
