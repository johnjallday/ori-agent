package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillInput represents editable skill fields for agent-scoped skills.
type SkillInput struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Prompt      string  `json:"prompt"`
	OpenAIYAML  *string `json:"openai_yaml,omitempty"`
}

func (m *Manager) CreateSkill(agentName string, input SkillInput) (Skill, error) {
	if err := validateSkillName(input.Name); err != nil {
		return Skill{}, err
	}
	if err := validateSkillDescription(input.Description); err != nil {
		return Skill{}, err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Skill{}, fmt.Errorf("prompt is required")
	}

	if existing, found, err := m.GetSkill(agentName, input.Name); err != nil {
		return Skill{}, err
	} else if found && existing != nil {
		return Skill{}, ErrSkillExists
	}

	skillDir, err := m.agentSkillDir(agentName, input.Name)
	if err != nil {
		return Skill{}, err
	}
	if _, err := os.Stat(skillDir); err == nil {
		return Skill{}, ErrSkillExists
	}

	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return Skill{}, err
	}

	if err := writeSkillMarkdown(filepath.Join(skillDir, "SKILL.md"), input.Name, input.Description, input.Prompt); err != nil {
		return Skill{}, err
	}

	if input.OpenAIYAML != nil {
		agentsDir := filepath.Join(skillDir, "agents")
		if err := os.MkdirAll(agentsDir, 0o755); err != nil {
			return Skill{}, err
		}
		if err := os.WriteFile(filepath.Join(agentsDir, "openai.yaml"), []byte(*input.OpenAIYAML), 0o644); err != nil {
			return Skill{}, err
		}
	}

	skill, err := m.loadSkillEntry(filepath.Join(skillDir, "SKILL.md"), input.Name, SourceAgent, skillDir, true)
	if err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func (m *Manager) UpdateSkill(agentName, skillName string, input SkillInput) (Skill, error) {
	if input.Name != "" && !strings.EqualFold(input.Name, skillName) {
		return Skill{}, ErrSkillRenameNotSupported
	}
	if err := validateSkillName(skillName); err != nil {
		return Skill{}, err
	}
	if err := validateSkillDescription(input.Description); err != nil {
		return Skill{}, err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Skill{}, fmt.Errorf("prompt is required")
	}

	skillDir, err := m.agentSkillDir(agentName, skillName)
	if err != nil {
		return Skill{}, err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	skill, err := m.updateSkillAtPath(skillPath, skillDir, skillName, SourceAgent, input)
	if err != nil {
		return Skill{}, err
	}
	// Editing content must not change enablement; report the skill's real
	// per-agent state (opt-in default for skills without explicit state).
	resolved := []Skill{skill}
	if stateErr := m.applySkillState(agentName, resolved); stateErr == nil {
		skill = resolved[0]
	}
	return skill, nil
}

func (m *Manager) UpdateSkillAtPath(source, skillPath, skillName string, input SkillInput) (Skill, error) {
	if source != SourcePersonal && source != SourceAgentsCompat {
		return Skill{}, ErrSkillReadOnly
	}
	if !m.isEditableSkillPath(source, skillPath) {
		return Skill{}, fmt.Errorf("invalid skill path")
	}

	skillPath = filepath.Clean(skillPath)
	skillDir := filepath.Dir(skillPath)
	return m.updateSkillAtPath(skillPath, skillDir, skillName, source, input)
}

func (m *Manager) updateSkillAtPath(skillPath, skillDir, skillName, source string, input SkillInput) (Skill, error) {
	if input.Name != "" && !strings.EqualFold(input.Name, skillName) {
		return Skill{}, ErrSkillRenameNotSupported
	}
	if err := validateSkillName(skillName); err != nil {
		return Skill{}, err
	}
	if err := validateSkillDescription(input.Description); err != nil {
		return Skill{}, err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Skill{}, fmt.Errorf("prompt is required")
	}

	if _, err := os.Stat(skillPath); err != nil {
		if os.IsNotExist(err) {
			return Skill{}, ErrSkillNotFound
		}
		return Skill{}, err
	}

	if err := writeSkillMarkdown(skillPath, skillName, input.Description, input.Prompt); err != nil {
		return Skill{}, err
	}

	if input.OpenAIYAML != nil {
		agentsDir := filepath.Join(skillDir, "agents")
		if err := os.MkdirAll(agentsDir, 0o755); err != nil {
			return Skill{}, err
		}
		if err := os.WriteFile(filepath.Join(agentsDir, "openai.yaml"), []byte(*input.OpenAIYAML), 0o644); err != nil {
			return Skill{}, err
		}
	}

	skill, err := m.loadSkillEntry(skillPath, skillName, source, skillDir, true)
	if err != nil {
		return Skill{}, err
	}
	// Editing a skill's content must not change its enabled state; callers with
	// agent context resolve the real state via applySkillState.
	return skill, nil
}

func (m *Manager) isEditableSkillPath(source, skillPath string) bool {
	skillPath = filepath.Clean(strings.TrimSpace(skillPath))
	if skillPath == "." || skillPath == "" {
		return false
	}
	if !strings.EqualFold(filepath.Base(skillPath), "SKILL.md") {
		return false
	}

	var root string
	var roots []string
	switch source {
	case SourcePersonal:
		root = strings.TrimSpace(m.personalSkillsDir)
		if root != "" {
			roots = append(roots, root)
		}
	case SourceAgentsCompat:
		roots = append(roots, resolveCompatSkillsDirs(m.agentStorePath)...)
	default:
		return false
	}

	if len(roots) == 0 {
		return false
	}

	pathAbs, err := filepath.Abs(skillPath)
	if err != nil {
		return false
	}

	for _, root := range roots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}

		rel, err := filepath.Rel(rootAbs, pathAbs)
		if err != nil {
			continue
		}
		if rel == "." {
			return true
		}
		if !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return true
		}
	}

	return false
}

func (m *Manager) DeleteSkill(agentName, skillName string) error {
	skillDir, err := m.agentSkillDir(agentName, skillName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(skillDir); err != nil {
		if os.IsNotExist(err) {
			return ErrSkillNotFound
		}
		return err
	}

	if err := os.RemoveAll(skillDir); err != nil {
		return err
	}

	// Clean up registry entry if present.
	_ = m.ClearSkillState(agentName, skillName)

	return nil
}

func (m *Manager) agentSkillDir(agentName, skillName string) (string, error) {
	if agentName == "" {
		return "", fmt.Errorf("agent name is required")
	}
	agentsDir, err := resolveAgentsDir(m.agentStorePath)
	if err != nil {
		return "", err
	}
	return filepath.Join(agentsDir, agentName, "skills", skillName), nil
}

// AgentSkillDir returns the expected absolute directory for an agent-scoped skill.
func (m *Manager) AgentSkillDir(agentName, skillName string) (string, error) {
	if err := validateSkillName(skillName); err != nil {
		return "", err
	}
	return m.agentSkillDir(agentName, skillName)
}

func writeSkillMarkdown(path, name, description, prompt string) error {
	frontmatter := map[string]string{
		"name":        name,
		"description": description,
	}
	fmBytes, err := yaml.Marshal(frontmatter)
	if err != nil {
		return err
	}
	content := fmt.Sprintf("---\n%s---\n\n%s\n", string(fmBytes), strings.TrimSpace(prompt))
	return os.WriteFile(path, []byte(content), 0o644)
}
