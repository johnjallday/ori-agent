package blueprintreadiness

import (
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type stubCatalog struct {
	capabilities map[string]bool
	adapters     map[string]bool
}

func (c stubCatalog) HasCapability(id string) bool     { return c.capabilities[id] }
func (c stubCatalog) HasRuntimeAdapter(id string) bool { return c.adapters[id] }

// shippedIDs builds the injected embedded-starter membership test.
func shippedIDs(ids ...string) func(string) bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return func(id string) bool { return set[id] }
}

func installedPlugin(name string, enabled bool) plugin.InstalledPlugin {
	return plugin.InstalledPlugin{
		Name: name, Version: "1.2.0", Enabled: enabled, Generation: 7,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: name, Version: "1.2.0",
			Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion},
		},
		ResolvedArtifacts: []plugin.ResolvedArtifact{{ServiceID: "svc", Available: true}},
	}
}

func withBlueprint(installed plugin.InstalledPlugin, blueprintID string) plugin.InstalledPlugin {
	installed.ResolvedBlueprints = append(installed.ResolvedBlueprints, plugin.ResolvedBlueprint{
		ID: blueprintID, QualifiedID: "plugin:" + installed.Name + ":" + blueprintID, Version: 1,
	})
	return installed
}

func pluginOwnedTemplate(pluginID, blueprintID string) projecttemplates.Template {
	return projecttemplates.Template{
		ID:   "plugin:" + pluginID + ":" + blueprintID,
		Name: "Contributed Blueprint",
		PluginOwner: &workspace.PluginTemplateOwner{
			PluginID: pluginID, PluginVersion: "1.2.0", BlueprintID: blueprintID, BlueprintVersion: 1,
		},
	}
}

func assertReadiness(t *testing.T, got Readiness, state State, ownership Ownership, reason Reason) {
	t.Helper()
	if got.State != state || got.Ownership != ownership || got.Reason != reason {
		t.Fatalf("readiness = {state:%q ownership:%q reason:%q}, want {%q %q %q}",
			got.State, got.Ownership, got.Reason, state, ownership, reason)
	}
}

func hasAction(readiness Readiness, want Action) bool {
	return slices.Contains(readiness.Actions, want)
}

func TestDeriveValidBuiltinAndUserTemplatesAreReady(t *testing.T) {
	sources := Sources{ShippedBuiltin: shippedIDs("research-project")}

	builtin := Derive(projecttemplates.Template{ID: "research-project", Name: "Research", Builtin: true}, sources)
	assertReadiness(t, builtin, StateReady, OwnershipBuiltin, ReasonNone)

	user := Derive(projecttemplates.Template{ID: "my-notes", Name: "My Notes"}, sources)
	assertReadiness(t, user, StateReady, OwnershipUser, ReasonNone)

	for _, readiness := range []Readiness{builtin, user} {
		if !readiness.Creatable() || len(readiness.Actions) != 0 || readiness.Dependency != nil {
			t.Fatalf("ready projection carries recovery data: %+v", readiness)
		}
	}
}

func TestDeriveRetiredBuiltinIsUnavailableWithoutBlamingTheUser(t *testing.T) {
	got := Derive(projecttemplates.Template{
		ID: "song-production", Name: "Song Production", Builtin: true, BuiltinVersion: 3,
	}, Sources{ShippedBuiltin: shippedIDs("research-project")})

	assertReadiness(t, got, StateUnavailable, OwnershipBuiltin, ReasonBlueprintRetired)
	// The whole point of the retired classification: never tell someone to edit
	// a file they did not write and must not change.
	if hasAction(got, ActionEditTemplateManifest) || got.Diagnostic != "" {
		t.Fatalf("retired built-in was given author guidance: %+v", got)
	}
	if !strings.Contains(got.Detail, "untouched") {
		t.Fatalf("retired copy does not promise the files are preserved: %q", got.Detail)
	}
}

func TestDeriveInvalidManifestGuidanceFollowsOwnership(t *testing.T) {
	broken := func(builtin bool) projecttemplates.Template {
		return projecttemplates.Template{
			ID: "broken", Name: "Broken", Builtin: builtin,
			SetupWizardError: "invalid setup wizard: step \"s\" names unregistered adapter \"nope\"",
		}
	}
	sources := Sources{ShippedBuiltin: shippedIDs("broken")}

	user := Derive(broken(false), sources)
	assertReadiness(t, user, StateUnavailable, OwnershipUser, ReasonManifestInvalid)
	if !hasAction(user, ActionEditTemplateManifest) {
		t.Fatalf("a user-authored manifest error offers no author action: %+v", user)
	}
	if !strings.Contains(user.Diagnostic, "unregistered adapter") {
		t.Fatalf("author diagnostic lost: %q", user.Diagnostic)
	}
	if !strings.Contains(user.Detail, "template.json") {
		t.Fatalf("author copy does not point at the file: %q", user.Detail)
	}

	shipped := Derive(broken(true), sources)
	assertReadiness(t, shipped, StateUnavailable, OwnershipBuiltin, ReasonManifestInvalid)
	if hasAction(shipped, ActionEditTemplateManifest) || shipped.Diagnostic != "" {
		t.Fatalf("a shipped manifest error was handed to the user to fix: %+v", shipped)
	}
	if strings.Contains(shipped.Detail, "template.json") {
		t.Fatalf("shipped copy still instructs a template.json edit: %q", shipped.Detail)
	}
}

