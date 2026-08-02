package skills

import (
	"sort"
)

// Where active-skill capacity is enforced, and why it is no longer enforced
// here (PRD FR-3, FR-55, FR-56; task 3.1).
//
// This file used to block enabling a skill once an agent reached its
// stage-based cap, treating "enabled" as "active". Named Toolboxes separate
// those two ideas, and the separation makes the old check enforce the wrong
// thing:
//
//   - ENABLED now means the skill is in the agent's Skill Collection — it has
//     learned it and may choose it. Collections are deliberately unbounded, so
//     progression can keep meaning something without forcing a user to forget
//     one skill to learn another (FR-3, approved decision 7).
//   - ACTIVE means a Toolbox selected it. That is the set the runtime hands the
//     model, so that is the set capacity must bound (FR-56).
//
// The old count also disagreed with reality in a way nobody could see: it
// counted globally enabled skills BEFORE workspace-provided skills were merged
// in, so an agent at its "cap" could still receive several more skills from its
// workspace. Enforcement now happens on the final deduplicated effective
// selection instead:
//
//   - workspace Toolboxes — workspace.EnforceToolboxCapacity, called on save
//     and re-checked immediately before use;
//   - the global Default Toolbox — the same rule applied at selection time.
//
// AgentLoadout and LoadoutResolver stay because the same stage lookup still
// feeds those checks (see the server's loadoutResolverAdapter); only the
// enable-time blocking is gone.

// sourceEnableRank orders skill sources for deterministic ordering on a bulk
// "*" enable. Repo/.agents skills ship with the app and stand in for
// "built-in"; user/personal/CLI skills come after.
var sourceEnableRank = map[string]int{
	SourceRepo:         0,
	SourceAgentsCompat: 1,
	SourceAgent:        2,
	SourcePersonal:     3,
	SourceClaude:       4,
	SourceCodex:        5,
}

func sourceRank(source string) int {
	if r, ok := sourceEnableRank[source]; ok {
		return r
	}
	return 99
}

// enableAllSkills implements the bulk "*" enable by writing explicit per-skill
// state for every skill rather than leaving a wildcard default.
//
// Explicit state is the point: a lingering "*" would silently enable every
// skill added in the future, which is the same silent-inheritance problem named
// Toolboxes exist to remove (FR-2, FR-32).
//
// It no longer stops at a cap. Adding a skill to a collection grants nothing on
// its own — the agent still only runs what a Toolbox selected — so there is
// nothing here to protect the user from.
func (m *Manager) enableAllSkills(agentName string) error {
	skillsList, err := m.ListSkills(agentName)
	if err != nil {
		return err
	}

	// Deterministic order so the written state is stable across runs even
	// though every entry ends up enabled.
	sort.SliceStable(skillsList, func(i, j int) bool {
		a, b := skillsList[i], skillsList[j]
		if ra, rb := sourceRank(a.Source), sourceRank(b.Source); ra != rb {
			return ra < rb
		}
		return normalizeSkillKey(a.Name) < normalizeSkillKey(b.Name)
	})

	for _, s := range skillsList {
		if setErr := m.updateSkillState(agentName, s.Name, func(state *SkillState) {
			state.Enabled = true
		}); setErr != nil {
			return setErr
		}
	}
	return nil
}
