// Package externalagents provides readers for external AI CLI tool configurations
// such as Claude Code (~/.claude) and OpenAI Codex (~/.codex).
package externalagents

// Source identifiers for external agents
const (
	SourceClaude = "claude"
	SourceCodex  = "codex"
)

// ExternalAgent represents an agent configuration from an external AI CLI tool.
type ExternalAgent struct {
	Source       string `json:"source"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Model        string `json:"model,omitempty"`
	Color        string `json:"color,omitempty"`
	SystemPrompt string `json:"systemPrompt,omitempty"`
}

// ClaudeSettings represents the global settings from ~/.claude/settings.json.
type ClaudeSettings struct {
	Model          string            `json:"model,omitempty"`
	Permissions    ClaudePermissions `json:"permissions"`
	EnabledPlugins map[string]bool   `json:"enabledPlugins"`
	StatusLine     map[string]any    `json:"statusLine,omitempty"`
}

// ClaudeMCPServer represents an MCP server configured for Claude Code in
// ~/.claude.json. Env values are intentionally NOT included — only variable
// names are surfaced, to avoid leaking secrets/API keys to the API/UI.
type ClaudeMCPServer struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport,omitempty"` // from the "type" field, e.g. "stdio", "sse", "http"
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	EnvNames  []string `json:"envNames,omitempty"` // variable names only — never values
}

// ClaudeRecentProject represents a recently used Claude Code project, read from
// the "projects" map in ~/.claude.json. Only lightweight last-session metrics
// are surfaced; no transcript bodies are read.
type ClaudeRecentProject struct {
	Path             string  `json:"path"`
	LastSessionID    string  `json:"lastSessionId,omitempty"`
	LastCost         float64 `json:"lastCost,omitempty"`
	LastDuration     int64   `json:"lastDuration,omitempty"`
	LastLinesAdded   int     `json:"lastLinesAdded,omitempty"`
	LastLinesRemoved int     `json:"lastLinesRemoved,omitempty"`
}

// ClaudePermissions represents the permissions configuration in Claude settings.
type ClaudePermissions struct {
	Allow       []string `json:"allow"`
	Deny        []string `json:"deny"`
	DefaultMode string   `json:"defaultMode"`
}

// ClaudePlugin represents an installed plugin from ~/.claude/plugins/installed_plugins.json.
type ClaudePlugin struct {
	Name         string `json:"name"`
	Scope        string `json:"scope"`
	InstallPath  string `json:"installPath"`
	Version      string `json:"version"`
	InstalledAt  string `json:"installedAt,omitempty"`
	LastUpdated  string `json:"lastUpdated,omitempty"`
	GitCommitSha string `json:"gitCommitSha,omitempty"`
	ProjectPath  string `json:"projectPath,omitempty"`
}

// CodexConfig represents the configuration from ~/.codex/config.toml.
type CodexConfig struct {
	Model                string `json:"model"`
	ModelReasoningEffort string `json:"modelReasoningEffort,omitempty"`
}

// CodexSkill represents a skill directory in ~/.codex/skills/.
type CodexSkill struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// CodexRule represents a rule file in ~/.codex/rules/.
type CodexRule struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// CodexMCPServer represents an MCP server configured for Codex in
// ~/.codex/config.toml. Env values are intentionally NOT included — only
// variable names are surfaced, to avoid leaking secrets/API keys to the API/UI.
type CodexMCPServer struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport,omitempty"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	URL       string   `json:"url,omitempty"`
	EnvNames  []string `json:"envNames,omitempty"` // variable names only — never values
}

// ClaudeData holds all data read from the Claude Code configuration.
type ClaudeData struct {
	Agents         []ExternalAgent       `json:"agents"`
	Settings       *ClaudeSettings       `json:"settings,omitempty"`
	Plugins        []ClaudePlugin        `json:"plugins"`
	MCPServers     []ClaudeMCPServer     `json:"mcpServers"`
	RecentProjects []ClaudeRecentProject `json:"recentProjects"`
}

// CodexData holds all data read from the Codex configuration.
type CodexData struct {
	Agents     []ExternalAgent  `json:"agents"`
	Config     *CodexConfig     `json:"config,omitempty"`
	Skills     []CodexSkill     `json:"skills"`
	Rules      []CodexRule      `json:"rules"`
	MCPServers []CodexMCPServer `json:"mcpServers"`
}

// ExternalAgentsData holds all external agent data from all sources.
type ExternalAgentsData struct {
	Claude *ClaudeData `json:"claude,omitempty"`
	Codex  *CodexData  `json:"codex,omitempty"`
}

// ClaudeReader defines the interface for reading Claude Code data.
type ClaudeReader interface {
	ReadAgents() ([]ExternalAgent, error)
	ReadSettings() (*ClaudeSettings, error)
	ReadPlugins() ([]ClaudePlugin, error)
	ReadMCPServers() ([]ClaudeMCPServer, error)
	ReadRecentProjects(limit int) ([]ClaudeRecentProject, error)
}

// CodexReader defines the interface for reading Codex data.
type CodexReader interface {
	ReadAgents() ([]ExternalAgent, error)
	ReadConfig() (*CodexConfig, error)
	ReadSkills() ([]CodexSkill, error)
	ReadRules() ([]CodexRule, error)
	ReadMCPServers() ([]CodexMCPServer, error)
}
