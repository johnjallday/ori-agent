package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
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

func (b *ServerBuilder) resetFreshOwners() ([]settingsreset.FreshTarget, func(context.Context) []settingsreset.Blocker) {
	var targets []settingsreset.FreshTarget
	var checkPluginSkills func() []settingsreset.Blocker
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
		manager := b.pluginHandler.Manager()
		for kind, path := range manager.FreshPersistencePaths() {
			add(settingsreset.CategoryIntegrations, kind, path, "Remove enumerated managed plugin registration, clone, artifact or state; linked external sources and personal skills remain.")
		}
		checkPluginSkills = func() []settingsreset.Blocker {
			installed, err := manager.List()
			if err != nil {
				return []settingsreset.Blocker{{Code: "plugin_inventory_unavailable", Category: settingsreset.CategoryIntegrations, Message: "Managed plugin registrations cannot be inspected safely.", Recovery: "Restore the plugin registry and review Start Fresh again."}}
			}
			for _, item := range installed {
				if len(item.Skills) != 0 {
					return []settingsreset.Blocker{{Code: "external_plugin_skills_present", Category: settingsreset.CategoryIntegrations, Message: "An installed plugin copied skills into the shared personal skills directory.", Recovery: "Uninstall plugins with skill components first so Ori can remove only their recorded copies without touching personal skills, then review Start Fresh again."}}
				}
			}
			return nil
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

	checkFresh := func(ctx context.Context) []settingsreset.Blocker {
		var blockers []settingsreset.Blocker
		if err := ctx.Err(); err != nil {
			return []settingsreset.Blocker{{Code: "fresh_inspection_cancelled", Message: "Start Fresh owner inspection did not finish.", Recovery: "Review Start Fresh again when the installation is idle."}}
		}
		if checkPluginSkills != nil {
			blockers = append(blockers, checkPluginSkills()...)
		}
		legacyUsage := filepath.Join(os.Getenv("HOME"), ".ori-agent", "usage_data", "usage_records.json")
		if active := filepath.Join(config.DefaultDataDir(), "usage_data", "usage_records.json"); legacyUsage != active {
			if _, err := os.Lstat(legacyUsage); err == nil {
				blockers = append(blockers, settingsreset.Blocker{Code: "legacy_usage_ownership_ambiguous", Category: settingsreset.CategoryActivity, Message: "A legacy shared-HOME usage file cannot be attributed exclusively to this installation.", Recovery: "Archive or remove that legacy file manually after confirming no other Ori installation uses it, then review Start Fresh again."})
			} else if !os.IsNotExist(err) {
				blockers = append(blockers, settingsreset.Blocker{Code: "legacy_usage_unavailable", Category: settingsreset.CategoryActivity, Message: "The legacy shared-HOME usage location cannot be inspected safely.", Recovery: "Restore access to that location and review Start Fresh again."})
			}
		}
		if entries, err := os.ReadDir(codexHome); err == nil {
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "ori-ws-") && strings.HasSuffix(entry.Name(), ".config.toml") {
					blockers = append(blockers, settingsreset.Blocker{Code: "external_cli_profile_present", Category: settingsreset.CategoryRuntimeCache, Message: "Generated Ori workspace profiles remain in external CODEX_HOME.", Recovery: "Remove only reviewed ori-ws-*.config.toml profiles manually; preserve auth.json, config.toml and all other external CLI files."})
					break
				}
			}
		} else if !os.IsNotExist(err) {
			blockers = append(blockers, settingsreset.Blocker{Code: "external_cli_home_unavailable", Category: settingsreset.CategoryRuntimeCache, Message: "External CODEX_HOME cannot be inspected safely.", Recovery: "Restore access to CODEX_HOME and review Start Fresh again; external authentication will not be removed."})
		}
		if b.resetWakeStore != nil {
			if candidates, err := b.resetWakeStore.Candidates(time.Now()); err != nil {
				blockers = append(blockers, settingsreset.Blocker{Code: "wake_state_unavailable", Category: settingsreset.CategoryRuntimeCache, Message: "Shared wake state cannot be inspected safely.", Recovery: "Resolve the wake coordinator file and stop other Ori/Herdr wake writers before Start Fresh."})
			} else if len(candidates) != 0 {
				blockers = append(blockers, settingsreset.Blocker{Code: "wake_candidates_active", Category: settingsreset.CategoryRuntimeCache, Message: "One or more shared wake requests are still active.", Recovery: "Cancel scheduled Ori work and any Herdr wake-enabled run, then review Start Fresh again. Other sources will not be cancelled automatically."})
			}
		}
		return blockers
	}
	return targets, checkFresh
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
