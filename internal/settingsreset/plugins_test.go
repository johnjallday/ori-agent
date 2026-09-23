package settingsreset

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

// demoSeeds is the domain-neutral inventory used across these tests: one fully
// loaded managed plugin and one linked-source plugin whose bytes must survive.
func demoSeeds() []resetfixture.PluginSeed {
	return []resetfixture.PluginSeed{
		{
			Name: "demo-managed", Version: "1.4.0", Enabled: true, Managed: true,
			MCPServers: []string{"tools"}, Skills: []string{"demo-managed-skill"},
			Surfaces: true, Artifacts: true,
		},
		{Name: "demo-linked", Version: "0.2.0", Skills: []string{"demo-linked-skill"}},
	}
}

// pluginFixture wires the standard preview fixture and attaches the plugin owner
// the production builder attaches, then seeds the given inventory.
func pluginFixture(t *testing.T, seeds ...resetfixture.PluginSeed) (*resetfixture.Fixture, *Owners, *Coordinator, *fixtureLifecycle) {
	t.Helper()
	f, owners, c, life := coordinatorFixture(t)
	owners.PluginPaths = f.PluginResetPaths()
	if len(seeds) != 0 {
		f.SeedPlugins(t, seeds...)
	}
	return f, owners, c, life
}

func pluginCategory(t *testing.T, preview Preview) CategoryPreview {
	t.Helper()
	for _, category := range preview.Categories {
		if category.ID == CategoryInstalledPlugins {
			return category
		}
	}
	t.Fatal("preview has no installed plugins category")
	return CategoryPreview{}
}

func factCount(t *testing.T, category CategoryPreview, name string) int64 {
	t.Helper()
	for _, fact := range category.Facts {
		if fact.Name == name {
			if fact.Count == nil {
				t.Fatalf("fact %q is unavailable: %s", name, fact.UnavailableReason)
			}
			return *fact.Count
		}
	}
	t.Fatalf("preview has no fact %q", name)
	return 0
}

func blockerCodes(preview Preview) []string {
	codes := make([]string, 0, len(preview.Blockers))
	for _, blocker := range preview.Blockers {
		codes = append(codes, blocker.Code)
	}
	slices.Sort(codes)
	return codes
}

func TestSelectionAcceptsInstalledPluginsAsAFifthCategory(t *testing.T) {
	selected, err := Selection(IntentSelectedData, []CategoryID{
		CategorySetupSteps, CategoryInstalledPlugins, CategoryAgents, CategoryAppRecords, CategorySettings,
	})
	if err != nil {
		t.Fatalf("five categories rejected: %v", err)
	}
	want := []CategoryID{CategoryAgents, CategoryAppRecords, CategoryInstalledPlugins, CategorySettings, CategorySetupSteps}
	if !slices.Equal(selected, want) {
		t.Fatalf("selection is not deterministically ordered: %v", selected)
	}
	if _, err := Selection(IntentSelectedData, append(want, CategoryInstalledPlugins)); !errors.Is(err, ErrInvalidSelection) {
		t.Fatal("a duplicate category was accepted")
	}
	// Start Fresh stays server-owned: selecting every box is not Start Fresh, and
	// its own categories remain unselectable.
	if _, err := Selection(IntentSelectedData, []CategoryID{CategoryIntegrations}); !errors.Is(err, ErrInvalidSelection) {
		t.Fatal("a Start Fresh category became selectable")
	}
	fresh, err := Selection(IntentStartFresh, nil)
	if err != nil || len(fresh) != 9 || slices.Contains(fresh, CategoryInstalledPlugins) {
		t.Fatalf("Start Fresh composition changed: %v %v", fresh, err)
	}
}

