package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
)

// Resolve through actual runtime owners after all builder phases have finished.
// No new store is constructed for preview, and no provider/secret values are
// read. Missing owners remain nil rather than falling back to conventional paths.
func (b *ServerBuilder) resetPreviewOwners() settingsreset.Owners {
	owners := settingsreset.Owners{
		Config: b.configManager, Agents: b.st,
		Setup: b.onboardingMgr, Uploads: b.sessionFilesStore,
		Workspaces: b.workspaceFileStore, Allowlist: b.workspaceAllowlist, Vaults: b.vaultStore,
	}
	if b.resetHandler != nil {
		owners.DataDir = b.resetHandler.DataDir()
		owners.PluginPaths = resetPluginPaths(owners.DataDir)
	}
	if b.sessionStore != nil {
		owners.Database = b.sessionStore.DB()
	}
	owners.FreshTargets, owners.CheckFresh = b.resetFreshOwners()
	// Readiness is a capability declaration, not sampled permission to reset.
	// TryFence remains the atomic active-work decision at execution time.
	if b.resetLease != nil && b.resetWork == b.resetLease.WorkGate() {
		owners.CheckLifecycle = func(context.Context) []settingsreset.Blocker {
			snapshot := b.resetWork.Snapshot()
			if !snapshot.Known {
				return []settingsreset.Blocker{{Code: "lifecycle_unavailable", Message: "Runtime work ownership is unavailable.", Recovery: "Fully relaunch Ori using the owned installation."}}
			}
			// Fenced is intentionally not a preview fact: the coordinator
			// revalidates the same plan immediately after its own atomic fence.
			// Competing operations are rejected by the durable journal.
			return nil
		}
	}
	return owners
}

// resetPluginPaths resolves the installed-plugin locations the same way the
// live handler wiring does, and the same way pre-store recovery will resolve
// them independently. An unresolved data dir leaves the owner zero-valued,
// which the planner reports as unavailable rather than guessing a layout.
func resetPluginPaths(dataDir string) plugin.ResetPaths {
	if strings.TrimSpace(dataDir) == "" {
		return plugin.ResetPaths{}
	}
	return plugin.DefaultResetPaths(dataDir)
}

