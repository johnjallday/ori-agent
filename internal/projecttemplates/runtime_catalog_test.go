package projecttemplates

import (
	"os"
	"path/filepath"
	"testing"
)

type testRuntimeCatalog struct {
	capabilities map[string]bool
	adapters     map[string]bool
}

func (c testRuntimeCatalog) HasCapability(id string) bool     { return c.capabilities[id] }
func (c testRuntimeCatalog) HasRuntimeAdapter(id string) bool { return c.adapters[id] }

func TestLoadFolderUsesInjectedCapabilityAndProviderCatalog(t *testing.T) {
	root := t.TempDir()
	manifest := `{
		"name":"Plugin Blueprint",
		"agents":[{"name":"Lead"}],
		"capabilities":[{"id":"demo-tools","source":"plugin-blueprint"}],
		"runtime_requirements":{
			"schema_version":1,
			"operating_modes":[{"id":"standard","label":"Standard","description":"Use provider.","requires":["demo_runtime"]}],
			"requirements":[{"key":"demo_runtime","label":"Demo","description":"Configure provider.","adapter":"plugin:demo:provider"}]
		}
	}`
	if err := os.WriteFile(filepath.Join(root, ManifestFileName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := testRuntimeCatalog{
		capabilities: map[string]bool{"demo-tools": true},
		adapters:     map[string]bool{"plugin:demo:provider": true},
	}
	template, err := LoadFolderWithCatalog(root, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(template.Capabilities) != 1 || template.RuntimeRequirements == nil || template.RuntimeRequirementsError != "" {
		t.Fatalf("injected template = %+v", template)
	}

	withoutProvider, err := LoadFolderWithCatalog(root, testRuntimeCatalog{capabilities: catalog.capabilities})
	if err != nil {
		t.Fatal(err)
	}
	if withoutProvider.RuntimeRequirements != nil || withoutProvider.RuntimeRequirementsError == "" {
		t.Fatalf("missing provider did not fail closed: %+v", withoutProvider)
	}
}

func TestDefaultRuntimeCatalogKeepsOlderBuiltinTemplatesResolvable(t *testing.T) {
	catalog := defaultRuntimeCatalog()
	if !catalog.HasCapability("file-janitor") || !catalog.HasRuntimeAdapter("reaper_live_control") {
		t.Fatal("default built-in catalog lost an existing provider")
	}
	if catalog.HasCapability("unknown-plugin-capability") || catalog.HasRuntimeAdapter("plugin:missing:provider") {
		t.Fatal("default catalog accepted a dynamic unknown provider")
	}
}