func TestPreviewNamesTheExactPluginInventory(t *testing.T) {
	f, owners, c, _ := pluginFixture(t, demoSeeds()...)
	preview, err := c.planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryInstalledPlugins})
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %v", blockerCodes(preview))
	}
	category := pluginCategory(t, preview)
	if got := factCount(t, category, "installed plugins"); got != 2 {
		t.Fatalf("plugin count = %d", got)
	}
	if got := factCount(t, category, "plugin MCP registrations removed"); got != 1 {
		t.Fatalf("mcp count = %d", got)
	}
	for _, fact := range category.Facts {
		if strings.Contains(fact.Name, "skill") {
			t.Fatalf("plugin reset still counts skills it no longer removes: %+v", fact)
		}
	}
	if got := factCount(t, category, "managed plugin clones removed"); got != 1 {
		t.Fatalf("managed count = %d", got)
	}
	if got := factCount(t, category, "linked plugin sources kept"); got != 1 {
		t.Fatalf("linked count = %d", got)
	}
	names := make([]string, 0, len(category.Items))
	for _, item := range category.Items {
		names = append(names, item.Name)
	}
	if !slices.Equal(names, []string{"demo-linked", "demo-managed"}) {
		t.Fatalf("items do not name the exact inventory: %v", names)
	}
	for _, item := range category.Items {
		if item.Summary == "" || len(item.Details) == 0 {
			t.Fatalf("item %q has no reviewable summary: %+v", item.Name, item)
		}
	}
	// The managed clone is removed; the linked source is named as retained.
	paths := f.PluginResetPaths()
	linkedSource := filepath.Join(f.Paths().Root, "external", "plugin-demo-linked")
	if !locationListed(category.Retained, linkedSource) {
		t.Fatal("the linked plugin source is not shown as retained")
	}
	if !locationListed(category.Retained, paths.MarketplacesPath()) {
		t.Fatal("marketplace registrations are not shown as retained")
	}
	// Plugin skills live in the plugin's folder: ~/.agents/skills is neither a
	// removal target nor named at all.
	for _, location := range append(append([]Location{}, category.Removed...), category.Retained...) {
		if strings.HasPrefix(location.DisplayPath, f.PersonalSkillsRoot()) {
			t.Fatalf("plugin reset names ~/.agents/skills: %+v", location)
		}
	}
	if locationListed(category.Removed, paths.MarketplacesPath()) {
		t.Fatal("selected plugin reset listed marketplace registrations for removal")
	}
	_ = owners
}

func locationListed(locations []Location, path string) bool {
	for _, location := range locations {
		if location.DisplayPath == path {
			return true
		}
	}
	return false
}

func TestPreviewReportsZeroPluginsAsAReviewedNoOp(t *testing.T) {
	_, _, c, _ := pluginFixture(t)
	preview, err := c.planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryInstalledPlugins})
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("an empty inventory blocked: %v", blockerCodes(preview))
	}
	category := pluginCategory(t, preview)
	if got := factCount(t, category, "installed plugins"); got != 0 {
		t.Fatalf("plugin count = %d", got)
	}
	if len(category.Items) != 0 {
		t.Fatalf("empty inventory listed items: %+v", category.Items)
	}
}

func TestPreviewBlocksUnsafePluginOwnership(t *testing.T) {
	f, _, c, _ := pluginFixture(t)
	f.SeedUnreadablePluginRegistry(t)
	preview, err := c.planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryInstalledPlugins})
	mustPreview(t, err)
	if !slices.Contains(blockerCodes(preview), plugin.ResetProblemRegistryUnreadable) {
		t.Fatalf("an unreadable registry did not block: %v", blockerCodes(preview))
	}
	category := pluginCategory(t, preview)
	for _, fact := range category.Facts {
		if fact.Name == "installed plugins" && fact.Count != nil {
			t.Fatal("an unreadable registry reported a false count")
		}
	}
}

func TestPreviewBlocksWhenThePluginOwnerIsUnavailable(t *testing.T) {
	_, owners, c, _ := pluginFixture(t)
	owners.PluginPaths = plugin.ResetPaths{}
	preview, err := c.planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryInstalledPlugins})
	mustPreview(t, err)
	if !slices.Contains(blockerCodes(preview), "plugin_owner_unavailable") {
		t.Fatalf("a missing plugin owner did not block: %v", blockerCodes(preview))
	}
}

