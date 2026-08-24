package workspacecapability

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func pluginCapabilityOwner(id, version string) workspace.CapabilityOwner {
	return workspace.CapabilityOwner{
		Kind: workspace.CapabilityOwnerPlugin, PluginID: id, PluginVersion: version,
	}
}

func pluginCapabilityDefinition(owner workspace.CapabilityOwner, id string) Definition {
	return Definition{
		ID: id, Version: 1, Owner: &owner,
		Display: Display{Name: "Plugin capability", Summary: "Inert plugin metadata."},
	}
}

func pluginCapabilityRecord(owner workspace.CapabilityOwner, id string) workspace.InstalledCapability {
	return workspace.InstalledCapability{
		ID: id, Version: 1, InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace, Owner: &owner,
	}
}

func TestRegistryRegistersAndUnregistersOwnerAwarePluginDefinitions(t *testing.T) {
	registry := mustBuiltinRegistry(t)
	owner := pluginCapabilityOwner("weather-tools", "1.2.0")
	if err := registry.RegisterPluginDefinitions(owner, []Definition{
		pluginCapabilityDefinition(owner, "forecast"),
		pluginCapabilityDefinition(owner, "alerts"),
	}); err != nil {
		t.Fatal(err)
	}
	resolved := registry.Resolve(pluginCapabilityRecord(owner, "forecast"))
	if !resolved.Available || resolved.Definition.Owner == nil || resolved.Definition.Owner.PluginID != "weather-tools" {
		t.Fatalf("resolved plugin capability = %+v", resolved)
	}

	registry.UnregisterPluginDefinitions(owner.PluginID)
	resolved = registry.Resolve(pluginCapabilityRecord(owner, "forecast"))
	if resolved.Available || resolved.Unavailable == "" || resolved.Definition.Owner == nil {
		t.Fatalf("unavailable plugin capability = %+v", resolved)
	}
	if !registry.Has(workspace.CapabilityFileJanitor) {
		t.Fatal("plugin unregister removed a built-in definition")
	}
}

func TestRegistryRejectsPluginCollisionsAtomically(t *testing.T) {
	registry := mustBuiltinRegistry(t)
	owner := pluginCapabilityOwner("collision-tools", "1.0.0")
	err := registry.RegisterPluginDefinitions(owner, []Definition{
		pluginCapabilityDefinition(owner, "safe-new-id"),
		pluginCapabilityDefinition(owner, workspace.CapabilityFileJanitor),
	})
	if err == nil {
		t.Fatal("built-in collision was accepted")
	}
	if registry.Has("safe-new-id") {
		t.Fatal("failed batch partially registered a plugin definition")
	}

	other := pluginCapabilityOwner("other-tools", "1.0.0")
	if err := registry.RegisterPluginDefinitions(owner, []Definition{pluginCapabilityDefinition(owner, "shared-id")}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterPluginDefinitions(other, []Definition{pluginCapabilityDefinition(other, "shared-id")}); err == nil {
		t.Fatal("cross-plugin ID collision was accepted")
	}
}

func TestRegistryNeverLetsOnePluginOwnerClaimAnotherWorkspaceRecord(t *testing.T) {
	registry := mustBuiltinRegistry(t)
	ownerA := pluginCapabilityOwner("owner-a", "1.0.0")
	ownerB := pluginCapabilityOwner("owner-b", "1.0.0")
	if err := registry.RegisterPluginDefinitions(ownerA, []Definition{pluginCapabilityDefinition(ownerA, "shared")}); err != nil {
		t.Fatal(err)
	}
	if resolved := registry.Resolve(pluginCapabilityRecord(ownerB, "shared")); resolved.Available {
		t.Fatalf("foreign owner activated definition: %+v", resolved)
	}
	legacy := pluginCapabilityRecord(ownerA, "shared")
	legacy.Owner = nil
	if resolved := registry.Resolve(legacy); resolved.Available {
		t.Fatalf("ownerless record activated plugin definition: %+v", resolved)
	}
}
