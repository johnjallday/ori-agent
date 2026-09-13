package settingsreset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

// freshFixture prepares a Start Fresh scope with the full owner set the planner
// needs, so these tests exercise the real nine-category composition.
func freshFixture(t *testing.T, seeds ...resetfixture.PluginSeed) (*resetfixture.Fixture, *Owners, *Coordinator, *fixtureLifecycle) {
	t.Helper()
	f, owners, c, life := coordinatorFixture(t)
	root := f.Paths().DataDir
	mustPreview(t, owners.Config.SetTemplatesRoot(filepath.Join(root, "templates")))
	mustPreview(t, owners.Config.Save())
	t.Setenv("ORI_TEMPLATES_DIR", filepath.Join(root, "templates"))
	t.Setenv("WORKFLOW_TEMPLATES_DIR", filepath.Join(root, "workflow_templates"))
	owners.FreshTargets = fixtureFreshTargets(root)
	if len(seeds) != 0 {
		f.SeedPlugins(t, seeds...)
	}
	return f, owners, c, life
}

// TestStartFreshNoLongerBlocksOnReviewedPluginSkills is the retirement of
// `external_plugin_skills_present`. Copied plugin skills used to force a manual
// uninstall first; now they are removed exactly by the shared owner.
func TestStartFreshNoLongerBlocksOnReviewedPluginSkills(t *testing.T) {
	f, _, c, _ := freshFixture(t,
		resetfixture.PluginSeed{
			Name: "fresh-managed", Version: "2.0.0", Enabled: true, Managed: true,
			MCPServers: []string{"tools"}, Skills: []string{"fresh-managed-skill"}, Surfaces: true,
		},
	)
	preview, err := c.planner.Create(t.Context(), IntentStartFresh, nil)
	mustPreview(t, err)
	for _, blocker := range preview.Blockers {
		if blocker.Code == "external_plugin_skills_present" {
			t.Fatal("the manual-uninstall blocker is still emitted for reviewed plugin skills")
		}
	}
	if len(preview.Blockers) != 0 {
		t.Fatalf("valid plugin skill records blocked Start Fresh: %+v", preview.Blockers)
	}
	// The plugin-copied skill is named as removed; the shared root is not.
	var integrations CategoryPreview
	for _, category := range preview.Categories {
		if category.ID == CategoryIntegrations {
			integrations = category
		}
	}
	if !locationListed(integrations.Removed, filepath.Join(f.PersonalSkillsRoot(), "fresh-managed-skill")) {
		t.Fatal("Start Fresh did not name the exact plugin-copied skill it removes")
	}
	if locationListed(integrations.Removed, f.PersonalSkillsRoot()) {
		t.Fatal("Start Fresh listed the shared personal skills root as a removal target")
	}
	if len(integrations.Items) != 1 || integrations.Items[0].Name != "fresh-managed" {
		t.Fatalf("Start Fresh did not name the installed plugin: %+v", integrations.Items)
	}
}

func TestStartFreshStillBlocksOnUninspectablePluginOwnership(t *testing.T) {
	t.Run("unreadable registry", func(t *testing.T) {
		f, _, c, _ := freshFixture(t)
		f.SeedUnreadablePluginRegistry(t)
		preview, err := c.planner.Create(t.Context(), IntentStartFresh, nil)
		mustPreview(t, err)
		if !slices.Contains(blockerCodes(preview), plugin.ResetProblemRegistryUnreadable) {
			t.Fatalf("an unreadable registry did not block Start Fresh: %v", blockerCodes(preview))
		}
	})
	t.Run("unavailable owner", func(t *testing.T) {
		_, owners, c, _ := freshFixture(t)
		owners.PluginPaths = plugin.ResetPaths{}
		preview, err := c.planner.Create(t.Context(), IntentStartFresh, nil)
		mustPreview(t, err)
		if !slices.Contains(blockerCodes(preview), "plugin_owner_unavailable") {
			t.Fatalf("an unavailable plugin owner did not block Start Fresh: %v", blockerCodes(preview))
		}
	})
}