// stagePluginReset drives the real preview → confirm path and returns the
// staged operation, leaving the process fenced and awaiting a relaunch.
func stagePluginReset(t *testing.T, c *Coordinator, categories ...CategoryID) Operation {
	t.Helper()
	if len(categories) == 0 {
		categories = []CategoryID{CategoryInstalledPlugins}
	}
	preview, err := c.planner.Create(t.Context(), IntentSelectedData, categories)
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %v", blockerCodes(preview))
	}
	operation, err := c.Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: "plugin-reset-request", Confirmation: "RESET",
	})
	mustPreview(t, err)
	if operation.State != StateAwaitingRestart {
		t.Fatalf("not staged for relaunch: %+v", operation)
	}
	return operation
}

func recoveryOptions(f *resetfixture.Fixture) RecoveryOptions {
	return RecoveryOptions{DataDir: f.Paths().DataDir, SecretStore: f.Secrets()}
}

func TestStagedPluginResetDeletesNothingUntilRelaunch(t *testing.T) {
	f, _, c, _ := pluginFixture(t, demoSeeds()...)
	before := treeBytes(t, f.Paths().Root)
	stagePluginReset(t, c)
	after := treeBytes(t, f.Paths().Root)
	for path, digest := range before {
		if strings.Contains(path, resetstate.Directory) {
			continue
		}
		if after[path] != digest {
			t.Fatalf("confirmation changed %s before relaunch", path)
		}
	}
	f.AssertPreserved(t)
}

func TestRecoveredPluginResetRemovesExactlyTheReviewedFootprint(t *testing.T) {
	f, _, c, _ := pluginFixture(t, demoSeeds()...)
	paths := f.PluginResetPaths()
	linkedSource := filepath.Join(f.Paths().Root, "external", "plugin-demo-linked")
	linkedBefore := treeBytes(t, linkedSource)
	marketplaceBefore := treeBytes(t, paths.MarketplacesPath())

	operation := stagePluginReset(t, c)
	mustPreview(t, RecoverBeforeStores(t.Context(), c.lease, recoveryOptions(f)))

	recovered, err := c.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	if !recovered.VerifiedComplete() {
		t.Fatalf("plugin reset did not verify complete: %+v", recovered)
	}
	if !slices.Contains(recovered.CompletedCategories(), CategoryInstalledPlugins) {
		t.Fatalf("installed plugins was not reported complete: %+v", recovered.Results)
	}
	names := map[string]Outcome{}
	for _, result := range recovered.Results {
		for _, item := range result.Items {
			names[item.Name] = item.Outcome
		}
	}
	if names["demo-managed"] != OutcomeCompleted || names["demo-linked"] != OutcomeCompleted {
		t.Fatalf("per-plugin outcomes are incomplete: %v", names)
	}

	// The managed clone goes, with the skill inside it; the linked source keeps
	// its own skill.
	for _, gone := range []string{
		filepath.Join(paths.CloneDir, "demo-managed-repo"),
		filepath.Join(paths.ArtifactsRoot(), "demo-managed"),
		filepath.Join(paths.StateRoot(), plugin.ResetStateNamespace("demo-managed")),
		filepath.Join(paths.StateRoot(), "demo-managed"),
		paths.PreviewRoot(),
	} {
		if pathExists(t, gone) {
			t.Errorf("reviewed plugin component survived: %s", gone)
		}
	}
	empty, err := plugin.ResetRegistryEmpty(paths)
	mustPreview(t, err)
	if !empty {
		t.Fatal("installed records remain after a verified plugin reset")
	}

	// Preservation: marketplaces, the linked source, unrelated MCP entries, the
	// personal skills root and the skill the user wrote themselves.
	if !treesEqual(marketplaceBefore, treeBytes(t, paths.MarketplacesPath())) {
		t.Error("selected plugin reset removed or changed marketplace registrations")
	}
	if !treesEqual(linkedBefore, treeBytes(t, linkedSource)) {
		t.Error("a linked plugin source was modified")
	}
	servers, err := mcpRegisteredNames(paths.MCPRegistry)
	mustPreview(t, err)
	if !slices.Equal(servers, []string{"unrelated-user-server"}) {
		t.Errorf("unrelated MCP registrations were not preserved exactly: %v", servers)
	}
	if !pathExists(t, f.PersonalSkillsRoot()) {
		t.Error("the shared personal skills root was removed")
	}
	f.AssertPreserved(t)
}

