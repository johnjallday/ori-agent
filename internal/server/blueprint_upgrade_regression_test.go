// TestBlueprintUpgradeRegression_RetiredBuiltinWithLegacyPluginRecord is the
// upgrade scenario the feature exists to repair, run against the real
// plugin.Manager and the real catalog projection — not mocks.
//
// The starting profile looks like an install that predates this feature: a
// shipped built-in whose ID the app no longer ships (retired), and a plugin
// installed before install roots were absolutized, whose record still has no
// contributed blueprints because blueprint contribution did not exist yet.
// Both conditions are real: `blueprint_retired` came out of Group 1, and the
// relative-install-root repair came out of the same group's install-root
// canonicalization work.
//
// The scenario proves, against one continuous story:
//   - nothing on disk is deleted classifying the retired built-in;
//   - the legacy relative install root resolves and Update succeeds, even
//     though the process is not running from wherever it was originally
//     launched;
//   - as soon as Update discovers the contribution, it supersedes the retired
//     built-in in the catalog — even while still disabled, so the user is
//     shown the real, current blueprint and its one remaining step rather
//     than a stale shipped copy sitting in front of it;
//   - but that candidate stays inert (no skeleton, not creatable) until an
//     explicit Enable — Update alone never makes anything creatable;
//   - once enabled, the plugin-owned blueprint is ready and creatable.
package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

