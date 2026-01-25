package externalagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultClaudeReader implements ClaudeReader for reading Claude Code data from ~/.claude.
type DefaultClaudeReader struct {
	baseDir string // Allows overriding for testing
}

// NewClaudeReader creates a new DefaultClaudeReader.
// If baseDir is empty, it defaults to ~/.claude.
func NewClaudeReader(baseDir string) *DefaultClaudeReader {
	return &DefaultClaudeReader{baseDir: baseDir}
}

// getClaudeDir returns the Claude configuration directory path.
func (r *DefaultClaudeReader) getClaudeDir() (string, error) {
	if r.baseDir != "" {
		return r.baseDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// ReadAgents reads all agent definitions from ~/.claude/agents/*.md files.
func (r *DefaultClaudeReader) ReadAgents() ([]ExternalAgent, error) {
	claudeDir, err := r.getClaudeDir()
	if err != nil {
		return nil, err
	}

	agentsDir := filepath.Join(claudeDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ExternalAgent{}, nil
		}
		return nil, err
	}

	var agents []ExternalAgent
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		agentPath := filepath.Join(agentsDir, entry.Name())
		agent, err := r.parseAgentFile(agentPath)
		if err != nil {
			// Log parsing errors for debugging
			fmt.Fprintf(os.Stderr, "Warning: failed to parse Claude agent file %s: %v\n", agentPath, err)
			continue
		}
		agents = append(agents, agent)
	}

	return agents, nil
}

// agentFrontmatter represents the YAML frontmatter in agent .md files.
type agentFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Model       string `yaml:"model"`
	Color       string `yaml:"color"`
}

// parseAgentFile parses a Claude agent markdown file with YAML frontmatter.
func (r *DefaultClaudeReader) parseAgentFile(path string) (ExternalAgent, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return ExternalAgent{}, err
	}

	frontmatter, body, err := parseFrontmatter(string(content))
	if err != nil {
		return ExternalAgent{}, err
	}

	// Try standard YAML parsing first
	var fm agentFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		// Fallback to line-by-line parsing for files with unquoted special characters
		fm = parseAgentFrontmatterManual(frontmatter)
	}

	return ExternalAgent{
		Source:       SourceClaude,
		Name:         fm.Name,
		Description:  fm.Description,
		Model:        fm.Model,
		Color:        fm.Color,
		SystemPrompt: strings.TrimSpace(body),
	}, nil
}

// parseAgentFrontmatterManual parses frontmatter line by line for files with
// unquoted special characters that break standard YAML parsing.
func parseAgentFrontmatterManual(frontmatter string) agentFrontmatter {
	var fm agentFrontmatter
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
		case "model":
			fm.Model = value
		case "color":
			fm.Color = value
		}
	}

	return fm
}

// parseFrontmatter extracts YAML frontmatter and body from markdown content.
// Frontmatter is delimited by --- at the start and end.
func parseFrontmatter(content string) (frontmatter, body string, err error) {
	const delimiter = "---"

	// Trim leading whitespace/newlines
	content = strings.TrimLeft(content, "\n\r\t ")

	if !strings.HasPrefix(content, delimiter) {
		// No frontmatter, entire content is body
		return "", content, nil
	}

	// Find the end of frontmatter
	rest := content[len(delimiter):]
	endIdx := strings.Index(rest, "\n"+delimiter)
	if endIdx == -1 {
		// No closing delimiter, treat as no frontmatter
		return "", content, nil
	}

	frontmatter = strings.TrimSpace(rest[:endIdx])
	body = strings.TrimLeft(rest[endIdx+len("\n"+delimiter):], "\n\r")

	return frontmatter, body, nil
}

// ReadSettings reads the global settings from ~/.claude/settings.json.
func (r *DefaultClaudeReader) ReadSettings() (*ClaudeSettings, error) {
	claudeDir, err := r.getClaudeDir()
	if err != nil {
		return nil, err
	}

	settingsPath := filepath.Join(claudeDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var settings ClaudeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}

	return &settings, nil
}

// installedPluginsFile represents the structure of installed_plugins.json.
type installedPluginsFile struct {
	Version int                              `json:"version"`
	Plugins map[string][]installedPluginInfo `json:"plugins"`
}

type installedPluginInfo struct {
	Scope        string `json:"scope"`
	InstallPath  string `json:"installPath"`
	Version      string `json:"version"`
	InstalledAt  string `json:"installedAt"`
	LastUpdated  string `json:"lastUpdated"`
	GitCommitSha string `json:"gitCommitSha"`
	ProjectPath  string `json:"projectPath"`
}

// ReadPlugins reads installed plugins from ~/.claude/plugins/installed_plugins.json.
func (r *DefaultClaudeReader) ReadPlugins() ([]ClaudePlugin, error) {
	claudeDir, err := r.getClaudeDir()
	if err != nil {
		return nil, err
	}

	pluginsPath := filepath.Join(claudeDir, "plugins", "installed_plugins.json")
	data, err := os.ReadFile(pluginsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []ClaudePlugin{}, nil
		}
		return nil, err
	}

	var file installedPluginsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	var plugins []ClaudePlugin
	for name, infos := range file.Plugins {
		for _, info := range infos {
			plugins = append(plugins, ClaudePlugin{
				Name:         name,
				Scope:        info.Scope,
				InstallPath:  info.InstallPath,
				Version:      info.Version,
				InstalledAt:  info.InstalledAt,
				LastUpdated:  info.LastUpdated,
				GitCommitSha: info.GitCommitSha,
				ProjectPath:  info.ProjectPath,
			})
		}
	}

	return plugins, nil
}
