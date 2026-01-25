package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/externalagents"
	"gopkg.in/yaml.v3"
)

const (
	SourceLocal  = "local"
	SourceClaude = "claude"
	SourceCodex  = "codex"
)

type ManagerConfig struct {
	AgentStorePath string
	ExternalAgents *externalagents.Cache
	ConfigManager  *config.Manager
}

type Manager struct {
	agentStorePath string
	externalAgents *externalagents.Cache
	configManager  *config.Manager
}

func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{
		agentStorePath: cfg.AgentStorePath,
		externalAgents: cfg.ExternalAgents,
		configManager:  cfg.ConfigManager,
	}
}

func (m *Manager) GetSkill(agentName, skillName string) (*Skill, bool, error) {
	skills, err := m.ListSkills(agentName)
	if err != nil {
		return nil, false, err
	}

	for _, skill := range skills {
		if strings.EqualFold(skill.Name, skillName) {
			return &skill, true, nil
		}
	}

	return nil, false, nil
}

func (m *Manager) ListSkills(agentName string) ([]Skill, error) {
	localSkills, err := m.loadLocalSkills(agentName)
	if err != nil {
		return nil, err
	}

	skillMap := make(map[string]Skill, len(localSkills))
	for _, skill := range localSkills {
		key := strings.ToLower(skill.Name)
		skillMap[key] = skill
	}

	if m.externalAgents != nil && m.configManager != nil {
		if m.configManager.GetExternalAgentsClaudeEnabled() {
			for _, ext := range m.externalAgents.GetClaudeAgents() {
				key := strings.ToLower(ext.Name)
				if _, exists := skillMap[key]; exists {
					continue
				}
				skillMap[key] = Skill{
					Name:        ext.Name,
					Description: ext.Description,
					Prompt:      ext.SystemPrompt,
					Source:      SourceClaude,
					Model:       ext.Model,
					Color:       ext.Color,
				}
			}
		}

		if m.configManager.GetExternalAgentsCodexEnabled() {
			for _, ext := range m.externalAgents.GetCodexAgents() {
				key := strings.ToLower(ext.Name)
				if _, exists := skillMap[key]; exists {
					continue
				}
				skillMap[key] = Skill{
					Name:        ext.Name,
					Description: ext.Description,
					Prompt:      ext.SystemPrompt,
					Source:      SourceCodex,
				}
			}
		}
	}

	skills := make([]Skill, 0, len(skillMap))
	for _, skill := range skillMap {
		skills = append(skills, skill)
	}

	return skills, nil
}

func (m *Manager) loadLocalSkills(agentName string) ([]Skill, error) {
	if agentName == "" {
		return []Skill{}, nil
	}

	agentsDir, err := resolveAgentsDir(m.agentStorePath)
	if err != nil {
		return nil, err
	}

	skillsDir := filepath.Join(agentsDir, agentName, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Skill{}, nil
		}
		return nil, err
	}

	var skills []Skill
	for _, entry := range entries {
		if entry.IsDir() {
			skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
			skill, err := parseSkillFile(skillPath, entry.Name())
			if err != nil {
				continue
			}
			skills = append(skills, skill)
			continue
		}

		if strings.HasSuffix(entry.Name(), ".md") {
			skillPath := filepath.Join(skillsDir, entry.Name())
			baseName := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			skill, err := parseSkillFile(skillPath, baseName)
			if err != nil {
				continue
			}
			skills = append(skills, skill)
		}
	}

	return skills, nil
}

type skillFrontmatter struct {
	Name               string
	Description        string
	AllowedTools       []string
	DisallowedTools    []string
	RequiredMCPServers []string
}

func parseSkillFile(path string, defaultName string) (Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	frontmatter, body, err := parseFrontmatter(string(content))
	if err != nil {
		return Skill{}, err
	}

	fm := parseSkillFrontmatter(frontmatter)
	name := fm.Name
	if name == "" {
		name = defaultName
	}

	return Skill{
		Name:               name,
		Description:        fm.Description,
		Prompt:             strings.TrimSpace(body),
		Source:             SourceLocal,
		Path:               path,
		AllowedTools:       fm.AllowedTools,
		DisallowedTools:    fm.DisallowedTools,
		RequiredMCPServers: fm.RequiredMCPServers,
	}, nil
}

func parseSkillFrontmatter(frontmatter string) skillFrontmatter {
	if strings.TrimSpace(frontmatter) == "" {
		return skillFrontmatter{}
	}

	raw := make(map[string]interface{})
	if err := yaml.Unmarshal([]byte(frontmatter), &raw); err != nil {
		return skillFrontmatter{}
	}

	fm := skillFrontmatter{
		Name:               getStringField(raw, "name"),
		Description:        getStringField(raw, "description"),
		AllowedTools:       getStringSliceField(raw, "allowed_tools", "allowed-tools"),
		DisallowedTools:    getStringSliceField(raw, "disallowed_tools", "disallowed-tools"),
		RequiredMCPServers: getStringSliceField(raw, "required_mcp_servers", "required-mcp-servers"),
	}

	return fm
}

func parseFrontmatter(content string) (frontmatter, body string, err error) {
	const delimiter = "---"

	content = strings.TrimLeft(content, "\n\r\t ")
	if !strings.HasPrefix(content, delimiter) {
		return "", content, nil
	}

	rest := content[len(delimiter):]
	endIdx := strings.Index(rest, "\n"+delimiter)
	if endIdx == -1 {
		return "", content, nil
	}

	frontmatter = strings.TrimSpace(rest[:endIdx])
	body = strings.TrimLeft(rest[endIdx+len("\n"+delimiter):], "\n\r")
	return frontmatter, body, nil
}

func getStringField(raw map[string]interface{}, key string) string {
	if val, ok := raw[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getStringSliceField(raw map[string]interface{}, keys ...string) []string {
	for _, key := range keys {
		if val, ok := raw[key]; ok {
			return interfaceSliceToStrings(val)
		}
	}
	return nil
}

func interfaceSliceToStrings(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return nil
	}
}

func resolveAgentsDir(agentStorePath string) (string, error) {
	if agentStorePath == "" {
		return "", fmt.Errorf("agent store path is required")
	}

	baseDir := filepath.Dir(agentStorePath)
	if strings.Contains(agentStorePath, "/agents/") || strings.Contains(agentStorePath, "\\agents\\") {
		agentsDirIndex := strings.LastIndex(agentStorePath, "/agents/")
		if agentsDirIndex == -1 {
			agentsDirIndex = strings.LastIndex(agentStorePath, "\\agents\\")
		}
		if agentsDirIndex != -1 {
			return agentStorePath[:agentsDirIndex+7], nil
		}
	}

	return filepath.Join(baseDir, "agents"), nil
}
