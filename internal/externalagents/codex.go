package externalagents

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultCodexReader implements CodexReader for reading Codex data from ~/.codex.
type DefaultCodexReader struct {
	baseDir string // Allows overriding for testing
}

// NewCodexReader creates a new DefaultCodexReader.
// If baseDir is empty, it defaults to ~/.codex.
func NewCodexReader(baseDir string) *DefaultCodexReader {
	return &DefaultCodexReader{baseDir: baseDir}
}

// getCodexDir returns the Codex configuration directory path.
func (r *DefaultCodexReader) getCodexDir() (string, error) {
	if r.baseDir != "" {
		return r.baseDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

// codexConfigFile represents the structure of config.toml.
type codexConfigFile struct {
	Model                string `toml:"model"`
	ModelReasoningEffort string `toml:"model_reasoning_effort"`
}

// ReadConfig reads the configuration from ~/.codex/config.toml.
func (r *DefaultCodexReader) ReadConfig() (*CodexConfig, error) {
	codexDir, err := r.getCodexDir()
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(codexDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var config codexConfigFile
	if _, err := toml.Decode(string(data), &config); err != nil {
		return nil, err
	}

	return &CodexConfig{
		Model:                config.Model,
		ModelReasoningEffort: config.ModelReasoningEffort,
	}, nil
}

// ReadSkills reads skill directories from ~/.codex/skills/.
func (r *DefaultCodexReader) ReadSkills() ([]CodexSkill, error) {
	codexDir, err := r.getCodexDir()
	if err != nil {
		return nil, err
	}

	skillsDir := filepath.Join(codexDir, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []CodexSkill{}, nil
		}
		return nil, err
	}

	var skills []CodexSkill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Skip hidden directories like .system
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skills = append(skills, CodexSkill{
			Name: entry.Name(),
			Path: filepath.Join(skillsDir, entry.Name()),
		})
	}

	return skills, nil
}

// ReadRules reads rule files from ~/.codex/rules/.
func (r *DefaultCodexReader) ReadRules() ([]CodexRule, error) {
	codexDir, err := r.getCodexDir()
	if err != nil {
		return nil, err
	}

	rulesDir := filepath.Join(codexDir, "rules")
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []CodexRule{}, nil
		}
		return nil, err
	}

	var rules []CodexRule
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// Only include .rules files
		if !strings.HasSuffix(entry.Name(), ".rules") {
			continue
		}
		rules = append(rules, CodexRule{
			Name: strings.TrimSuffix(entry.Name(), ".rules"),
			Path: filepath.Join(rulesDir, entry.Name()),
		})
	}

	return rules, nil
}

// ReadAgents reads all skill definitions from ~/.codex/skills/ as agents.
// Skills are stored in subdirectories with SKILL.md files containing YAML frontmatter.
func (r *DefaultCodexReader) ReadAgents() ([]ExternalAgent, error) {
	codexDir, err := r.getCodexDir()
	if err != nil {
		return nil, err
	}

	skillsDir := filepath.Join(codexDir, "skills")
	var agents []ExternalAgent

	// Walk through skills directories (public, .system, etc.)
	topEntries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ExternalAgent{}, nil
		}
		return nil, err
	}

	for _, topEntry := range topEntries {
		if !topEntry.IsDir() {
			continue
		}

		// Read skill subdirectories within each category (public, .system)
		categoryDir := filepath.Join(skillsDir, topEntry.Name())
		skillEntries, err := os.ReadDir(categoryDir)
		if err != nil {
			continue
		}

		for _, skillEntry := range skillEntries {
			if !skillEntry.IsDir() {
				continue
			}

			// Look for SKILL.md file
			skillMdPath := filepath.Join(categoryDir, skillEntry.Name(), "SKILL.md")
			agent, err := r.parseSkillFile(skillMdPath)
			if err != nil {
				// Skip skills that can't be parsed
				continue
			}
			agents = append(agents, agent)
		}
	}

	return agents, nil
}

// skillFrontmatter represents the YAML frontmatter in SKILL.md files.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// parseSkillFile parses a Codex skill markdown file with YAML frontmatter.
func (r *DefaultCodexReader) parseSkillFile(path string) (ExternalAgent, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return ExternalAgent{}, err
	}

	frontmatter, body := parseFrontmatter(string(content))

	// Use manual parsing like Claude agents to handle special characters
	fm := parseSkillFrontmatterManual(frontmatter)

	return ExternalAgent{
		Source:       SourceCodex,
		Name:         fm.Name,
		Description:  fm.Description,
		SystemPrompt: strings.TrimSpace(body),
	}, nil
}

// parseSkillFrontmatterManual parses frontmatter line by line for SKILL.md files.
func parseSkillFrontmatterManual(frontmatter string) skillFrontmatter {
	var fm skillFrontmatter
	lines := strings.Split(frontmatter, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Find the first colon to split key and value
		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			continue
		}

		key := strings.TrimSpace(line[:colonIdx])
		value := strings.TrimSpace(line[colonIdx+1:])

		// Remove surrounding quotes if present
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}

		switch key {
		case "name":
			fm.Name = value
		case "description":
			// Unescape literal \n sequences
			fm.Description = strings.ReplaceAll(value, "\\n", "\n")
		}
	}

	return fm
}