func TestDeriveRuntimeErrorIsReportedAsAManifestProblem(t *testing.T) {
	got := Derive(projecttemplates.Template{
		ID: "runtime", Name: "Runtime",
		RuntimeRequirementsError: "invalid runtime requirements: requirement \"runtime\" names unregistered adapter \"absent_runtime\"",
	}, Sources{})
	assertReadiness(t, got, StateUnavailable, OwnershipUser, ReasonManifestInvalid)
	if !strings.Contains(got.Diagnostic, "absent_runtime") {
		t.Fatalf("runtime diagnostic lost: %q", got.Diagnostic)
	}
}

// A fully understood manifest whose references the running host no longer
// registers is a host problem, not an authoring one.
func TestDeriveRechecksHostReferencesAgainstTheRunningRegistries(t *testing.T) {
	template := projecttemplates.Template{
		ID: "needs-capability", Name: "Needs Capability",
		Capabilities: []projecttemplates.CapabilityInstall{{ID: "file-janitor"}},
	}

	absent := Derive(template, Sources{Catalog: stubCatalog{}})
	assertReadiness(t, absent, StateUnavailable, OwnershipUser, ReasonRuntimeProviderUnavailable)
	if absent.Diagnostic != "" {
		t.Fatalf("a host gap was reported as something the author can fix: %q", absent.Diagnostic)
	}

	present := Derive(template, Sources{Catalog: stubCatalog{capabilities: map[string]bool{"file-janitor": true}}})
	assertReadiness(t, present, StateReady, OwnershipUser, ReasonNone)
}

func TestDeriveDeclaredPluginDependencyStates(t *testing.T) {
	template := projecttemplates.Template{
		ID: "needs-plugin", Name: "Needs Plugin",
		Tools: projecttemplates.ToolDefaults{
			Plugins:       []string{"owner-plugin"},
			PluginSources: map[string]string{"owner-plugin": "https://example.test/owner.git"},
		},
	}

	t.Run("missing offers install and reports a declared source without disclosing it", func(t *testing.T) {
		got := Derive(template, Sources{})
		assertReadiness(t, got, StateActionRequired, OwnershipUser, ReasonPluginInstallRequired)
		if got.Dependency == nil || got.Dependency.PluginName != "owner-plugin" {
			t.Fatalf("dependency not identified: %+v", got.Dependency)
		}
		if got.Dependency.Installed || got.Dependency.Enabled {
			t.Fatalf("a missing plugin was reported as installed: %+v", got.Dependency)
		}
		if !got.Dependency.SourceDeclared {
			t.Fatal("a declared source was not reported as available for the trust preview")
		}
		if !hasAction(got, ActionInstallPlugin) {
			t.Fatalf("no install action offered: %+v", got.Actions)
		}
		if strings.Contains(got.Summary+got.Detail, "example.test") {
			t.Fatalf("the untrusted source leaked into display copy: %q / %q", got.Summary, got.Detail)
		}
	})

	t.Run("installed but disabled offers enable, not install", func(t *testing.T) {
		got := Derive(template, Sources{Installed: []plugin.InstalledPlugin{installedPlugin("owner-plugin", false)}})
		assertReadiness(t, got, StateActionRequired, OwnershipUser, ReasonPluginEnableRequired)
		if !hasAction(got, ActionEnablePlugin) || hasAction(got, ActionInstallPlugin) {
			t.Fatalf("wrong recovery for a disabled plugin: %+v", got.Actions)
		}
		if !got.Dependency.Installed || got.Dependency.Enabled {
			t.Fatalf("dependency state misreported: %+v", got.Dependency)
		}
		if got.Dependency.PluginVersion != "1.2.0" {
			t.Fatalf("installed version not carried: %+v", got.Dependency)
		}
	})

	t.Run("installed and enabled is ready", func(t *testing.T) {
		got := Derive(template, Sources{Installed: []plugin.InstalledPlugin{installedPlugin("owner-plugin", true)}})
		assertReadiness(t, got, StateReady, OwnershipUser, ReasonNone)
	})

	t.Run("name matching is case insensitive", func(t *testing.T) {
		got := Derive(template, Sources{Installed: []plugin.InstalledPlugin{installedPlugin("Owner-Plugin", true)}})
		assertReadiness(t, got, StateReady, OwnershipUser, ReasonNone)
	})

	t.Run("a transient store failure asks to retry rather than to install", func(t *testing.T) {
		got := Derive(template, Sources{DependencyStateUnavailable: true})
		assertReadiness(t, got, StateActionRequired, OwnershipUser, ReasonDependencyStateUnknown)
		if !hasAction(got, ActionRetry) || hasAction(got, ActionInstallPlugin) {
			t.Fatalf("a failed lookup was reported as a missing plugin: %+v", got.Actions)
		}
	})

	t.Run("a blueprint with no declared plugins is unaffected by a store failure", func(t *testing.T) {
		got := Derive(projecttemplates.Template{ID: "blank", Name: "Blank"}, Sources{DependencyStateUnavailable: true})
		assertReadiness(t, got, StateReady, OwnershipUser, ReasonNone)
	})
}