func (b *ServerBuilder) resetFreshOwners() ([]settingsreset.FreshTarget, func(context.Context) settingsreset.FreshInspection) {
	var targets []settingsreset.FreshTarget
	add := func(category settingsreset.CategoryID, kind, path, reason string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		targets = append(targets, settingsreset.FreshTarget{Category: category, Kind: kind, Path: path, Reason: reason})
	}
	if b.onboardingMgr != nil {
		add(settingsreset.CategoryIdentityProgress, "first_run_state", b.onboardingMgr.PersistencePath(), "Replace app identity, setup, progression and app preferences with canonical first-run state.")
	}
	if owner, ok := b.modelCategoryStore.(interface{ PersistencePath() string }); ok {
		add(settingsreset.CategoryAppConfiguration, "model_categories", owner.PersistencePath(), "Remove custom model categories and assignments; built-in defaults may return.")
	}
	if b.locationManager != nil {
		add(settingsreset.CategoryAppConfiguration, "location_zones", b.locationManager.PersistencePath(), "Remove custom location zones and manual location state.")
	}
	if b.connStore != nil {
		add(settingsreset.CategoryIntegrations, "connection_metadata", b.connStore.PersistencePath(), "Remove local Google connection metadata without revoking the provider account or editing a retained vault.")
	}
	if b.consentLog != nil {
		add(settingsreset.CategoryIntegrations, "connection_consent", b.consentLog.PersistencePath(), "Remove local connection consent history for the fresh identity.")
	}
	if b.mcpConfigManager != nil {
		add(settingsreset.CategoryIntegrations, "mcp_registry", b.mcpConfigManager.PersistencePath(), "Remove global MCP registrations; external CLI configuration remains unchanged.")
	}
	if b.mcpCatalogStore != nil {
		sources, cache := b.mcpCatalogStore.PersistencePaths()
		add(settingsreset.CategoryIntegrations, "mcp_search_sources", sources, "Remove custom MCP search sources; the curated built-in source may return.")
		add(settingsreset.CategoryIntegrations, "mcp_search_cache", cache, "Remove fetched MCP registry cache.")
	}
	if b.pluginHandler != nil && b.pluginHandler.Manager() != nil {
		// Start Fresh keeps these broader targets. Its plugin portion runs the
		// same exact offline removal the selective category uses, before these
		// roots are deleted. Plugin ownership that cannot be established safely
		// still blocks, through the planner's own inventory inspection.
		for kind, path := range b.pluginHandler.Manager().FreshPersistencePaths() {
			add(settingsreset.CategoryIntegrations, kind, path, "Remove enumerated managed plugin registration, marketplace, clone, artifact or state after each installed plugin's exact components have been removed; linked external sources remain.")
		}
	}
	if b.configManager != nil {
		add(settingsreset.CategoryTemplates, "project_templates", config.ResolveTemplatesRoot(b.configManager.GetTemplatesRoot()), "Remove the active Ori-owned project-template library; instantiated projects remain.")
		add(settingsreset.CategoryTemplates, "post_reset_project_templates", config.ResolveTemplatesRoot(""), "Remove the project-template library that would become active after settings return to defaults.")
		add(settingsreset.CategoryTemplates, "workflow_templates", resolveWorkflowTemplatesDir(), "Remove custom workflow templates; embedded defaults may return.")
	}
	if b.costTracker != nil {
		add(settingsreset.CategoryActivity, "usage_records", b.costTracker.PersistenceRoot(), "Remove this installation's usage records.")
	}
	if b.activityLogger != nil {
		add(settingsreset.CategoryActivity, "activity_logs", b.activityLogger.PersistencePath(), "Remove this installation's activity logs.")
	}
	if b.cliAgentLogger != nil {
		add(settingsreset.CategoryActivity, "cli_event_logs", b.cliAgentLogger.PersistenceRoot(), "Remove persisted CLI task event logs without touching project edits.")
	}
	cliMCP := llm.NewCLIMCPConfigStore()
	claudeRoot, codexHome := cliMCP.PersistenceRoots()
	add(settingsreset.CategoryRuntimeCache, "cli_mcp_configs", claudeRoot, "Remove generated installation-owned CLI MCP JSON; external CLI authentication and user configuration remain.")

	checkFresh := func(ctx context.Context) settingsreset.FreshInspection {
		var found settingsreset.FreshInspection
		if err := ctx.Err(); err != nil {
			found.Blockers = []settingsreset.Blocker{{Code: "fresh_inspection_cancelled", Message: "Start Fresh owner inspection did not finish.", Recovery: "Review Start Fresh again when the installation is idle."}}
			return found
		}
		found.Kept = append(found.Kept, keptLegacyUsage(os.Getenv("HOME"), config.DefaultDataDir())...)
		found.Kept = append(found.Kept, keptCodexProfiles(codexHome)...)
		if b.resetWakeStore != nil {
			if candidates, err := b.resetWakeStore.Candidates(time.Now()); err != nil {
				found.Blockers = append(found.Blockers, settingsreset.Blocker{Code: "wake_state_unavailable", Category: settingsreset.CategoryRuntimeCache, Message: "Shared wake state cannot be inspected safely.", Recovery: "Resolve the wake coordinator file and stop other Ori/Herdr wake writers before Start Fresh."})
			} else if len(candidates) != 0 {
				found.Blockers = append(found.Blockers, settingsreset.Blocker{Code: "wake_candidates_active", Category: settingsreset.CategoryRuntimeCache, Message: "One or more shared wake requests are still active.", Recovery: "Cancel scheduled Ori work and any Herdr wake-enabled run, then review Start Fresh again. Other sources will not be cancelled automatically."})
			}
		}
		return found
	}
	return targets, checkFresh
}

