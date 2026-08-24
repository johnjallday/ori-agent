package plugin

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

func installedSurfacePlugin(t *testing.T, enabled, artifactAvailable bool) InstalledPlugin {
	t.Helper()
	descriptor := canonicalSurfaceDescriptor(t)
	artifact := descriptor.WorkspaceSurfaces.Services[0].Artifacts[0]
	return InstalledPlugin{
		Name: descriptor.Name, Version: descriptor.Version, InstallDir: descriptor.InstallDir,
		WorkspaceSurfaces: descriptor.WorkspaceSurfaces, ComponentFingerprint: trustedComponentFingerprint(descriptor),
		Generation: 7, Enabled: enabled,
		ResolvedArtifacts: []ResolvedArtifact{{
			ServiceID: "demo-service", ArtifactID: artifact.ID, OS: "darwin", Arch: "arm64",
			SHA256: artifact.SHA256, Size: artifact.Size,
			ManagedPath: "/managed/artifacts/demo-service", Available: artifactAvailable,
			Unavailable: func() string {
				if artifactAvailable {
					return ""
				}
				return "platform_unsupported"
			}(),
		}},
	}
}

func TestSurfaceLifecycleRegistersEnabledContributionAtomically(t *testing.T) {
	registry := workspacesurface.NewRegistry()
	lifecycle := NewSurfaceLifecycle(registry, workspacesurface.NewServiceManager(func(workspacesurface.ServiceSpec) workspacesurface.ServiceProcess {
		t.Fatal("registration must not start a service")
		return nil
	}))
	installed := installedSurfacePlugin(t, true, true)
	if err := lifecycle.RegisterInstalled(installed); err != nil {
		t.Fatalf("RegisterInstalled() error = %v", err)
	}
	surfaces := registry.SurfacesForOwner(workspacesurface.OwnerPlugin, installed.Name)
	if len(surfaces) != 1 || !surfaces[0].Available || surfaces[0].Owner.Generation != 7 {
		t.Fatalf("registered surfaces = %+v", surfaces)
	}
	binding, ok := registry.Binding(surfaces[0].Key)
	if !ok || binding.Runtime == nil || binding.EntryAsset != "ui/index.html" || len(binding.Operations) != 3 || surfaces[0].Surface.SetupProviderID != "plugin:workspace-surface-demo:demo-runtime" {
		// The canonical surface declares status.read, greeting.create, and
		// setting.validate. Provider-only operations remain private to the
		// service and are not frame-callable.
		t.Fatalf("trusted binding = %+v, %v", binding, ok)
	}
}

func TestSurfaceLifecycleRegistersPluginCapabilitiesOnlyWhileEnabled(t *testing.T) {
	surfaces := workspacesurface.NewRegistry()
	capabilities, err := workspacecapability.NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := NewSurfaceLifecycle(surfaces, nil)
	lifecycle.SetCapabilityRegistry(capabilities)
	installed := installedSurfacePlugin(t, true, true)
	if err := lifecycle.RegisterInstalled(installed); err != nil {
		t.Fatal(err)
	}
	definition, ok := capabilities.Definition("demo-tools")
	if !ok || definition.Owner == nil || !definition.Owner.MatchesPlugin(installed.Name) {
		t.Fatalf("plugin definition = %+v, %v", definition, ok)
	}

	disabled := installed
	disabled.Enabled = false
	disabled.Generation++
	if err := lifecycle.Replace(installed, disabled); err != nil {
		t.Fatal(err)
	}
	if capabilities.Has("demo-tools") {
		t.Fatal("disabled plugin definition remained executable in the catalog")
	}
	recordOwner := workspace.CapabilityOwner{
		Kind: workspace.CapabilityOwnerPlugin, PluginID: installed.Name, PluginVersion: installed.Version,
	}
	resolved := capabilities.Resolve(workspace.InstalledCapability{ID: "demo-tools", Version: 1, Owner: &recordOwner})
	if resolved.Available || resolved.Definition.Owner == nil {
		t.Fatalf("disabled install projection = %+v", resolved)
	}
}

