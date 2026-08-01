package server

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Startup migration of legacy implicit capability state into explicit
// Toolboxes (PRD FR-28–FR-35).
//
// It runs at boot rather than lazily on first read for one reason: until an
// agent instance has an explicit assignment it keeps resolving through the
// legacy implicit merge, where a newly bound workspace capability silently
// widens what it can do. That window should close on upgrade, not on the next
// time somebody happens to open the Workshop.
//
// Both halves are idempotent and non-fatal. A workspace that cannot be migrated
// keeps its pre-migration behavior and logs a recoverable diagnostic; it is not
// left half-written, because ApplyToolboxMigrationPlan builds every Toolbox
// before committing any of them (FR-34, FR-35).

// migrateAgentDefaultToolboxes gives every global agent an explicit Default
// Toolbox seeded from the skills it currently has enabled, so direct-chat
// behavior is preserved exactly (FR-28).
func migrateAgentDefaultToolboxes(agentStore store.Store, skillsManager *skills.Manager) {
	if agentStore == nil {
		return
	}

	migrated := 0
	for _, agentName := range agentStore.ListAgents() {
		// UpdateAgent persists unconditionally, so check under a plain read
		// first: without this, every boot would rewrite the whole agent store
		// to change nothing.
		if existing, ok := agentStore.GetAgent(agentName); ok && existing != nil && existing.DefaultToolbox != nil {
			continue
		}
		enabled := enabledSkillNames(skillsManager, agentName)
		err := agentStore.UpdateAgent(agentName, func(ag *agent.Agent) error {
			if !agent.MigrateDefaultToolbox(ag, enabled) {
				return nil
			}
			migrated++
			return nil
		})
		if err != nil {
			logger.Warn("Default toolbox migration failed for one agent", logger.Fields{
				"agent": agentName,
				"error": err.Error(),
			})
		}
	}

	if migrated > 0 {
		logger.Info("Migrated agents to explicit default toolboxes", logger.Fields{"agents": migrated})
	}
}

// enabledSkillNames lists the skills an agent currently has enabled — the set
// direct chat injected before Default Toolboxes existed.
//
// A nil skills manager yields an empty selection rather than skipping the
// agent: an agent with no resolvable skills genuinely has an empty Default
// Toolbox, and leaving it unmigrated would keep it on the legacy path forever.
func enabledSkillNames(skillsManager *skills.Manager, agentName string) []string {
	if skillsManager == nil {
		return nil
	}
	available, err := skillsManager.ListSkills(agentName)
	if err != nil {
		logger.Warn("Could not read enabled skills for default toolbox migration", logger.Fields{
			"agent": agentName,
			"error": err.Error(),
		})
		return nil
	}

	names := make([]string, 0, len(available))
	for _, skill := range available {
		if !skill.Enabled {
			continue
		}
		if name := strings.TrimSpace(skill.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// migrateWorkspaceToolboxes gives every agent instance in every workspace an
// explicit `Workspace Default` Toolbox representing exactly what it could use
// before migration (FR-29–FR-33).
func migrateWorkspaceToolboxes(
	workspaceStore workspace.Store,
	skillSource workspace.ToolboxMigrationSkillSource,
	capacitySource workspace.ToolboxMigrationCapacitySource,
) {
	if workspaceStore == nil {
		return
	}

	workspaceIDs, err := workspaceStore.List()
	if err != nil {
		logger.Warn("Toolbox migration skipped: workspace store cannot list workspaces", logger.Fields{
			"error": err.Error(),
		})
		return
	}

	migrated := 0
	for _, workspaceID := range workspaceIDs {
		state, err := workspace.MigrateWorkspaceToolboxes(workspaceStore, workspaceID, skillSource, capacitySource, "startup-migration")
		if err != nil {
			// Non-fatal by design: the workspace keeps resolving through the
			// legacy path, which is the pre-migration behavior FR-35 requires
			// a failure to preserve.
			logger.Warn("Toolbox migration failed for one workspace", logger.Fields{
				"workspace_id": workspaceID,
				"error":        err.Error(),
			})
			continue
		}
		if state == nil {
			continue
		}
		if state.ToolboxCount > 0 {
			migrated++
		}
		for _, diagnostic := range state.Diagnostics {
			if diagnostic.Severity == workspace.ToolboxMigrationWarning {
				logger.Info("Toolbox migration note", logger.Fields{
					"workspace_id": workspaceID,
					"agent":        diagnostic.AgentName,
					"message":      diagnostic.Message,
				})
			}
		}
	}

	if migrated > 0 {
		logger.Info("Migrated workspaces to explicit toolboxes", logger.Fields{"workspaces": migrated})
	}
}