// keptLegacyUsage discloses the usage file Ori kept in $HOME/.ori-agent before
// v0.0.111 moved usage into the data dir. No current build reads or writes it,
// and any installation sharing the HOME may have written to it, so Start Fresh
// leaves it and names it rather than refusing to run. Where the data dir is
// that same folder the file is the live one and is removed as usage_records.
func keptLegacyUsage(home, dataDir string) []settingsreset.FreshKept {
	legacyUsage := filepath.Join(home, ".ori-agent", "usage_data", "usage_records.json")
	if legacyUsage == filepath.Join(dataDir, "usage_data", "usage_records.json") {
		return nil
	}
	// #nosec G703 -- this is a read-only metadata check of the one explicit
	// legacy HOME location; reset never opens its contents or removes it.
	_, err := os.Lstat(legacyUsage)
	reason := "Usage records from Ori versions before v0.0.111, which kept them in your home folder. This version never reads them, and other Ori installations may have written to the same file, so Start Fresh leaves its usage_data folder as it is. Delete that folder yourself if you want those records gone."
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		reason = "Could not be checked for usage records from Ori versions before v0.0.111. Start Fresh does not touch this location either way."
	}
	return []settingsreset.FreshKept{{Category: settingsreset.CategoryActivity, Location: settingsreset.Location{DisplayPath: legacyUsage, Reason: reason}}}
}

// keptCodexProfiles discloses the ori-ws-*.config.toml profiles Ori writes into
// CODEX_HOME for workspace MCP servers. They are named by workspace, so another
// installation sharing CODEX_HOME (a wt demo server, say) may own them, and
// Codex runs rewrite a profile before passing it, so a leftover never takes
// effect. Start Fresh leaves them and names them rather than refusing to run.
func keptCodexProfiles(codexHome string) []settingsreset.FreshKept {
	if strings.TrimSpace(codexHome) == "" {
		return nil
	}
	entries, err := os.ReadDir(codexHome)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return []settingsreset.FreshKept{{Category: settingsreset.CategoryRuntimeCache, Location: settingsreset.Location{
			DisplayPath: codexHome,
			Reason:      "Could not be checked for the Codex profiles Ori writes for workspace MCP servers (ori-ws-*.config.toml). Start Fresh does not touch this folder either way.",
		}}}
	}
	profiles := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "ori-ws-") && strings.HasSuffix(entry.Name(), ".config.toml") {
			profiles++
		}
	}
	if profiles == 0 {
		return nil
	}
	count := "1 Codex profile"
	if profiles != 1 {
		count = fmt.Sprintf("%d Codex profiles", profiles)
	}
	return []settingsreset.FreshKept{{Category: settingsreset.CategoryRuntimeCache, Location: settingsreset.Location{
		DisplayPath: filepath.Join(codexHome, "ori-ws-*.config.toml"),
		Reason:      count + " Ori wrote for workspace MCP servers. Start Fresh leaves them: another Ori installation sharing this Codex folder may use them, and Ori rewrites a workspace's profile before any Codex run uses it, so a leftover one never takes effect. Delete them yourself if you want them gone; keep auth.json, config.toml and every other file there.",
	}}}
}

func (b *ServerBuilder) initializeResetCoordinator() {
	if b.resetLease == nil || b.resetPlanner == nil || b.resetHandler == nil || b.resetWork != b.resetLease.WorkGate() {
		return
	}
	// The lifecycle can become destructive only through the coordinator, after
	// a bounded reviewed plan, atomic non-cancelling fence, and verified drain.
	b.resetHandler.SetCoordinator(settingsreset.NewCoordinator(
		b.resetLease,
		b.resetPlanner,
		newServerResetLifecycle(b.server),
	))
}