func TestRecoveredPluginResetIsIdempotentAndDoesNotReplayVerifiedWork(t *testing.T) {
	f, _, c, _ := pluginFixture(t, demoSeeds()...)
	operation := stagePluginReset(t, c)
	mustPreview(t, RecoverBeforeStores(t.Context(), c.lease, recoveryOptions(f)))
	first, err := c.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	// A second pre-store pass on a completed receipt must authorize startup
	// without re-applying anything.
	mustPreview(t, RecoverBeforeStores(t.Context(), c.lease, recoveryOptions(f)))
	second, err := c.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	if second.Revision != first.Revision || !second.VerifiedComplete() {
		t.Fatalf("a completed receipt was re-applied: %d -> %d", first.Revision, second.Revision)
	}
	f.AssertPreserved(t)
}

func TestPluginInventoryChangeAfterReviewInvalidatesConfirmation(t *testing.T) {
	t.Run("before confirmation", func(t *testing.T) {
		f, _, c, life := pluginFixture(t, demoSeeds()...)
		preview, err := c.planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryInstalledPlugins})
		mustPreview(t, err)
		f.SeedPlugins(t, append(demoSeeds(), resetfixture.PluginSeed{
			Name: "demo-installed-later", Version: "0.0.1", Managed: true,
		})...)
		if _, err := c.Stage(t.Context(), ExecuteRequest{
			PreviewID: preview.ID, RequestID: "late-install", Confirmation: "RESET",
		}); !errors.Is(err, ErrScopeChanged) {
			t.Fatalf("a newly installed plugin did not invalidate the review: %v", err)
		}
		if life.drains != 0 {
			t.Fatal("a changed scope still drained the runtime")
		}
		f.AssertPreserved(t)
	})

	t.Run("after confirmation, before relaunch", func(t *testing.T) {
		f, _, c, _ := pluginFixture(t, demoSeeds()...)
		operation := stagePluginReset(t, c)
		f.SeedPlugins(t, append(demoSeeds(), resetfixture.PluginSeed{
			Name: "demo-installed-later", Version: "0.0.1", Managed: true,
		})...)
		if err := RecoverBeforeStores(t.Context(), c.lease, recoveryOptions(f)); !errors.Is(err, ErrRecoveryIncomplete) {
			t.Fatalf("a changed inventory was applied anyway: %v", err)
		}
		blocked, err := c.Status(t.Context(), operation.ID)
		mustPreview(t, err)
		if blocked.State != StateBlocked {
			t.Fatalf("expected a blocked operation, got %+v", blocked)
		}
		paths := f.PluginResetPaths()
		for _, kept := range []string{
			filepath.Join(paths.CloneDir, "demo-managed-repo", "skills", "demo-managed-skill"),
			filepath.Join(paths.CloneDir, "demo-installed-later-repo"),
		} {
			if !pathExists(t, kept) {
				t.Errorf("a blocked recovery still removed %s", kept)
			}
		}
		f.AssertPreserved(t)
	})
}

// FR 40: plugin reset touches no skills folder. Anything in ~/.agents/skills —
// an old plugin copy, a stray file where a copy was — neither blocks the reset
// nor is removed by it.
func TestPluginResetNeverTouchesTheHomeSkillsFolder(t *testing.T) {
	f, _, c, _ := pluginFixture(t, demoSeeds()...)
	oldCopy := filepath.Join(f.PersonalSkillsRoot(), "demo-managed-skill")
	mustPreview(t, os.MkdirAll(oldCopy, 0o750))
	mustPreview(t, os.WriteFile(filepath.Join(oldCopy, "SKILL.md"), []byte("old copy\n"), 0o600))
	mustPreview(t, os.WriteFile(filepath.Join(f.PersonalSkillsRoot(), "demo-linked-skill"), []byte("not a skill directory\n"), 0o600))
	before := treeBytes(t, f.PersonalSkillsRoot())

	operation := stagePluginReset(t, c)
	mustPreview(t, RecoverBeforeStores(t.Context(), c.lease, recoveryOptions(f)))
	recovered, err := c.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	if !recovered.VerifiedComplete() {
		t.Fatalf("plugin reset did not verify complete: %+v", recovered)
	}
	if !treesEqual(before, treeBytes(t, f.PersonalSkillsRoot())) {
		t.Fatal("plugin reset changed ~/.agents/skills")
	}
	f.AssertPreserved(t)
}

