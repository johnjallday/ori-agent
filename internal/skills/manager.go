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
	SourceAgent        = "agent"
	SourceRepo         = "repo"
	SourceAgentsCompat = ".agents"
	SourcePersonal     = "personal"
	SourceClaude       = "claude"
	SourceCodex        = "codex"
)

type ManagerConfig struct {
	AgentStorePath    string
	PersonalSkillsDir string
	ExternalAgents    *externalagents.Cache
	ConfigManager     *config.Manager
}

// AgentLoadout describes an agent's active-skill slot budget for cap
// enforcement (PRD section C). Stage is carried only for error messages.
type AgentLoadout struct {
	SlotCap    int
	ExpertMode bool
	Stage      string
}

// LoadoutResolver resolves an agent's slot cap and expert-mode flag at check
// time (read through the agent store, never cached — PRD technical notes).
// Implemented by an adapter over the agent store in the server package.
type LoadoutResolver interface {
	// ResolveAgentLoadout returns the agent's loadout budget. ok=false means
	// the agent is not resolvable, in which case the caller applies no cap
	// (fail open — a missing agent should never block a skill toggle).
	ResolveAgentLoadout(agentName string) (loadout AgentLoadout, ok bool)
}

type Manager struct {
	agentStorePath    string
	personalSkillsDir string
	externalAgents    *externalagents.Cache
	configManager     *config.Manager
	loadoutResolver   LoadoutResolver
}

func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{
		agentStorePath:    cfg.AgentStorePath,
		personalSkillsDir: cfg.PersonalSkillsDir,
		externalAgents:    cfg.ExternalAgents,
		configManager:     cfg.ConfigManager,
	}
}

// SetLoadoutResolver wires stage-based slot-cap enforcement for the per-agent
// skill-enable path. When nil (unset), SetSkillEnabled behaves as it did
// before caps existed — no enforcement — preserving legacy callers.
func (m *Manager) SetLoadoutResolver(resolver LoadoutResolver) {
	m.loadoutResolver = resolver
}

func (m *Manager) GetSkill(agentName, skillName string) (*Skill, bool, error) {
	skills, err := m.listSkills(agentName, false)
	if err != nil {
		return nil, false, err
	}

	for _, skill := range skills {
		if strings.EqualFold(skill.Name, skillName) {
			if skill.Prompt == "" && skill.Path != "" {
				full, err := m.loadSkillWithPrompt(skill)
				if err != nil {
					return nil, false, err
				}
				return full, true, nil
			}
			return &skill, true, nil
		}
	}

	return nil, false, nil
}

func (m *Manager) ListSkills(agentName string) ([]Skill, error) {
	return m.listSkills(agentName, false)
}

// ListEnabledSkillsWithPrompts returns only enabled skills that have non-empty
// prompt text loaded. This is used to inject skill knowledge into agent system
// prompts so agents benefit from their enabled skills during normal chat.
func (m *Manager) ListEnabledSkillsWithPrompts(agentName string) ([]Skill, error) {
	allSkills, err := m.listSkills(agentName, true)
	if err != nil {
		return nil, err
	}
	var enabled []Skill
	for _, s := range allSkills {
		if s.Enabled && strings.TrimSpace(s.Prompt) != "" {
			enabled = append(enabled, s)
		}
	}
	return enabled, nil
}

// ResolveSkillByName searches all non-agent-specific sources for a skill by name
// and returns it with its full prompt content loaded. This is used by workspace
// skill bindings to resolve skills by name at runtime.
func (m *Manager) ResolveSkillByName(skillName string) (*Skill, bool, error) {
	target := strings.ToLower(strings.TrimSpace(skillName))
	if target == "" {
		return nil, false, nil
	}

	sources := []func(bool) ([]Skill, error){
		m.loadRepoSkills,
		m.loadCompatSkills,
		m.loadPersonalSkills,
	}
	for _, loadFn := range sources {
		skills, err := loadFn(true)
		if err != nil {
			return nil, false, err
		}
		for _, skill := range skills {
			if strings.ToLower(strings.TrimSpace(skill.Name)) == target {
				return &skill, true, nil
			}
		}
	}

	if m.externalAgents != nil && m.configManager != nil {
		if m.configManager.GetExternalAgentsClaudeEnabled() {
			for _, ext := range m.externalAgents.GetClaudeAgents() {
				if strings.ToLower(strings.TrimSpace(ext.Name)) == target {
					s := Skill{
						Name:        ext.Name,
						Description: ext.Description,
						Prompt:      ext.SystemPrompt,
						Source:      SourceClaude,
						Model:       ext.Model,
						Color:       ext.Color,
						Enabled:     true,
						Trusted:     true,
					}
					return &s, true, nil
				}
			}
		}
		if m.configManager.GetExternalAgentsCodexEnabled() {
			for _, ext := range m.externalAgents.GetCodexAgents() {
				if strings.ToLower(strings.TrimSpace(ext.Name)) == target {
					s := Skill{
						Name:        ext.Name,
						Description: ext.Description,
						Prompt:      ext.SystemPrompt,
						Source:      SourceCodex,
						Enabled:     true,
						Trusted:     true,
					}
					return &s, true, nil
				}
			}
		}
	}

	return nil, false, nil
}