// TestStartFreshCompositionIsUnchanged pins the public contract that receipt
// compatibility depends on: the same nine categories, in the same order, with
// the same named checks.
func TestStartFreshCompositionIsUnchanged(t *testing.T) {
	fresh, err := Selection(IntentStartFresh, nil)
	mustPreview(t, err)
	want := []CategoryID{
		CategorySettings, CategoryAgents, CategoryAppRecords,
		CategoryIdentityProgress, CategoryAppConfiguration, CategoryIntegrations,
		CategoryTemplates, CategoryActivity, CategoryRuntimeCache,
	}
	if !slices.Equal(fresh, want) {
		t.Fatalf("Start Fresh composition changed: %v", fresh)
	}
	definitionChecks := map[CategoryID][]string{
		CategoryIntegrations:     {"local_integrations_absent", "external_integrations_preserved"},
		CategorySettings:         {"preferences_default", "provider_search_keys_absent", "retained_roots_usable"},
		CategoryAgents:           {"old_profiles_absent", "agent_rehydration_disabled"},
		CategoryAppRecords:       {"app_records_default", "registrations_detached", "automatic_reattachment_disabled", "retained_files_usable"},
		CategoryIdentityProgress: {"first_run_state_default"},
		CategoryAppConfiguration: {"supplemental_configuration_default"},
		CategoryTemplates:        {"owned_templates_default", "project_files_preserved"},
		CategoryActivity:         {"owned_activity_absent"},
		CategoryRuntimeCache:     {"generated_runtime_absent", "automatic_imports_disabled"},
	}
	for id, checks := range definitionChecks {
		def, ok := definition(id)
		if !ok || !slices.Equal(def.Checks, checks) {
			t.Fatalf("category %s checks changed: %v", id, def.Checks)
		}
	}
	// Start Fresh's own target kinds are unchanged too: the selective category
	// resolves its own distinct kinds and never collides with them.
	if slices.ContainsFunc(targetKinds(CategoryIntegrations), func(kind string) bool {
		return slices.Contains(targetKinds(CategoryInstalledPlugins), kind)
	}) {
		t.Fatal("the selective plugin category shares a target kind with Start Fresh integrations")
	}
}

// TestLegacyStartFreshReceiptRecoversUnderOriginalSemantics reconciles receipts
// staged before this feature existed. Such a receipt carries no plugin evidence
// and could only be staged when no plugin had copied a skill, so it must keep
// recovering through the original raw managed-root removal — never be stranded,
// and never be reinterpreted under the new exact rules.
func TestLegacyStartFreshReceiptRecoversUnderOriginalSemantics(t *testing.T) {
	f, owners, c, life := freshFixture(t, resetfixture.PluginSeed{
		Name: "legacy-managed", Version: "1.0.0", Managed: true, MCPServers: []string{"tools"},
	})
	root := f.Paths().DataDir
	preview, err := c.planner.Create(t.Context(), IntentStartFresh, nil)
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %+v", preview.Blockers)
	}
	life.drain = func(context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}
	operation, err := c.Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: "legacy-fresh", Confirmation: "RESET",
	})
	mustPreview(t, err)
	if operation.State != StateAwaitingRestart {
		t.Fatalf("staged Start Fresh = %+v", operation)
	}

	// Rewrite the staged receipt into exactly the shape the previous release
	// wrote: no plugin evidence, and no per-plugin members in the review.
	receipt, err := c.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	legacy, err := decodeJournal(receipt)
	mustPreview(t, err)
	legacy.Plan.Evidence.Plugins = nil
	for index := range legacy.Plan.Preview.Categories {
		legacy.Plan.Preview.Categories[index].Items = nil
	}
	encoded, err := encodeJournal(legacy)
	if err != nil {
		t.Fatalf("a legacy-shaped receipt no longer validates: %v", err)
	}
	mustPreview(t, c.lease.Replace(resetstate.OperationRecord, encoded))

	mustPreview(t, RecoverBeforeStores(t.Context(), c.lease, RecoveryOptions{DataDir: root, SecretStore: f.Secrets()}))
	recovered, err := c.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	if !recovered.VerifiedComplete() || len(recovered.CompletedCategories()) != 9 {
		t.Fatalf("a legacy receipt was stranded: %+v", recovered)
	}
	// Original semantics: the managed roots go wholesale, and no per-plugin
	// outcome is invented for a receipt that never reviewed one.
	for _, result := range recovered.Results {
		if len(result.Items) != 0 {
			t.Fatalf("legacy recovery invented per-plugin members: %+v", result.Items)
		}
	}
	paths := f.PluginResetPaths()
	for _, gone := range []string{
		paths.RegistryPath(), paths.MarketplacesPath(), paths.CloneDir,
		paths.StateRoot(), paths.ArtifactsRoot(), paths.PreviewRoot(),
	} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Fatalf("legacy Start Fresh left the managed root %s: %v", gone, err)
		}
	}
	f.AssertPreserved(t)
}

// TestStartFreshReceiptRefusesPluginEvidenceItDidNotReview complements the
// above: evidence may ride along with Start Fresh, but never with a selection
// that owns no plugins.
func TestStartFreshReceiptRefusesPluginEvidenceItDidNotReview(t *testing.T) {
	f, _, c, _ := pluginFixture(t, demoSeeds()...)
	stagePluginReset(t, c)
	receipt, err := c.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	borrowed, err := decodeJournal(receipt)
	mustPreview(t, err)
	if borrowed.Plan.Evidence.Plugins == nil {
		t.Fatal("the selective receipt carried no plugin evidence to borrow")
	}
	if !pluginEvidencePermitted([]CategoryID{CategoryIntegrations}) {
		t.Fatal("Start Fresh may carry plugin evidence")
	}
	if pluginEvidenceRequired([]CategoryID{CategoryIntegrations}) {
		t.Fatal("Start Fresh must not require plugin evidence, or legacy receipts strand")
	}
	if pluginEvidencePermitted([]CategoryID{CategorySettings, CategorySetupSteps}) {
		t.Fatal("an unrelated selection may not carry plugin evidence")
	}
	f.AssertPreserved(t)
}
