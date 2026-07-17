package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const skillStateFileName = "skills_state.json"

// SkillState stores per-agent state for a skill (not persisted in SKILL.md).
type SkillState struct {
	Enabled   bool      `json:"enabled"`
	Trusted   bool      `json:"trusted"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// SkillRegistry stores skill state keyed by canonical skill name.
type SkillRegistry struct {
	Skills map[string]SkillState `json:"skills"`
}

func skillStatePath(agentStorePath, agentName string) (string, error) {
	if agentName == "" {
		return "", fmt.Errorf("agent name is required")
	}
	agentsDir, err := resolveAgentsDir(agentStorePath)
	if err != nil {
		return "", err
	}
	return filepath.Join(agentsDir, agentName, skillStateFileName), nil
}

func loadSkillRegistry(path string) (SkillRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SkillRegistry{Skills: map[string]SkillState{}}, nil
		}
		return SkillRegistry{}, err
	}

	var registry SkillRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return SkillRegistry{}, err
	}
	if registry.Skills == nil {
		registry.Skills = map[string]SkillState{}
	}

	return registry, nil
}

func saveSkillRegistry(path string, registry SkillRegistry) error {
	if registry.Skills == nil {
		registry.Skills = map[string]SkillState{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	payload, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o644)
}

func normalizeSkillKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func (m *Manager) getSkillRegistry(agentName string) (SkillRegistry, string, error) {
	path, err := skillStatePath(m.agentStorePath, agentName)
	if err != nil {
		return SkillRegistry{}, "", err
	}
	registry, err := loadSkillRegistry(path)
	if err != nil {
		return SkillRegistry{}, "", err
	}
	return registry, path, nil
}

func (m *Manager) updateSkillState(agentName, skillName string, updateFn func(*SkillState)) error {
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	key := normalizeSkillKey(skillName)
	if key == "" {
		return fmt.Errorf("skill name is required")
	}

	registry, path, err := m.getSkillRegistry(agentName)
	if err != nil {
		return err
	}

	state, exists := registry.Skills[key]
	if !exists {
		// Opt-in model: skills are disabled by default unless an agent-level
		// ("*") default says otherwise. Seeding disabled keeps side-effect
		// updates (e.g. trusting a skill) from implicitly enabling it.
		if defaultState, ok := registry.Skills["*"]; ok {
			state.Enabled = defaultState.Enabled
			state.Trusted = defaultState.Trusted
		} else {
			state.Enabled = false
		}
	}
	updateFn(&state)
	state.UpdatedAt = time.Now().UTC()
	registry.Skills[key] = state

	return saveSkillRegistry(path, registry)
}

func (m *Manager) SetSkillTrusted(agentName, skillName string, trusted bool) error {
	return m.updateSkillState(agentName, skillName, func(state *SkillState) {
		state.Trusted = trusted
	})
}

func (m *Manager) SetSkillEnabled(agentName, skillName string, enabled bool) error {
	key := normalizeSkillKey(skillName)
	if enabled {
		// Bulk "*" enable fills up to the cap deterministically for non-expert
		// agents rather than blanket-enabling everything (PRD FR14).
		if key == "*" {
			return m.enableAllWithinCap(agentName)
		}
		if err := m.enforceSlotCapForEnable(agentName, key); err != nil {
			return err
		}
	}
	return m.updateSkillState(agentName, skillName, func(state *SkillState) {
		state.Enabled = enabled
	})
}

func (m *Manager) ClearSkillState(agentName, skillName string) error {
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	key := normalizeSkillKey(skillName)
	if key == "" {
		return fmt.Errorf("skill name is required")
	}

	registry, path, err := m.getSkillRegistry(agentName)
	if err != nil {
		return err
	}
	delete(registry.Skills, key)
	return saveSkillRegistry(path, registry)
}