// ResolveSkillsByNames resolves multiple skills by name, returning found skills
// and a list of names that could not be resolved.
func (m *Manager) ResolveSkillsByNames(skillNames []string) ([]Skill, []string, error) {
	var resolved []Skill
	var unresolved []string
	for _, name := range skillNames {
		skill, found, err := m.ResolveSkillByName(name)
		if err != nil {
			return nil, nil, err
		}
		if found {
			resolved = append(resolved, *skill)
		} else {
			unresolved = append(unresolved, name)
		}
	}
	return resolved, unresolved, nil
}

func (m *Manager) listSkills(agentName string, includePrompt bool) ([]Skill, error) {
	agentSkills, err := m.loadAgentSkills(agentName, includePrompt)
	if err != nil {
		return nil, err
	}
	repoSkills, err := m.loadRepoSkills(includePrompt)
	if err != nil {
		return nil, err
	}
	compatSkills, err := m.loadCompatSkills(includePrompt)
	if err != nil {
		return nil, err
	}
	personalSkills, err := m.loadPersonalSkills(includePrompt)
	if err != nil {
		return nil, err
	}

	skillMap := make(map[string]Skill)
	conflictMap := make(map[string]*SkillConflict)

	localSources := [][]Skill{agentSkills, repoSkills, compatSkills}
	for _, sourceList := range localSources {
		for _, skill := range sourceList {
			key := strings.ToLower(skill.Name)
			if key == "" {
				continue
			}
			if existing, exists := skillMap[key]; exists {
				conflict := conflictMap[key]
				if conflict == nil {
					conflict = &SkillConflict{
						Name:    skill.Name,
						Paths:   []string{existing.Path},
						Sources: []string{existing.Source},
					}
				}
				conflict.Paths = append(conflict.Paths, skill.Path)
				conflict.Sources = append(conflict.Sources, skill.Source)
				conflictMap[key] = conflict
				continue
			}
			skillMap[key] = skill
		}
	}

	if len(conflictMap) > 0 {
		conflicts := make([]SkillConflict, 0, len(conflictMap))
		for _, conflict := range conflictMap {
			conflicts = append(conflicts, *conflict)
		}
		return nil, &SkillConflictError{Conflicts: conflicts}
	}

	for _, skill := range personalSkills {
		key := strings.ToLower(skill.Name)
		if key == "" {
			continue
		}
		if _, exists := skillMap[key]; exists {
			continue
		}
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
					Enabled:     true,
					Trusted:     true,
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
					Enabled:     true,
					Trusted:     true,
				}
			}
		}
	}

	skills := make([]Skill, 0, len(skillMap))
	for _, skill := range skillMap {
		skills = append(skills, skill)
	}

	if err := m.applySkillState(agentName, skills); err != nil {
		return nil, err
	}

	return skills, nil
}

func (m *Manager) loadAgentSkills(agentName string, includePrompt bool) ([]Skill, error) {
	if agentName == "" {
		return []Skill{}, nil
	}

	agentsDir, err := resolveAgentsDir(m.agentStorePath)
	if err != nil {
		return nil, err
	}

	skillsDir := filepath.Join(agentsDir, agentName, "skills")
	return m.loadSkillsFromDir(skillsDir, SourceAgent, includePrompt, true, false)
}

func (m *Manager) loadRepoSkills(includePrompt bool) ([]Skill, error) {
	agentsDir, err := resolveAgentsDir(m.agentStorePath)
	if err != nil {
		return nil, err
	}

	skillsDir := filepath.Join(agentsDir, "skills")
	return m.loadSkillsFromDir(skillsDir, SourceRepo, includePrompt, false, true)
}