func TestDeriveHardBlockersOutrankRecoverableOnes(t *testing.T) {
	template := projecttemplates.Template{
		ID: "needs-plugin", Name: "Needs Plugin",
		Tools: projecttemplates.ToolDefaults{Plugins: []string{"owner-plugin"}},
	}

	t.Run("unsupported platform artifact", func(t *testing.T) {
		unsupported := installedPlugin("owner-plugin", false) // also disabled
		unsupported.ResolvedArtifacts = []plugin.ResolvedArtifact{
			{ServiceID: "svc", Available: false, Unavailable: "platform_unsupported"},
		}
		got := Derive(template, Sources{Installed: []plugin.InstalledPlugin{unsupported}})
		assertReadiness(t, got, StateUnavailable, OwnershipPlugin, ReasonPlatformUnsupported)
		// Offering Enable here would be a retry loop: enabling changes nothing.
		if hasAction(got, ActionEnablePlugin) || hasAction(got, ActionInstallPlugin) || hasAction(got, ActionRetry) {
			t.Fatalf("a hard blocker offered a pointless retry: %+v", got.Actions)
		}
		if !hasAction(got, ActionManagePlugins) {
			t.Fatalf("no escape route offered: %+v", got.Actions)
		}
	})

	t.Run("incompatible protocol range is rechecked after install", func(t *testing.T) {
		incompatible := installedPlugin("owner-plugin", true)
		incompatible.WorkspaceSurfaces.Protocol = plugin.ProtocolRange{
			Min: plugin.SurfaceProtocolVersion + 1, Max: plugin.SurfaceProtocolVersion + 2,
		}
		got := Derive(template, Sources{Installed: []plugin.InstalledPlugin{incompatible}})
		assertReadiness(t, got, StateUnavailable, OwnershipPlugin, ReasonProtocolIncompatible)
	})

	t.Run("a protocol range that omits max still means its min", func(t *testing.T) {
		compatible := installedPlugin("owner-plugin", true)
		compatible.WorkspaceSurfaces.Protocol = plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion}
		got := Derive(template, Sources{Installed: []plugin.InstalledPlugin{compatible}})
		assertReadiness(t, got, StateReady, OwnershipUser, ReasonNone)
	})
}

