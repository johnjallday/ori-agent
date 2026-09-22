package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) GetSkillMarkdown(agentName, skillName string) (string, bool, error) {
	skill, found, err := m.GetSkill(agentName, skillName)
	if err != nil || !found || skill == nil {
		return "", found, err
	}
	if strings.TrimSpace(skill.Path) != "" {
		data, readErr := os.ReadFile(skill.Path) // #nosec G304 -- skill.Path is resolved by the manager from its configured skill roots
		if readErr != nil {
			return "", false, readErr
		}
		return strings.TrimSpace(string(data)), true, nil
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", skill.Name, skill.Description, skill.Prompt), true, nil
}

func (m *Manager) BundledSkillMatches(agentName, skillName, text string) (bool, error) {
	candidate, err := ValidateSkillMarkdown(text, skillName)
	if err != nil {
		return false, err
	}
	current, found, err := m.GetSkill(agentName, skillName)
	if err != nil || !found || current == nil {
		return false, err
	}
	return current.Name == candidate.Name && current.Description == candidate.Description && current.Prompt == candidate.Prompt, nil
}

// InstallBundledSkill installs exact reviewed SKILL.md text into the entry
// agent's own skill folder. It never replaces an existing agent-scoped copy
// unless the caller has recorded an explicit collision choice.
func (m *Manager) InstallBundledSkill(agentName, skillName, text string, replace bool) error {
	if _, err := ValidateSkillMarkdown(text, skillName); err != nil {
		return err
	}
	dir, err := m.agentSkillDir(agentName, skillName)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(dir); statErr == nil && !replace {
		return ErrSkillExists
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create bundled skill directory: %w", err)
	}
	path := filepath.Join(dir, "SKILL.md")
	temp, err := os.CreateTemp(dir, ".SKILL.md-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.WriteString(text); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	if err := m.SetSkillTrusted(agentName, skillName, false); err != nil {
		return err
	}
	return m.SetSkillEnabled(agentName, skillName, false)
}