// A receipt written while plugin skills were copied names a skills root and
// each plugin's skills. It must still validate; that part is ignored.
func TestAnOlderReceiptWithASkillsRootStillValidates(t *testing.T) {
	f, _, c, _ := pluginFixture(t, demoSeeds()...)
	stagePluginReset(t, c)
	receipt, err := c.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	older, err := decodeJournal(receipt)
	mustPreview(t, err)
	older.Plan.Evidence.Plugins.SkillsRoot = f.PersonalSkillsRoot()
	older.Plan.Evidence.Plugins.SkillsRootPresent = true
	for index := range older.Plan.Evidence.Plugins.Items {
		older.Plan.Evidence.Plugins.Items[index].SkillOwnershipSchema = 1
	}
	if err := validateJournal(older); err != nil {
		t.Fatalf("an older receipt with a skills root stopped validating: %v", err)
	}
}

func TestPluginResetCombinesWithOtherSelectedCategories(t *testing.T) {
	f, _, c, _ := pluginFixture(t, demoSeeds()...)
	operation := stagePluginReset(t, c, CategorySetupSteps, CategoryInstalledPlugins)
	mustPreview(t, RecoverBeforeStores(t.Context(), c.lease, recoveryOptions(f)))
	recovered, err := c.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	if !recovered.VerifiedComplete() {
		t.Fatalf("mixed selection did not verify complete: %+v", recovered)
	}
	completed := recovered.CompletedCategories()
	if !slices.Contains(completed, CategoryInstalledPlugins) || !slices.Contains(completed, CategorySetupSteps) {
		t.Fatalf("mixed selection did not complete both categories: %v", completed)
	}
	// Only the plugin category carries members.
	for _, result := range recovered.Results {
		if result.ID != CategoryInstalledPlugins && len(result.Items) != 0 {
			t.Fatalf("category %s reported members: %+v", result.ID, result.Items)
		}
	}
	f.AssertPreserved(t)
}

func TestPartialPluginFailureKeepsCompletedEvidenceAndStaysFenced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based fault injection is meaningless as root")
	}
	f, _, c, _ := pluginFixture(t, demoSeeds()...)
	operation := stagePluginReset(t, c)

	// Injected fault: one plugin's service data folder holds an unreadable
	// subdirectory, so its removal fails while the other plugin's succeeds.
	// Scope revalidation passes and the failure happens where a real I/O error
	// would.
	locked := filepath.Join(f.PluginResetPaths().StateRoot(), "demo-linked", "locked")
	mustPreview(t, os.MkdirAll(locked, 0o750))
	mustPreview(t, os.WriteFile(filepath.Join(locked, "held.txt"), []byte("held\n"), 0o600))
	mustPreview(t, os.Chmod(locked, 0))
	unlock := func() { _ = os.Chmod(locked, 0o750) }
	t.Cleanup(unlock)

	if err := RecoverBeforeStores(t.Context(), c.lease, recoveryOptions(f)); !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatalf("an unremovable component reported success: %v", err)
	}
	failed, err := c.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	if failed.VerifiedComplete() {
		t.Fatal("an unresolved plugin was reported as verified complete")
	}
	if failed.State != StatePartialFailure {
		t.Fatalf("expected a partial failure, got %+v", failed)
	}
	outcomes := map[string]Outcome{}
	for _, result := range failed.Results {
		for _, item := range result.Items {
			outcomes[item.Name] = item.Outcome
		}
	}
	if outcomes["demo-managed"] != OutcomeCompleted {
		t.Fatalf("completed work was not retained: %v", outcomes)
	}
	if outcomes["demo-linked"] == OutcomeCompleted {
		t.Fatalf("a plugin that could not be removed was reported complete: %v", outcomes)
	}
	// The unresolved plugin keeps its installed record, which is the authority a
	// retry needs; the completed one does not.
	records, err := os.ReadFile(f.PluginResetPaths().RegistryPath()) // #nosec G304 -- fixture-owned temporary tree
	mustPreview(t, err)
	if !strings.Contains(string(records), "demo-linked") || strings.Contains(string(records), "demo-managed") {
		t.Fatalf("registry authority was not retained exactly: %s", records)
	}

	// Clear the obstruction; the retry converges and re-verifies rather than
	// replaying the plugin it already removed.
	unlock()
	mustPreview(t, RecoverBeforeStores(t.Context(), c.lease, recoveryOptions(f)))
	recovered, err := c.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	if !recovered.VerifiedComplete() {
		t.Fatalf("retry did not converge: %+v", recovered)
	}
	if pathExists(t, filepath.Join(f.PluginResetPaths().StateRoot(), "demo-linked")) {
		t.Error("the retry did not finish removing the unresolved plugin")
	}
	f.AssertPreserved(t)
}

