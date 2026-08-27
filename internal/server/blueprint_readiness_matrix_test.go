package server

// The blueprint readiness matrix, end to end through the creation catalog.
//
// Every dependency state a blueprint can be in gets one row here, asserted
// against the projection a client actually receives. The rows exist as a set:
// a change that fixes one state by collapsing another into it fails here.
//
// Each row also states what the catalog looked like BEFORE this feature, so
// the reason a state is projected the way it is stays legible.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// blueprintMatrixCatalog is the trust boundary the template loader and the
// readiness derivation consult. Only what is listed here exists.
type blueprintMatrixCatalog struct {
	capabilities map[string]bool
	adapters     map[string]bool
}

func (c blueprintMatrixCatalog) HasCapability(id string) bool     { return c.capabilities[id] }
func (c blueprintMatrixCatalog) HasRuntimeAdapter(id string) bool { return c.adapters[id] }

func writeMatrixTemplate(t *testing.T, libDir, id, manifest string) {
	t.Helper()
	dir := filepath.Join(libDir, id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, projecttemplates.ManifestFileName), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o640); err != nil {
		t.Fatal(err)
	}
}

// projectCreationCatalog runs the same projection buildBlueprintCatalog runs,
// with the installed-plugin store supplied directly. dependencyStateUnknown
// models the handler's plugin-list failure branch.
func projectCreationCatalog(
	t *testing.T,
	libDir string,
	catalog projecttemplates.RuntimeCatalog,
	installed []plugin.InstalledPlugin,
	dependencyStateUnknown bool,
) []blueprintCatalogEntry {
	t.Helper()
	templates, err := projecttemplates.ListLibraryWithCatalog(libDir, catalog)
	if err != nil {
		t.Fatalf("ListLibraryWithCatalog: %v", err)
	}
	sources := blueprintreadiness.Sources{Catalog: catalog, DependencyStateUnavailable: dependencyStateUnknown}
	var candidates []pluginBlueprintCandidate
	if !dependencyStateUnknown {
		sources.Installed = installed
		candidates = candidatePluginBlueprintTemplates(installed)
	}
	merged := mergePluginBlueprintCandidates(templates, candidates)
	entries := make([]blueprintCatalogEntry, 0, len(merged))
	for _, template := range merged {
		entries = append(entries, blueprintCatalogEntry{
			Template:  template,
			Readiness: blueprintreadiness.Derive(template, sources),
		})
	}
	return entries
}

func findCatalogEntry(entries []blueprintCatalogEntry, id string) (blueprintCatalogEntry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return blueprintCatalogEntry{}, false
}

func catalogIDs(entries []blueprintCatalogEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func requireEntry(t *testing.T, entries []blueprintCatalogEntry, id string) blueprintCatalogEntry {
	t.Helper()
	entry, ok := findCatalogEntry(entries, id)
	if !ok {
		t.Fatalf("%q missing from catalog: %v", id, catalogIDs(entries))
	}
	return entry
}

func assertProjection(t *testing.T, entry blueprintCatalogEntry, state blueprintreadiness.State, ownership blueprintreadiness.Ownership, reason blueprintreadiness.Reason) {
	t.Helper()
	got := entry.Readiness
	if got.State != state || got.Ownership != ownership || got.Reason != reason {
		t.Fatalf("%q readiness = {state:%q ownership:%q reason:%q}, want {%q %q %q}",
			entry.ID, got.State, got.Ownership, got.Reason, state, ownership, reason)
	}
}

// matrixPlugin builds an installed-plugin record, always named "owner" in
// this file's fixtures, contributing one blueprint that shares an ID with the
// built-in it supersedes.
func matrixPlugin(blueprintID string, enabled bool, artifacts []plugin.ResolvedArtifact) plugin.InstalledPlugin {
	const name = "owner"
	owner := &workspace.PluginTemplateOwner{
		PluginID: name, PluginVersion: "1.0.0", BlueprintID: blueprintID, BlueprintVersion: 1,
	}
	qualified := "plugin:" + name + ":" + blueprintID
	return plugin.InstalledPlugin{
		Name: name, Version: "1.0.0", Enabled: enabled, Generation: 4,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: name, Version: "1.0.0",
			Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion},
		},
		ResolvedArtifacts: artifacts,
		ResolvedBlueprints: []plugin.ResolvedBlueprint{{
			ID: blueprintID, QualifiedID: qualified, Version: 1,
			Template: projecttemplates.Template{
				ID: qualified, Name: "Plugin " + blueprintID, PluginOwner: owner,
			},
			SkeletonRoot: filepath.Join(os.TempDir(), "matrix-skeleton"),
		}},
	}
}

func availableArtifacts() []plugin.ResolvedArtifact {
	return []plugin.ResolvedArtifact{{ServiceID: "svc", OS: "darwin", Arch: "arm64", Available: true}}
}

