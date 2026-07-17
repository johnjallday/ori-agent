package skills

import (
	"fmt"
	"sort"
)

// sourceEnableRank orders skill sources for deterministic cap-filling on a
// bulk "*" enable (PRD FR14: "starter/built-in skills first, then
// alphabetical"). Lower rank fills first. Repo/.agents skills ship with the
// app and stand in for "built-in"; user/personal/CLI skills come after.
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

// resolveLoadout returns the agent's cap budget, or ok=false when no resolver
// is wired or the agent is unresolvable (fail open — never block a toggle).
func (m *Manager) resolveLoadout(agentName string) (AgentLoadout, bool) {
	if m.loadoutResolver == nil {
		return AgentLoadout{}, false
	}
	return m.loadoutResolver.ResolveAgentLoadout(agentName)
}

// enforceSlotCapForEnable rejects enabling a not-yet-enabled skill when the
// agent is already at (or over) its stage-based slot cap. Idempotent
// re-enables of an already-active skill are always allowed, and over-cap
// grandfathered agents keep everything — the cap only blocks *new* enables
// (PRD FR12). Expert-mode and unresolvable agents bypass entirely.
func (m *Manager) enforceSlotCapForEnable(agentName, skillKey string) error {
	loadout, ok := m.resolveLoadout(agentName)
	if !ok || loadout.ExpertMode {
		return nil
	}

	skillsList, err := m.ListSkills(agentName)
	if err != nil {
		return err
	}

	enabledCount := 0
	for _, s := range skillsList {
		if !s.Enabled {
			continue
		}
		enabledCount++
		if normalizeSkillKey(s.Name) == skillKey {
			// Already enabled → re-enabling is a no-op, never blocked.
			return nil
		}
	}

	if enabledCount >= loadout.SlotCap {
		return fmt.Errorf("%w: the %s stage allows %d active skills (%d in use). Disable a skill or enable expert mode to add more",
			ErrSkillSlotCapReached, loadout.Stage, loadout.SlotCap, enabledCount)
	}
	return nil
}

// enableAllWithinCap implements the bulk "*" enable for a non-expert agent by
// filling active-skill slots deterministically up to the cap rather than
// blanket-enabling every skill. Order: currently-enabled first (never demote a
// grandfathered skill out of the kept set), then built-in/source rank, then
// alphabetical. Expert / unresolvable agents fall back to the legacy wildcard
// enable (unrestricted). PRD FR14.
func (m *Manager) enableAllWithinCap(agentName string) error {
	loadout, ok := m.resolveLoadout(agentName)
	if !ok || loadout.ExpertMode {
		return m.updateSkillState(agentName, "*", func(state *SkillState) {
			state.Enabled = true
		})
	}

	skillsList, err := m.ListSkills(agentName)
	if err != nil {
		return err
	}

	sort.SliceStable(skillsList, func(i, j int) bool {
		a, b := skillsList[i], skillsList[j]
		// Currently-enabled skills sort first so a bulk enable never demotes
		// an agent's existing loadout below the cap.
		if a.Enabled != b.Enabled {
			return a.Enabled
		}
		if ra, rb := sourceRank(a.Source), sourceRank(b.Source); ra != rb {
			return ra < rb
		}
		return normalizeSkillKey(a.Name) < normalizeSkillKey(b.Name)
	})

	// Write explicit per-skill state for every skill: the first `cap` in the
	// deterministic order enabled, the rest disabled. Explicit state avoids
	// leaving a wildcard "*" default that would silently enable future skills.
	for i, s := range skillsList {
		enabled := i < loadout.SlotCap
		if setErr := m.updateSkillState(agentName, s.Name, func(state *SkillState) {
			state.Enabled = enabled
		}); setErr != nil {
			return setErr
		}
	}
	return nil
}
