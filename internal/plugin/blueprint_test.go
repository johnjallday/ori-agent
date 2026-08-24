package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func blueprintDescriptorFixture(t *testing.T) PluginDescriptor {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "blueprints", "demo", "template.json"), `{
		"name":"Demo Workspace",
		"description":"Created from an installed plugin blueprint.",
		"agents":[{"name":"Demo Lead"}],
		"capabilities":[{"id":"demo-tools","source":"plugin-blueprint"}],
		"runtime_requirements":{
			"schema_version":1,
			"operating_modes":[{"id":"standard","label":"Standard","description":"Use the demo runtime.","requires":["demo_runtime"]}],
			"requirements":[{"key":"demo_runtime","label":"Demo runtime","description":"Use the installed provider.","adapter":"plugin:workspace-surface-demo:demo-runtime"}]
		}
	}`)
	writeFile(t, filepath.Join(root, "blueprints", "demo", "project", "README.md"), "# {{WORKSPACE_NAME}}\n")
	return PluginDescriptor{
		Name: "workspace-surface-demo", Version: "0.1.0", InstallDir: root,
		WorkspaceSurfaces: &SurfaceContribution{
			Capabilities: []ContributedCapability{{
				ID: "demo-tools", Version: 1,
				RuntimeProvider: &ContributedRuntimeProvider{ID: "demo-runtime", RequirementKey: "demo_runtime"},
			}},
			Blueprints: []ContributedBlueprint{{
				ID: "demo-workspace", Version: 1,
				Manifest: "blueprints/demo/template.json", Skeleton: "blueprints/demo/project",
				Capabilities: []string{"demo-tools"},
			}},
		},
	}
}

func TestResolvePluginBlueprintsValidatesAndNamespacesTrustedTemplate(t *testing.T) {
	descriptor := blueprintDescriptorFixture(t)
	resolved, err := ResolvePluginBlueprints(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].QualifiedID != "plugin:workspace-surface-demo:demo-workspace" || resolved[0].SkeletonDigest == "" {
		t.Fatalf("resolved blueprints = %+v", resolved)
	}
	template := resolved[0].Template
	if template.PluginOwner == nil || template.PluginOwner.PluginID != descriptor.Name || template.Path != resolved[0].SkeletonRoot {
		t.Fatalf("trusted template = %+v", template)
	}
	if template.RuntimeRequirements == nil || template.RuntimeRequirements.Requirements[0].Adapter != "plugin:workspace-surface-demo:demo-runtime" {
		t.Fatalf("runtime requirements = %+v", template.RuntimeRequirements)
	}
}

func TestResolvePluginBlueprintsRejectsUnknownFieldsCapabilityDriftAndSymlinks(t *testing.T) {
	t.Run("unknown manifest field", func(t *testing.T) {
		descriptor := blueprintDescriptorFixture(t)
		writeFile(t, filepath.Join(descriptor.InstallDir, "blueprints", "demo", "template.json"), `{"name":"Demo","command":"run","capabilities":[{"id":"demo-tools"}]}`)
		if _, err := ResolvePluginBlueprints(descriptor); err == nil {
			t.Fatal("unknown executable field was accepted")
		}
	})
	t.Run("capability drift", func(t *testing.T) {
		descriptor := blueprintDescriptorFixture(t)
		writeFile(t, filepath.Join(descriptor.InstallDir, "blueprints", "demo", "template.json"), `{"name":"Demo","capabilities":[]}`)
		if _, err := ResolvePluginBlueprints(descriptor); err == nil {
			t.Fatal("blueprint capability mismatch was accepted")
		}
	})
	t.Run("skeleton symlink", func(t *testing.T) {
		descriptor := blueprintDescriptorFixture(t)
		link := filepath.Join(descriptor.InstallDir, "blueprints", "demo", "project", "escape")
		if err := os.Symlink(t.TempDir(), link); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolvePluginBlueprints(descriptor); err == nil {
			t.Fatal("skeleton symlink was accepted")
		}
	})
}