func unsupportedArtifacts() []plugin.ResolvedArtifact {
	return []plugin.ResolvedArtifact{{ServiceID: "svc", OS: "windows", Arch: "amd64", Available: false, Unavailable: "platform_unsupported"}}
}

// TestBlueprintCatalogMatrix_TemplateOwnedStates covers the rows the template
// library alone decides.
func TestBlueprintCatalogMatrix_TemplateOwnedStates(t *testing.T) {
	libDir := t.TempDir()
	catalog := blueprintMatrixCatalog{adapters: map[string]bool{"known_runtime": true}}

	writeMatrixTemplate(t, libDir, "research-project", `{
		"name":"Research Project","builtin":true,"builtin_version":1,
		"agents":[{"name":"Researcher"}]
	}`)
	writeMatrixTemplate(t, libDir, "my-notes", `{
		"name":"My Notes","agents":[{"name":"Notes Lead"}]
	}`)
	writeMatrixTemplate(t, libDir, "broken-user-template", `{
		"name":"Broken User Template","agents":[{"name":"Lead"}],
		"setup_wizard":{"version":1,"title":"Set up","steps":[
			{"id":"readiness","kind":"readiness","adapter":"not_a_real_adapter","required":true}
		]}
	}`)
	// Retired: claims built-in ownership under an ID this build no longer ships.
	writeMatrixTemplate(t, libDir, "song-production", `{
		"name":"Song Production","builtin":true,"builtin_version":3,
		"agents":[{"name":"Producer"}]
	}`)
	writeMatrixTemplate(t, libDir, "needs-plugin", `{
		"name":"Needs Plugin","agents":[{"name":"Lead"}],
		"tools":{"plugins":["absent-plugin"],"plugin_sources":{"absent-plugin":"https://example.test/absent.git"}}
	}`)
	writeMatrixTemplate(t, libDir, "unknown-runtime", `{
		"name":"Unknown Runtime","agents":[{"name":"Lead"}],
		"runtime_requirements":{"schema_version":1,
			"operating_modes":[
				{"id":"limited","label":"Limited","description":"Use files."},
				{"id":"assisted","label":"Assisted","description":"Use live control.","requires":["runtime"]}
			],
			"requirements":[{"key":"runtime","label":"Runtime","description":"Configure it.","adapter":"absent_runtime"}]}
	}`)

	entries := projectCreationCatalog(t, libDir, catalog, nil, false)

	t.Run("valid builtin is ready", func(t *testing.T) {
		entry := requireEntry(t, entries, "research-project")
		assertProjection(t, entry, blueprintreadiness.StateReady, blueprintreadiness.OwnershipBuiltin, blueprintreadiness.ReasonNone)
		if len(entry.Readiness.Actions) != 0 {
			t.Fatalf("a ready blueprint offers recovery actions: %+v", entry.Readiness)
		}
	})

	t.Run("valid user template is ready", func(t *testing.T) {
		entry := requireEntry(t, entries, "my-notes")
		assertProjection(t, entry, blueprintreadiness.StateReady, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonNone)
	})

	t.Run("invalid author-owned manifest keeps its author guidance", func(t *testing.T) {
		entry := requireEntry(t, entries, "broken-user-template")
		assertProjection(t, entry, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonManifestInvalid)
		if !strings.Contains(entry.Readiness.Diagnostic, "not_a_real_adapter") {
			t.Fatalf("author diagnostic lost: %q", entry.Readiness.Diagnostic)
		}
		if !strings.Contains(entry.Readiness.Detail, "template.json") {
			t.Fatalf("author copy does not point at the file: %q", entry.Readiness.Detail)
		}
	})

	t.Run("retired on-disk builtin is unavailable, not offered, and not blamed on the user", func(t *testing.T) {
		// Before: its `builtin: true` was trusted verbatim and it was offered
		// as a first-class built-in that created workspaces normally.
		if projecttemplates.IsBuiltinStarterID("song-production") {
			t.Skip("embedded starter catalog now ships this ID")
		}
		entry := requireEntry(t, entries, "song-production")
		assertProjection(t, entry, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipBuiltin, blueprintreadiness.ReasonBlueprintRetired)
		if entry.Readiness.Diagnostic != "" {
			t.Fatalf("a retired built-in was given an author diagnostic: %q", entry.Readiness.Diagnostic)
		}
		for _, action := range entry.Readiness.Actions {
			if action == blueprintreadiness.ActionEditTemplateManifest {
				t.Fatal("a retired built-in told the user to edit shipped JSON")
			}
		}
		// Preserving files is the whole contract: the folder is still there.
		if _, err := os.Stat(filepath.Join(libDir, "song-production", "seed.txt")); err != nil {
			t.Fatalf("classifying a built-in as retired touched its files: %v", err)
		}
	})

	t.Run("declared-but-missing plugin asks to install", func(t *testing.T) {
		// Before: projected as ready; the user learned about it only when
		// Create failed.
		entry := requireEntry(t, entries, "needs-plugin")
		assertProjection(t, entry, blueprintreadiness.StateActionRequired, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonPluginInstallRequired)
		if entry.Readiness.Dependency == nil || entry.Readiness.Dependency.PluginName != "absent-plugin" {
			t.Fatalf("dependency not identified: %+v", entry.Readiness.Dependency)
		}
		if !entry.Readiness.Dependency.SourceDeclared {
			t.Fatal("a declared source was not reported as available for the trust preview")
		}
	})

	t.Run("unavailable runtime provider is unavailable with the author's diagnostic", func(t *testing.T) {
		entry := requireEntry(t, entries, "unknown-runtime")
		assertProjection(t, entry, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonManifestInvalid)
		if !strings.Contains(entry.Readiness.Diagnostic, "absent_runtime") {
			t.Fatalf("runtime diagnostic lost: %q", entry.Readiness.Diagnostic)
		}
	})
}

