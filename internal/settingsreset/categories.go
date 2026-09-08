package settingsreset

import (
	"errors"
	"slices"
)

var ErrInvalidSelection = errors.New("choose a supported intent and at least one distinct reset category")

// Definition owns the public description and expected postconditions for a
// category. The planner's resolved target kinds (not UI checkbox names) are
// the inputs to staged application.
type Definition struct {
	ID          CategoryID
	Label       string
	Description string
	Checks      []string
}

func definition(id CategoryID) (Definition, bool) {
	switch id {
	case CategorySettings:
		return Definition{id, "Settings & API keys", "Global preferences and Ori-saved provider/search API keys. Keep roots required by unselected workspaces and vaults. Environment and other applications' authentication are not removed.", []string{"preferences_default", "provider_search_keys_absent", "retained_roots_usable"}}, true
	case CategoryAgents:
		return Definition{id, "Agents", "Ori agent profiles, owned skills/state and compatibility indexes. Keep workspace snapshots, external skills and conversation history; do not automatically restore removed profiles.", []string{"old_profiles_absent", "agent_rehydration_disabled"}}, true
	case CategoryAppRecords:
		return Definition{id, "Conversation & app records", "The shared database includes messages, profiles/HQ, notes, plans, jobs, reviews, assistant state and vault registrations, not just chat. Detach workspaces and vaults; keep their backing files and encryption material. Remove only owned uploads, never linked external documents.", []string{"app_records_default", "registrations_detached", "automatic_reattachment_disabled", "retained_files_usable"}}, true
	case CategorySetupSteps:
		return Definition{id, "Setup steps", "Reset only setup completion and steps through the onboarding owner. Keep names/profile, agents, workspaces, history, credentials and quest progress.", []string{"setup_incomplete", "identity_and_progress_unchanged"}}, true
	case CategoryIdentityProgress:
		return Definition{id, "Identity, setup & progress", "Return Ori's app-state identity, profile, setup, assistant evolution, Getting Started progress and app preferences to canonical first-run values.", []string{"first_run_state_default"}}, true
	case CategoryAppConfiguration:
		return Definition{id, "Supplemental app configuration", "Remove custom model categories and location zones. Built-in defaults may be recreated after relaunch.", []string{"supplemental_configuration_default"}}, true
	case CategoryIntegrations:
		return Definition{id, "Local integrations", "Remove local connection metadata and consent, MCP registrations/search cache, and managed plugin registrations, clones, artifacts and state. Keep third-party accounts, external authentication, linked sources and credentials inside retained vault packages.", []string{"local_integrations_absent", "external_integrations_preserved"}}, true
	case CategoryTemplates:
		return Definition{id, "Ori-owned templates", "Remove the active Ori project-template library and custom workflow templates only when their roots are inside this installation. Keep instantiated workspace/project files and external template roots.", []string{"owned_templates_default", "project_files_preserved"}}, true
	case CategoryActivity:
		return Definition{id, "Usage & activity", "Remove this installation's usage records, activity logs and CLI event logs. Shared or external history requires explicit ownership and otherwise blocks Start Fresh.", []string{"owned_activity_absent"}}, true
	case CategoryRuntimeCache:
		return Definition{id, "Generated runtime state", "Remove generated workspace CLI-MCP files and other enumerated transient runtime state without touching external CLI profiles or authentication.", []string{"generated_runtime_absent", "automatic_imports_disabled"}}, true
	default:
		return Definition{}, false
	}
}

// Selection normalizes category order, but never interprets selecting all as a
// different intent. Duplicate/unknown categories fail instead of being ignored.
func Selection(intent Intent, categories []CategoryID) ([]CategoryID, error) {
	if intent == IntentStartFresh {
		if len(categories) != 0 {
			return nil, ErrInvalidSelection
		}
		return []CategoryID{
			CategorySettings, CategoryAgents, CategoryAppRecords,
			CategoryIdentityProgress, CategoryAppConfiguration, CategoryIntegrations,
			CategoryTemplates, CategoryActivity, CategoryRuntimeCache,
		}, nil
	}
	if intent != IntentSelectedData || len(categories) == 0 || len(categories) > 4 {
		return nil, ErrInvalidSelection
	}
	selected := slices.Clone(categories)
	slices.Sort(selected)
	for i, id := range selected {
		if _, ok := definition(id); !ok || !slices.Contains([]CategoryID{CategorySettings, CategoryAgents, CategoryAppRecords, CategorySetupSteps}, id) || (i > 0 && selected[i-1] == id) {
			return nil, ErrInvalidSelection
		}
	}
	return selected, nil
}