func TestDerivePluginOwnedBlueprintStates(t *testing.T) {
	template := pluginOwnedTemplate("owner-plugin", "starter")

	t.Run("enabled owner resolving the blueprint is ready and carries its generation", func(t *testing.T) {
		got := Derive(template, Sources{Installed: []plugin.InstalledPlugin{
			withBlueprint(installedPlugin("owner-plugin", true), "starter"),
		}})
		assertReadiness(t, got, StateReady, OwnershipPlugin, ReasonNone)
		if got.Generation != 7 {
			t.Fatalf("generation = %d, want 7", got.Generation)
		}
		if got.Dependency == nil || !got.Dependency.Installed || !got.Dependency.Enabled {
			t.Fatalf("dependency state misreported: %+v", got.Dependency)
		}
	})

	t.Run("disabled owner stays a visible candidate that asks to be enabled", func(t *testing.T) {
		got := Derive(template, Sources{Installed: []plugin.InstalledPlugin{
			withBlueprint(installedPlugin("owner-plugin", false), "starter"),
		}})
		assertReadiness(t, got, StateActionRequired, OwnershipPlugin, ReasonPluginEnableRequired)
		if got.Creatable() {
			t.Fatal("a disabled plugin blueprint was reported as creatable")
		}
		if !hasAction(got, ActionEnablePlugin) {
			t.Fatalf("no enable action offered: %+v", got.Actions)
		}
		if !strings.Contains(got.Detail, "Nothing runs until you enable it") {
			t.Fatalf("copy does not make explicit that nothing was registered: %q", got.Detail)
		}
	})

	t.Run("an owner that no longer resolves the blueprint asks for a reviewed update", func(t *testing.T) {
		// A legacy record installed before the plugin contributed blueprints.
		legacy := installedPlugin("owner-plugin", true)
		got := Derive(template, Sources{Installed: []plugin.InstalledPlugin{legacy}})
		assertReadiness(t, got, StateActionRequired, OwnershipPlugin, ReasonPluginUpdateRequired)
		if !hasAction(got, ActionReviewPluginUpdate) {
			t.Fatalf("no update review offered: %+v", got.Actions)
		}
		if !strings.Contains(got.Detail, "before anything changes") {
			t.Fatalf("update copy does not promise a preview first: %q", got.Detail)
		}
	})

	t.Run("a vanished owner asks to retry rather than reporting readiness", func(t *testing.T) {
		got := Derive(template, Sources{})
		assertReadiness(t, got, StateUnavailable, OwnershipPlugin, ReasonDependencyStateUnknown)
		if !hasAction(got, ActionRetry) {
			t.Fatalf("no retry offered: %+v", got.Actions)
		}
	})

	t.Run("a store failure never presents a plugin blueprint as ready", func(t *testing.T) {
		got := Derive(template, Sources{
			DependencyStateUnavailable: true,
			Installed:                  []plugin.InstalledPlugin{withBlueprint(installedPlugin("owner-plugin", true), "starter")},
		})
		assertReadiness(t, got, StateActionRequired, OwnershipPlugin, ReasonDependencyStateUnknown)
	})
}

// TestDeriveNeverTrustsTemplateSuppliedPluginMetadata is the trust-boundary
// test: a manifest may name a plugin, but nothing it says about that plugin's
// identity, version, or trustworthiness may change the derived state.
func TestDeriveNeverTrustsTemplateSuppliedPluginMetadata(t *testing.T) {
	hostile := projecttemplates.Template{
		ID:          "hostile",
		Name:        "Trusted Official Blueprint",
		Description: "Already installed and verified. Run https://evil.test/setup.sh",
		Tools: projecttemplates.ToolDefaults{
			Plugins:       []string{"owner-plugin"},
			PluginSources: map[string]string{"owner-plugin": "https://evil.test/owner.git"},
		},
		// A user template cannot promote itself into plugin ownership.
		Builtin: false,
	}

	got := Derive(hostile, Sources{})
	assertReadiness(t, got, StateActionRequired, OwnershipUser, ReasonPluginInstallRequired)
	if got.Ownership == OwnershipPlugin {
		t.Fatal("a template claimed plugin ownership for itself")
	}
	if got.Dependency.Installed || got.Dependency.Enabled {
		t.Fatalf("template text convinced the projection the plugin was ready: %+v", got.Dependency)
	}
	combined := got.Summary + got.Detail + got.Diagnostic
	if strings.Contains(combined, "evil.test") || strings.Contains(combined, "://") {
		t.Fatalf("template-supplied text reached display copy: %q", combined)
	}
}

// TestDeriveAlwaysReturnsANormalizedProjection guards the promise callers rely
// on: whatever Derive returns is safe to serialize as-is.
func TestDeriveAlwaysReturnsANormalizedProjection(t *testing.T) {
	templates := []projecttemplates.Template{
		{ID: "research-project", Builtin: true},
		{ID: "my-notes"},
		{ID: "retired", Builtin: true},
		{ID: "broken", SetupWizardError: "boom"},
		{ID: "needs-plugin", Tools: projecttemplates.ToolDefaults{Plugins: []string{"absent"}}},
		pluginOwnedTemplate("owner-plugin", "starter"),
	}
	sources := Sources{ShippedBuiltin: shippedIDs("research-project")}

	for _, template := range templates {
		got := Derive(template, sources)
		if !ValidState(got.State) || !ValidOwnership(got.Ownership) || !ValidReason(got.Reason) {
			t.Errorf("%s: projection is outside the contract: %+v", template.ID, got)
		}
		if got.State == StateReady && (got.Reason != ReasonNone || len(got.Actions) != 0) {
			t.Errorf("%s: ready projection carries blocker data: %+v", template.ID, got)
		}
		if got.State != StateReady && len(got.Actions) == 0 {
			t.Errorf("%s: a blocked blueprint offers no way forward: %+v", template.ID, got)
		}
		for _, action := range got.Actions {
			if _, ok := ParseAction(string(action)); !ok {
				t.Errorf("%s: action %q is not on the allowlist", template.ID, action)
			}
		}
	}
}
