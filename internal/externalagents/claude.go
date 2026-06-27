package externalagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// defaultRecentProjectsLimit is used when ReadRecentProjects is called with a
// non-positive limit.
const defaultRecentProjectsLimit = 5

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

	frontmatter, body := parseFrontmatter(string(content))

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
func parseFrontmatter(content string) (frontmatter, body string) {
	const delimiter = "---"

	// Trim leading whitespace/newlines
	content = strings.TrimLeft(content, "\n\r\t ")

	if !strings.HasPrefix(content, delimiter) {
		// No frontmatter, entire content is body
		return "", content
	}

	// Find the end of frontmatter
	rest := content[len(delimiter):]
	endIdx := strings.Index(rest, "\n"+delimiter)
	if endIdx == -1 {
		// No closing delimiter, treat as no frontmatter
		return "", content
	}

	frontmatter = strings.TrimSpace(rest[:endIdx])
	body = strings.TrimLeft(rest[endIdx+len("\n"+delimiter):], "\n\r")

	return frontmatter, body
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

// claudeJSONFile is a partial view of ~/.claude.json. Only the fields needed by
// the reader are declared so the (large, secret-bearing) file is never fully
// decoded or re-serialized.
type claudeJSONFile struct {
	MCPServers map[string]claudeJSONMCPServer `json:"mcpServers"`
	Projects   map[string]claudeJSONProject   `json:"projects"`
}

type claudeJSONMCPServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

type claudeJSONProject struct {
	LastSessionID    string  `json:"lastSessionId"`
	LastCost         float64 `json:"lastCost"`
	LastDuration     int64   `json:"lastDuration"`
	LastLinesAdded   int     `json:"lastLinesAdded"`
	LastLinesRemoved int     `json:"lastLinesRemoved"`
}

// getClaudeJSONPath returns the path to ~/.claude.json. Note this file is a
// SIBLING of the ~/.claude directory, not inside it. When baseDir is set (for
// testing), the fixture file is expected at <baseDir>/.claude.json so tests stay
// self-contained.
func (r *DefaultClaudeReader) getClaudeJSONPath() (string, error) {
	if r.baseDir != "" {
		return filepath.Join(r.baseDir, ".claude.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude.json"), nil
}

// readClaudeJSON reads and parses ~/.claude.json. A missing file is not an
// error (returns nil, nil) so callers degrade to empty sections.
func (r *DefaultClaudeReader) readClaudeJSON() (*claudeJSONFile, error) {
	path, err := r.getClaudeJSONPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path) // #nosec G304 -- reads the user's own ~/.claude.json config file
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var file claudeJSONFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return &file, nil
}

// ReadMCPServers reads the top-level MCP servers configured for Claude Code from
// ~/.claude.json. Env variable VALUES are deliberately discarded — only the
// names are surfaced, since values may contain secrets/API keys.
func (r *DefaultClaudeReader) ReadMCPServers() ([]ClaudeMCPServer, error) {
	file, err := r.readClaudeJSON()
	if err != nil {
		return nil, err
	}
	if file == nil || len(file.MCPServers) == 0 {
		return []ClaudeMCPServer{}, nil
	}

	servers := make([]ClaudeMCPServer, 0, len(file.MCPServers))
	for name, s := range file.MCPServers {
		server := ClaudeMCPServer{
			Name:      name,
			Transport: s.Type,
			Command:   s.Command,
			Args:      s.Args,
		}
		if len(s.Env) > 0 {
			names := make([]string, 0, len(s.Env))
			for k := range s.Env {
				names = append(names, k)
			}
			sort.Strings(names)
			server.EnvNames = names
		}
		servers = append(servers, server)
	}

	// Stable ordering by server name.
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return servers, nil
}

// ReadRecentProjects returns the most recently used Claude Code projects from
// ~/.claude.json, ordered by the modification time of their session directory
// under ~/.claude/projects/. Projects with no session directory are kept but
// sort last (zero time). limit <= 0 falls back to defaultRecentProjectsLimit.
func (r *DefaultClaudeReader) ReadRecentProjects(limit int) ([]ClaudeRecentProject, error) {
	if limit <= 0 {
		limit = defaultRecentProjectsLimit
	}

	file, err := r.readClaudeJSON()
	if err != nil {
		return nil, err
	}
	if file == nil || len(file.Projects) == 0 {
		return []ClaudeRecentProject{}, nil
	}

	claudeDir, err := r.getClaudeDir()
	if err != nil {
		return nil, err
	}
	projectsDir := filepath.Join(claudeDir, "projects")

	type scoredProject struct {
		project ClaudeRecentProject
		modTime time.Time
	}

	scored := make([]scoredProject, 0, len(file.Projects))
	for path, p := range file.Projects {
		var modTime time.Time
		if info, statErr := os.Stat(filepath.Join(projectsDir, encodeProjectDirName(path))); statErr == nil {
			modTime = info.ModTime()
		}
		scored = append(scored, scoredProject{
			project: ClaudeRecentProject{
				Path:             path,
				LastSessionID:    p.LastSessionID,
				LastCost:         p.LastCost,
				LastDuration:     p.LastDuration,
				LastLinesAdded:   p.LastLinesAdded,
				LastLinesRemoved: p.LastLinesRemoved,
			},
			modTime: modTime,
		})
	}

	// Most recent first; deterministic path tie-break for equal/zero mtimes.
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].modTime.Equal(scored[j].modTime) {
			return scored[i].project.Path < scored[j].project.Path
		}
		return scored[i].modTime.After(scored[j].modTime)
	})

	if len(scored) > limit {
		scored = scored[:limit]
	}

	result := make([]ClaudeRecentProject, 0, len(scored))
	for _, s := range scored {
		result = append(result, s.project)
	}
	return result, nil
}

// encodeProjectDirName converts an absolute project path to the directory name
// Claude Code uses under ~/.claude/projects. Claude replaces path separators and
// dots with hyphens (e.g. /Users/me/.config -> -Users-me--config). Projects
// whose encoded name has no matching directory simply get a zero mtime.
func encodeProjectDirName(path string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(path)
}
