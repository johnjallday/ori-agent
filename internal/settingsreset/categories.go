package settingsreset

import (
	"errors"
	"slices"
)

var ErrInvalidSelection = errors.New("choose a supported intent and at least one distinct reset category")

// Definition owns the public description and expected postconditions for a
// category. The planner's resolved target kinds (not UI checkbox names) are
// the inputs to staged application. Start Fresh is deliberately not enabled
// until all of its additional owners have been implemented.
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
	default:
		return Definition{}, false
	}
}

// Selection normalizes category order, but never interprets selecting all as a
// different intent. Duplicate/unknown categories fail instead of being ignored.
func Selection(intent Intent, categories []CategoryID) ([]CategoryID, error) {
	if intent != IntentSelectedData || len(categories) == 0 || len(categories) > 4 {
		return nil, ErrInvalidSelection
	}
	selected := slices.Clone(categories)
	slices.Sort(selected)
	for i, id := range selected {
		if _, ok := definition(id); !ok || (i > 0 && selected[i-1] == id) {
			return nil, ErrInvalidSelection
		}
	}
	return selected, nil
}
