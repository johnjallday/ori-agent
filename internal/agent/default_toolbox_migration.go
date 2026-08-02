package agent

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/types"
)

// MigrateDefaultToolbox gives an agent that predates Default Toolboxes an
// explicit one, filled from the skills it currently has enabled (PRD FR-28).
//
// Seeding from the CURRENTLY ENABLED set is the whole point: before this
// feature, "enabled globally" was what direct chat injected, so copying that
// set into the Default Toolbox makes migration a no-op for the user. The agent
// answers direct chats with exactly the same skills the day after migration as
// the day before — the difference is that the selection is now written down and
// stops changing on its own (FR-2, FR-3).
//
// It is idempotent: an agent that already has a Default Toolbox is left alone,
// including one deliberately emptied by the user. Re-running migration must
// never re-add skills someone removed (FR-34).
//
// Returns true when the agent was modified and needs persisting.
func MigrateDefaultToolbox(ag *Agent, enabledSkillNames []string) bool {
	if ag == nil || ag.DefaultToolbox != nil {
		return false
	}

	skills := make([]types.DefaultToolboxSkillRef, 0, len(enabledSkillNames))
	for _, name := range enabledSkillNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		skills = append(skills, types.DefaultToolboxSkillRef{
			CapabilityID: strings.ToLower(trimmed),
			DisplayName:  trimmed,
		})
	}

	normalized, err := types.NormalizeDefaultToolboxSkills(skills)
	if err != nil {
		// A legacy skill name that cannot be a Default Toolbox identity is not
		// a reason to leave the agent unmigrated forever; migrate it to an
		// empty explicit selection rather than repeatedly failing. The agent
		// keeps working and the user can add skills back in the Workshop.
		normalized = nil
	}

	toolbox := types.NewAgentDefaultToolbox()
	toolbox.Skills = normalized
	ag.DefaultToolbox = toolbox
	return true
}