func TestBlueprintUpgradeRegression_RetiredBuiltinWithLegacyPluginRecord(t *testing.T) {
	const pluginName = "workspace-surface-demo"
	const blueprintID = "demo-workspace"

	exampleRoot, err := filepath.Abs(filepath.Join("..", "..", "examples", "plugins", "workspace-surface-demo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exampleRoot); err != nil {
		t.Skipf("repository example plugin not present at %s: %v", exampleRoot, err)
	}

	libDir := t.TempDir()
	writeMatrixTemplate(t, libDir, blueprintID, `{
		"name":"Old Demo Workspace","builtin":true,"builtin_version":1,
		"agents":[{"name":"Legacy Lead"}]
	}`)
	beforeManifest, err := os.ReadFile(filepath.Join(libDir, blueprintID, "template.json"))
	if err != nil {
		t.Fatal(err)
	}
	beforeSeed, err := os.ReadFile(filepath.Join(libDir, blueprintID, "seed.txt"))
	if err != nil {
		t.Fatal(err)
	}

	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	legacy := plugin.InstalledPlugin{
		Name:    pluginName,
		Version: "0.0.1",
		Source:  exampleRoot,
		Format:  plugin.FormatClaude,
		// A relative install root, as a pre-canonicalization record would
		// have stored it — resolvable only by falling back to Source, per
		// canonicalInstallRoot.
		InstallDir: "checkouts/workspace-surface-demo",
		Generation: 1,
		Enabled:    false,
		// No ResolvedBlueprints, no WorkspaceSurfaces, no ResolvedArtifacts:
		// this plugin predates blueprint contribution entirely.
	}
	s := catalogEndpointServer(t, libDir, pluginsDir, []plugin.InstalledPlugin{legacy})
	manager := s.Handlers.Plugin.Manager()

	// ---- before: only the retired built-in is offered, and only as an
	// explanation, never as something the wizard can create from ----
	_, before := getCatalog(t, s)
	beforeEntry := findEntryOrFail(t, before, blueprintID)
	if beforeEntry.Readiness.State != string(blueprintreadiness.StateUnavailable) ||
		beforeEntry.Readiness.Reason != string(blueprintreadiness.ReasonBlueprintRetired) {
		t.Fatalf("before update: readiness = %+v, want retired/unavailable", beforeEntry.Readiness)
	}
	if _, ok := findEntry(before, "plugin:"+pluginName+":"+blueprintID); ok {
		t.Fatal("a plugin with no resolved blueprints already offered one")
	}

	// ---- update: the legacy relative root must resolve via Source, and the
	// update must discover the newly declared blueprint contribution ----
	preview, changed, err := manager.UpdatePreview(pluginName)
	if err != nil {
		t.Fatalf("update preview against the legacy record: %v", err)
	}
	if !changed {
		t.Fatal("gaining a blueprint contribution was not reported as a change")
	}
	if preview.String() == "" {
		t.Fatal("the update disclosed nothing")
	}
	updated, err := manager.Update(pluginName, func(plugin.TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("update the legacy record: %v", err)
	}
	if !filepath.IsAbs(updated.InstallDir) {
		t.Fatalf("update did not heal the legacy relative root: %q", updated.InstallDir)
	}
	if len(updated.ResolvedBlueprints) != 1 || updated.ResolvedBlueprints[0].ID != blueprintID {
		t.Fatalf("update did not record the contributed blueprint: %+v", updated.ResolvedBlueprints)
	}

	// ---- enablement is explicit: Update alone must not have made the
	// blueprint creatable, only visible and explained ----
	if updated.Enabled {
		t.Fatal("update silently enabled a plugin that was disabled before it")
	}
	_, afterUpdate := getCatalog(t, s)
	// The inert candidate supersedes the retired built-in as soon as it is
	// discovered, disabled or not — that is the whole point of carrying an
	// inert candidate at all (Group 1, FR: "the correct plugin-owned manifest
	// can supersede a stale matching built-in before enablement"). The user
	// should see the real, current blueprint and the one thing left to do,
	// not a stale shipped copy sitting in front of it.
	if _, ok := findEntry(afterUpdate, blueprintID); ok {
		t.Fatalf("the retired built-in was still offered once its replacement was discovered: %v", catalogEntryIDs(afterUpdate))
	}
	candidate := findEntryOrFail(t, afterUpdate, "plugin:"+pluginName+":"+blueprintID)
	if candidate.Readiness.State != string(blueprintreadiness.StateActionRequired) ||
		candidate.Readiness.Reason != string(blueprintreadiness.ReasonPluginEnableRequired) {
		t.Fatalf("the updated-but-disabled candidate readiness = %+v, want plugin_enable_required", candidate.Readiness)
	}
	if candidate.HasSkeleton {
		// Inert means inert: not creatable, whatever the readiness copy says.
		t.Fatal("an inert candidate advertised a skeleton before it was enabled")
	}

	// ---- explicit enable ----
	if err := manager.SetEnabled(pluginName, true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// ---- after: the trusted replacement is ready and has REPLACED the
	// retired built-in, which is no longer offered at all ----
	_, after := getCatalog(t, s)
	if _, ok := findEntry(after, blueprintID); ok {
		t.Fatalf("the retired built-in survived alongside its enabled replacement: %v", catalogEntryIDs(after))
	}
	final := findEntryOrFail(t, after, "plugin:"+pluginName+":"+blueprintID)
	if final.Readiness.State != string(blueprintreadiness.StateReady) {
		t.Fatalf("the trusted replacement is not ready: %+v", final.Readiness)
	}
	if !final.HasSkeleton {
		// Path itself is server-side only (json:"-" on Template, deliberately
		// never sent to the browser); HasSkeleton is the client-visible signal
		// that there is something to instantiate from.
		t.Fatalf("a ready plugin blueprint advertises no skeleton to instantiate from: %+v", final)
	}

	// ---- nothing on disk was ever deleted ----
	afterManifest, err := os.ReadFile(filepath.Join(libDir, blueprintID, "template.json"))
	if err != nil {
		t.Fatalf("the retired built-in's manifest was removed: %v", err)
	}
	if string(afterManifest) != string(beforeManifest) {
		t.Fatal("the retired built-in's manifest was rewritten")
	}
	afterSeed, err := os.ReadFile(filepath.Join(libDir, blueprintID, "seed.txt"))
	if err != nil {
		t.Fatalf("the retired built-in's other files were removed: %v", err)
	}
	if string(afterSeed) != string(beforeSeed) {
		t.Fatal("the retired built-in's other files were rewritten")
	}
}

// --- small helpers over the catalog endpoint's JSON shape ---

type upgradeCatalogEntry struct {
	ID          string                                `json:"id"`
	HasSkeleton bool                                  `json:"has_skeleton"`
	Readiness   upgradeCatalogEntryReadinessRawFields `json:"readiness"`
}

// upgradeCatalogEntryReadinessRawFields decodes only the fields this test
// checks, tolerating the rest of the readiness projection's shape.
type upgradeCatalogEntryReadinessRawFields struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

func entriesFromCatalog(raw string) []upgradeCatalogEntry {
	var body struct {
		Templates []upgradeCatalogEntry `json:"templates"`
	}
	_ = json.Unmarshal([]byte(raw), &body)
	return body.Templates
}

func findEntry(raw string, id string) (upgradeCatalogEntry, bool) {
	for _, entry := range entriesFromCatalog(raw) {
		if entry.ID == id {
			return entry, true
		}
	}
	return upgradeCatalogEntry{}, false
}

func findEntryOrFail(t *testing.T, raw string, id string) upgradeCatalogEntry {
	t.Helper()
	entry, ok := findEntry(raw, id)
	if !ok {
		t.Fatalf("%q missing from catalog: %v", id, catalogEntryIDs(raw))
	}
	return entry
}

func catalogEntryIDs(raw string) []string {
	entries := entriesFromCatalog(raw)
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}