func TestMissingRegistryRowAloneIsNotCompletion(t *testing.T) {
	f, _, c, _ := pluginFixture(t, demoSeeds()...)
	operation := stagePluginReset(t, c)
	// Drop the whole registry, as a hand-edit or a partially applied removal
	// would. The managed clone still exists.
	paths := f.PluginResetPaths()
	mustPreview(t, os.Remove(paths.RegistryPath()))
	if err := RecoverBeforeStores(t.Context(), c.lease, recoveryOptions(f)); !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatalf("a vanished registry was treated as a verified reset: %v", err)
	}
	blocked, err := c.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	if blocked.VerifiedComplete() {
		t.Fatal("a vanished registry produced a false completion")
	}
	if !pathExists(t, filepath.Join(paths.CloneDir, "demo-managed-repo")) {
		t.Error("a blocked recovery removed the managed clone anyway")
	}
	f.AssertPreserved(t)
}

func TestJournalRefusesPluginEvidenceOutsideItsBoundary(t *testing.T) {
	_, _, c, _ := pluginFixture(t, demoSeeds()...)
	stagePluginReset(t, c)
	receipt, err := c.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	valid, err := decodeJournal(receipt)
	mustPreview(t, err)

	for name, mutate := range map[string]func(*journal){
		"plugin name traverses": func(j *journal) {
			j.Plan.Evidence.Plugins.Items[0].Name = "../escape"
		},
		"mcp entry is not namespaced": func(j *journal) {
			j.Plan.Evidence.Plugins.Items[0].MCPServers = []string{"unrelated-user-server"}
		},
		"linked source claims managed ownership": func(j *journal) {
			for index := range j.Plan.Evidence.Plugins.Items {
				j.Plan.Evidence.Plugins.Items[index].Managed = true
			}
		},
		"registry path points elsewhere": func(j *journal) {
			j.Plan.Evidence.Plugins.RegistryPath = filepath.Join(j.Plan.Installation, "installed.json")
		},
		"evidence is absent": func(j *journal) {
			j.Plan.Evidence.Plugins = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			mutated, err := decodeJournal(receipt)
			mustPreview(t, err)
			mutate(mutated)
			if err := validateJournal(mutated); !errors.Is(err, ErrJournalInvalid) {
				t.Fatalf("unsafe plugin evidence was accepted: %v", err)
			}
		})
	}

	t.Run("evidence on an unrelated selection", func(t *testing.T) {
		unrelated, err := decodeJournal(receipt)
		mustPreview(t, err)
		unrelated.Plan.Preview.Selected = []CategoryID{CategorySetupSteps}
		if err := validateJournal(unrelated); !errors.Is(err, ErrJournalInvalid) {
			t.Fatalf("plugin evidence rode along on an unrelated receipt: %v", err)
		}
	})
	if err := validateJournal(valid); err != nil {
		t.Fatalf("the unmodified receipt stopped validating: %v", err)
	}
}

func pathExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	mustPreview(t, err)
	return true
}

func treesEqual(a, b map[string][sha256.Size]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for path, digest := range a {
		if b[path] != digest {
			return false
		}
	}
	return true
}

// mcpRegisteredNames reads persisted registration names without constructing a
// runtime registry or starting a server.
func mcpRegisteredNames(path string) ([]string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- fixture-owned temporary tree
	if err != nil {
		return nil, err
	}
	var document struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(document.Servers))
	for _, server := range document.Servers {
		names = append(names, server.Name)
	}
	slices.Sort(names)
	return names, nil
}