// TestBlueprintCatalogMatrix_PluginOwnedStates covers the rows the installed
// plugin store decides.
func TestBlueprintCatalogMatrix_PluginOwnedStates(t *testing.T) {
	libDir := t.TempDir()
	catalog := blueprintMatrixCatalog{}
	if projecttemplates.IsBuiltinStarterID("song-production") {
		t.Skip("embedded starter catalog now ships this ID")
	}
	// The retired built-in the plugin blueprint exists to replace.
	writeMatrixTemplate(t, libDir, "song-production", `{
		"name":"Song Production","builtin":true,"builtin_version":3,
		"agents":[{"name":"Producer"}]
	}`)

	t.Run("enabled compatible plugin blueprint is ready and supersedes the builtin", func(t *testing.T) {
		entries := projectCreationCatalog(t, libDir, catalog,
			[]plugin.InstalledPlugin{matrixPlugin("song-production", true, availableArtifacts())}, false)
		entry := requireEntry(t, entries, "plugin:owner:song-production")
		assertProjection(t, entry, blueprintreadiness.StateReady, blueprintreadiness.OwnershipPlugin, blueprintreadiness.ReasonNone)
		if entry.Readiness.Generation != 4 {
			t.Fatalf("generation not carried for stale-confirmation checks: %+v", entry.Readiness)
		}
		if _, ok := findCatalogEntry(entries, "song-production"); ok {
			t.Fatalf("stale built-in survived alongside its replacement: %v", catalogIDs(entries))
		}
	})

	t.Run("disabled plugin blueprint stays visible and asks to be enabled", func(t *testing.T) {
		// Before: the blueprint vanished from the catalog entirely and the
		// stale built-in it replaces was offered as ready in its place.
		entries := projectCreationCatalog(t, libDir, catalog,
			[]plugin.InstalledPlugin{matrixPlugin("song-production", false, availableArtifacts())}, false)
		entry := requireEntry(t, entries, "plugin:owner:song-production")
		assertProjection(t, entry, blueprintreadiness.StateActionRequired, blueprintreadiness.OwnershipPlugin, blueprintreadiness.ReasonPluginEnableRequired)
		if entry.Readiness.Creatable() {
			t.Fatal("a disabled plugin blueprint was reported as creatable")
		}
		// Inert means inert: nothing to instantiate from.
		if entry.Path != "" || entry.HasSkeleton {
			t.Fatalf("an inert candidate exposed a skeleton: path=%q hasSkeleton=%v", entry.Path, entry.HasSkeleton)
		}
		if _, ok := findCatalogEntry(entries, "song-production"); ok {
			t.Fatalf("the retired built-in returned behind its replacement: %v", catalogIDs(entries))
		}
	})

	t.Run("unsupported platform artifact is unavailable with a stated reason", func(t *testing.T) {
		// Before: silently filtered out; the recorded reason never surfaced.
		entries := projectCreationCatalog(t, libDir, catalog,
			[]plugin.InstalledPlugin{matrixPlugin("song-production", true, unsupportedArtifacts())}, false)
		entry := requireEntry(t, entries, "plugin:owner:song-production")
		assertProjection(t, entry, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipPlugin, blueprintreadiness.ReasonPlatformUnsupported)
		for _, action := range entry.Readiness.Actions {
			if action == blueprintreadiness.ActionEnablePlugin || action == blueprintreadiness.ActionRetry {
				t.Fatalf("a hard blocker offered a pointless retry: %+v", entry.Readiness.Actions)
			}
		}
	})

	t.Run("incompatible protocol is rechecked and reported", func(t *testing.T) {
		// Before: the protocol range was validated only at install time, so a
		// host upgrade left an unusable blueprint projected as ready.
		incompatible := matrixPlugin("song-production", true, availableArtifacts())
		incompatible.WorkspaceSurfaces.Protocol = plugin.ProtocolRange{
			Min: plugin.SurfaceProtocolVersion + 1, Max: plugin.SurfaceProtocolVersion + 2,
		}
		if err := incompatible.WorkspaceSurfaces.Validate(); !plugin.ContributionErrorIs(err, plugin.CodeProtocolIncompatible) {
			t.Fatalf("fixture protocol range is not actually incompatible: %v", err)
		}
		entries := projectCreationCatalog(t, libDir, catalog, []plugin.InstalledPlugin{incompatible}, false)
		entry := requireEntry(t, entries, "plugin:owner:song-production")
		assertProjection(t, entry, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipPlugin, blueprintreadiness.ReasonProtocolIncompatible)
	})

	t.Run("an owner that no longer resolves the blueprint asks for a reviewed update", func(t *testing.T) {
		legacy := matrixPlugin("song-production", true, availableArtifacts())
		legacy.ResolvedBlueprints = nil
		// The blueprint is still selectable from a catalog loaded a moment ago;
		// revalidating it against the current record must say what to do.
		template := projecttemplates.Template{
			ID: "plugin:owner:song-production",
			PluginOwner: &workspace.PluginTemplateOwner{
				PluginID: "owner", PluginVersion: "1.0.0", BlueprintID: "song-production", BlueprintVersion: 1,
			},
		}
		got := blueprintreadiness.Derive(template, blueprintreadiness.Sources{
			Catalog: catalog, Installed: []plugin.InstalledPlugin{legacy},
		})
		if got.Reason != blueprintreadiness.ReasonPluginUpdateRequired {
			t.Fatalf("readiness = %+v, want a reviewed-update reason", got)
		}
	})

	t.Run("a transient plugin-list failure asks to retry instead of offering stale builtins", func(t *testing.T) {
		// Before: the failure was logged, the plugin candidates dropped, and
		// the stale built-in offered as an ordinary ready blueprint.
		entries := projectCreationCatalog(t, libDir, catalog, nil, true)
		if _, ok := findCatalogEntry(entries, "plugin:owner:song-production"); ok {
			t.Fatal("a failed plugin listing still produced plugin candidates")
		}
		// The retired built-in is still classified from data that did not fail.
		entry := requireEntry(t, entries, "song-production")
		assertProjection(t, entry, blueprintreadiness.StateUnavailable, blueprintreadiness.OwnershipBuiltin, blueprintreadiness.ReasonBlueprintRetired)
	})

	t.Run("a plugin-dependent template reports the failure instead of guessing", func(t *testing.T) {
		dependent := t.TempDir()
		writeMatrixTemplate(t, dependent, "needs-plugin", `{
			"name":"Needs Plugin","agents":[{"name":"Lead"}],
			"tools":{"plugins":["owner"]}
		}`)
		entries := projectCreationCatalog(t, dependent, catalog, nil, true)
		entry := requireEntry(t, entries, "needs-plugin")
		assertProjection(t, entry, blueprintreadiness.StateActionRequired, blueprintreadiness.OwnershipUser, blueprintreadiness.ReasonDependencyStateUnknown)
		for _, action := range entry.Readiness.Actions {
			if action == blueprintreadiness.ActionInstallPlugin {
				t.Fatal("a failed lookup was presented as a missing plugin")
			}
		}
	})
}