func (m *Manager) loadCompatSkills(includePrompt bool) ([]Skill, error) {
	compatRoots := resolveCompatSkillsDirs(m.agentStorePath)
	if len(compatRoots) == 0 {
		return []Skill{}, nil
	}

	var allSkills []Skill
	for _, skillsDir := range compatRoots {
		loaded, err := m.loadSkillsFromDir(skillsDir, SourceAgentsCompat, includePrompt, false, true)
		if err != nil {
			return nil, err
		}
		allSkills = append(allSkills, loaded...)
	}
	return allSkills, nil
}

func (m *Manager) loadPersonalSkills(includePrompt bool) ([]Skill, error) {
	skillsDir := strings.TrimSpace(m.personalSkillsDir)
	if skillsDir == "" {
		return []Skill{}, nil
	}
	return m.loadSkillsFromDir(skillsDir, SourcePersonal, includePrompt, false, true)
}

func (m *Manager) loadSkillsFromDir(skillsDir, source string, includePrompt bool, allowSingleFile bool, allowCategories bool) ([]Skill, error) {
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
			skillDir := filepath.Join(skillsDir, entry.Name())
			skillPath := filepath.Join(skillDir, "SKILL.md")
			if _, err := os.Stat(skillPath); err == nil {
				skill, err := m.loadSkillEntry(skillPath, entry.Name(), source, skillDir, includePrompt)
				if err == nil {
					skills = append(skills, skill)
				}
				continue
			}

			if allowCategories {
				subEntries, err := os.ReadDir(skillDir)
				if err != nil {
					continue
				}
				for _, sub := range subEntries {
					if !sub.IsDir() {
						continue
					}
					subDir := filepath.Join(skillDir, sub.Name())
					subPath := filepath.Join(subDir, "SKILL.md")
					if _, err := os.Stat(subPath); err != nil {
						continue
					}
					skill, err := m.loadSkillEntry(subPath, sub.Name(), source, subDir, includePrompt)
					if err == nil {
						skills = append(skills, skill)
					}
				}
			}
			continue
		}

		if allowSingleFile && strings.HasSuffix(entry.Name(), ".md") {
			skillPath := filepath.Join(skillsDir, entry.Name())
			baseName := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			skill, err := m.loadSkillEntry(skillPath, baseName, source, filepath.Dir(skillPath), includePrompt)
			if err == nil {
				skills = append(skills, skill)
			}
		}
	}

	return skills, nil
}

func (m *Manager) loadSkillEntry(skillPath, defaultName, source, skillDir string, includePrompt bool) (Skill, error) {
	skill, err := parseSkillFile(skillPath, defaultName, includePrompt)
	if err != nil {
		return Skill{}, err
	}

	skill.Source = source
	skill.Path = skillPath

	if skillDir != "" {
		skill.HasScripts = hasScripts(skillDir)
		meta, err := loadOpenAIMetadata(skillDir)
		if err != nil {
			skill.ValidationErrors = append(skill.ValidationErrors, fmt.Sprintf("openai.yaml: %v", err))
		} else if meta != nil {
			skill.OpenAIMetadata = meta
			if len(skill.AllowedTools) == 0 && len(meta.Tools) > 0 {
				skill.AllowedTools = meta.Tools
			}
			if len(skill.RequiredMCPServers) == 0 && len(meta.MCPServers) > 0 {
				skill.RequiredMCPServers = meta.MCPServers
			}
		}
	}

	return skill, nil
}

func (m *Manager) loadSkillWithPrompt(skill Skill) (*Skill, error) {
	if skill.Path == "" {
		return &skill, nil
	}

	skillDir := filepath.Dir(skill.Path)
	full, err := m.loadSkillEntry(skill.Path, skill.Name, skill.Source, skillDir, true)
	if err != nil {
		return nil, err
	}

	full.Enabled = skill.Enabled
	full.Trusted = skill.Trusted

	return &full, nil
}