func TestSurfaceLifecycleKeepsDisabledAndUnsupportedContributionsInert(t *testing.T) {
	for _, test := range []struct {
		name     string
		plugin   InstalledPlugin
		wantCode string
	}{
		{name: "disabled", plugin: installedSurfacePlugin(t, false, true), wantCode: "plugin_disabled"},
		{name: "unsupported platform", plugin: installedSurfacePlugin(t, true, false), wantCode: "platform_unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := workspacesurface.NewRegistry()
			lifecycle := NewSurfaceLifecycle(registry, nil)
			if err := lifecycle.RegisterInstalled(test.plugin); err != nil {
				t.Fatal(err)
			}
			surfaces := registry.Surfaces()
			if len(surfaces) != 1 || surfaces[0].Available || surfaces[0].UnavailableCode != test.wantCode {
				t.Fatalf("inert surface = %+v", surfaces)
			}
			if _, ok := registry.Binding(surfaces[0].Key); ok {
				t.Fatal("inert surface retained executable binding")
			}
		})
	}
}

func TestSurfaceLifecycleRejectsOwnerCollisionAndUnregistersExactGeneration(t *testing.T) {
	registry := workspacesurface.NewRegistry()
	lifecycle := NewSurfaceLifecycle(registry, nil)
	installed := installedSurfacePlugin(t, true, true)
	if err := lifecycle.RegisterInstalled(installed); err != nil {
		t.Fatal(err)
	}
	collision := installed
	collision.Generation = 8
	if err := lifecycle.RegisterInstalled(collision); err == nil {
		t.Fatal("owner collision was accepted")
	}
	if len(registry.Surfaces()) != 1 || registry.Surfaces()[0].Owner.Generation != 7 {
		t.Fatalf("collision partially replaced registry: %+v", registry.Surfaces())
	}
	if err := lifecycle.Unregister(installed.Name, installed.Generation); err != nil {
		t.Fatal(err)
	}
	if len(registry.Surfaces()) != 0 {
		t.Fatalf("unregister left surfaces: %+v", registry.Surfaces())
	}
}

func TestSurfaceLifecycleInvalidatesSessionsBeforeUnregister(t *testing.T) {
	registry := workspacesurface.NewRegistry()
	lifecycle := NewSurfaceLifecycle(registry, nil)
	installed := installedSurfacePlugin(t, true, true)
	if err := lifecycle.RegisterInstalled(installed); err != nil {
		t.Fatal(err)
	}
	invalidated := false
	lifecycle.SetSessionInvalidator(func(pluginID string, generation uint64) {
		invalidated = true
		if pluginID != installed.Name || generation != installed.Generation {
			t.Fatalf("invalidated owner = %s@%d", pluginID, generation)
		}
		if len(registry.Surfaces()) != 1 {
			t.Fatal("registry was removed before session invalidation")
		}
	})
	if err := lifecycle.Unregister(installed.Name, installed.Generation); err != nil {
		t.Fatal(err)
	}
	if !invalidated || len(registry.Surfaces()) != 0 {
		t.Fatalf("invalidated=%v surfaces=%+v", invalidated, registry.Surfaces())
	}
}

func TestSurfaceLifecycleRestoreIgnoresMCPOnlyPlugins(t *testing.T) {
	registry := workspacesurface.NewRegistry()
	lifecycle := NewSurfaceLifecycle(registry, nil)
	if err := lifecycle.Restore([]InstalledPlugin{{Name: "portable", Enabled: true}, installedSurfacePlugin(t, true, true)}); err != nil {
		t.Fatal(err)
	}
	if len(registry.Surfaces()) != 1 {
		t.Fatalf("restored surfaces = %+v", registry.Surfaces())
	}
}