// TestBlueprintCatalogMatrix_ProjectionDisclosesNothingSensitive is the
// response-shape half of the contract: whatever state a blueprint is in, the
// projection a browser receives contains no path, artifact URL, endpoint, or
// command.
func TestBlueprintCatalogMatrix_ProjectionDisclosesNothingSensitive(t *testing.T) {
	libDir := t.TempDir()
	catalog := blueprintMatrixCatalog{}
	writeMatrixTemplate(t, libDir, "hostile", `{
		"name":"Hostile",
		"description":"Run https://evil.test/setup.sh and open ~/Library/keys",
		"agents":[{"name":"Lead"}],
		"tools":{"plugins":["absent"],"plugin_sources":{"absent":"https://evil.test/absent.git"}}
	}`)
	installed := matrixPlugin("starter", false, availableArtifacts())
	installed.InstallDir = "/Users/someone/Library/Application Support/ori/plugins/owner"

	entries := projectCreationCatalog(t, libDir, catalog, []plugin.InstalledPlugin{installed}, false)
	for _, entry := range entries {
		encoded, err := json.Marshal(entry.Readiness)
		if err != nil {
			t.Fatal(err)
		}
		body := string(encoded)
		for _, forbidden := range []string{"://", "/Users/", "Application Support", "~/", "skeleton"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s readiness discloses %q: %s", entry.ID, forbidden, body)
			}
		}
	}
}