func (m *Manager) applySkillState(agentName string, skills []Skill) error {
	if agentName == "" {
		// No agent context (global catalog / chat slash-command picker):
		// surface every skill as available rather than per-agent enabled.
		for i := range skills {
			if !skills[i].Enabled {
				skills[i].Enabled = true
			}
		}
		return nil
	}

	registry, _, err := m.getSkillRegistry(agentName)
	if err != nil {
		return err
	}

	defaultState, hasDefaultState := registry.Skills["*"]

	for i := range skills {
		key := normalizeSkillKey(skills[i].Name)
		if state, ok := registry.Skills[key]; ok {
			skills[i].Enabled = state.Enabled
			skills[i].Trusted = state.Trusted
			continue
		}
		if hasDefaultState {
			skills[i].Enabled = defaultState.Enabled
			skills[i].Trusted = defaultState.Trusted
			continue
		}
		// Opt-in model: with no explicit per-skill state and no agent-wide
		// ("*") default, keep the skill's loaded default. File-based skills load
		// disabled (zero value); built-in CLI-agent skills load enabled.
	}

	return nil
}

func hasScripts(skillDir string) bool {
	if skillDir == "" {
		return false
	}
	scriptsDir := filepath.Join(skillDir, "scripts")
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

type skillFrontmatter struct {
	Name               string
	Description        string
	AllowedTools       []string
	DisallowedTools    []string
	RequiredMCPServers []string
}

func parseSkillFile(path string, defaultName string, includePrompt bool) (Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	frontmatter, body := parseFrontmatter(string(content))

	fm, fmErr := parseSkillFrontmatter(frontmatter)
	name := fm.Name
	if name == "" {
		name = defaultName
	}

	skill := Skill{
		Name:               name,
		Description:        fm.Description,
		AllowedTools:       fm.AllowedTools,
		DisallowedTools:    fm.DisallowedTools,
		RequiredMCPServers: fm.RequiredMCPServers,
	}
	if includePrompt {
		skill.Prompt = strings.TrimSpace(body)
	}

	skill.ValidationErrors = validateSkillMetadata(name, fm.Description)
	if fmErr != nil {
		skill.ValidationErrors = append(skill.ValidationErrors, fmt.Sprintf("invalid frontmatter: %v", fmErr))
	}

	return skill, nil
}

func parseSkillFrontmatter(frontmatter string) (skillFrontmatter, error) {
	if strings.TrimSpace(frontmatter) == "" {
		return skillFrontmatter{}, nil
	}

	raw := make(map[string]any)
	if err := yaml.Unmarshal([]byte(frontmatter), &raw); err != nil {
		return skillFrontmatter{}, err
	}

	fm := skillFrontmatter{
		Name:               getStringField(raw, "name"),
		Description:        getStringField(raw, "description"),
		AllowedTools:       getStringSliceField(raw, "allowed_tools", "allowed-tools"),
		DisallowedTools:    getStringSliceField(raw, "disallowed_tools", "disallowed-tools"),
		RequiredMCPServers: getStringSliceField(raw, "required_mcp_servers", "required-mcp-servers"),
	}

	return fm, nil
}

func parseFrontmatter(content string) (frontmatter, body string) {
	const delimiter = "---"

	content = strings.TrimLeft(content, "\n\r\t ")
	if !strings.HasPrefix(content, delimiter) {
		return "", content
	}

	rest := content[len(delimiter):]
	endIdx := strings.Index(rest, "\n"+delimiter)
	if endIdx == -1 {
		return "", content
	}

	frontmatter = strings.TrimSpace(rest[:endIdx])
	body = strings.TrimLeft(rest[endIdx+len("\n"+delimiter):], "\n\r")
	return frontmatter, body
}

func getStringField(raw map[string]any, key string) string {
	if val, ok := raw[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getStringSliceField(raw map[string]any, keys ...string) []string {
	for _, key := range keys {
		if val, ok := raw[key]; ok {
			return interfaceSliceToStrings(val)
		}
	}
	return nil
}

func interfaceSliceToStrings(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
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

func resolveCompatSkillsDirs(agentStorePath string) []string {
	if strings.TrimSpace(agentStorePath) == "" {
		return nil
	}

	repoRoot := filepath.Dir(agentStorePath)
	candidates := []string{
		filepath.Join(repoRoot, ".agents", "skills"),
	}

	if strings.EqualFold(filepath.Base(repoRoot), "ori-data") {
		candidates = append(candidates, filepath.Join(filepath.Dir(repoRoot), ".agents", "skills"))
	}

	seen := make(map[string]struct{}, len(candidates))
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		cleaned := filepath.Clean(strings.TrimSpace(candidate))
		if cleaned == "." || cleaned == "" {
			continue
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		result = append(result, cleaned)
	}
	return result
}
