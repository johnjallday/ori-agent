// Package plugin installs Claude Code- and Codex-compatible plugins into Ori.
//
// A plugin is a packaging layer over Ori's existing MCP + skills primitives:
// installing one resolves an external bundle, normalizes its manifest into a
// PluginDescriptor, and (elsewhere) registers the supported components through
// mechanisms Ori already has. It is NOT a runtime-extension protocol — the old
// gRPC plugin system is not revived here.
package plugin

import "github.com/johnjallday/ori-agent/internal/projecttemplates"

// SourceFormat identifies which external plugin packaging format a bundle uses.
type SourceFormat string

const (
	// FormatClaude is the Claude Code packaging format (.claude-plugin/plugin.json).
	FormatClaude SourceFormat = "claude"
	// FormatCodex is the Codex packaging format (.codex-plugin/plugin.json).
	FormatCodex SourceFormat = "codex"
)

// PluginDescriptor is Ori's normalized, format-agnostic view of a plugin,
// produced from a Claude Code or Codex manifest before component registration.
type PluginDescriptor struct {
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`

	SourceFormat   SourceFormat `json:"source_format"`
	SourceLocation string       `json:"source_location,omitempty"`
	InstallDir     string       `json:"install_dir"`

	MCPServers         []MCPServerSpec        `json:"mcp_servers,omitempty"`
	Skills             []SkillSpec            `json:"skills,omitempty"`
	Interface          *InterfaceMetadata     `json:"interface,omitempty"`
	WorkspaceSurfaces  *SurfaceContribution   `json:"workspace_surfaces,omitempty"`
	ResolvedBlueprints []ResolvedBlueprint    `json:"resolved_blueprints,omitempty"`
	TrustedAssetDigest string                 `json:"trusted_asset_digest,omitempty"`
	Unsupported        []UnsupportedComponent `json:"unsupported,omitempty"`
}

// ResolvedBlueprint is the install-time validated trusted blueprint component.
// SkeletonRoot is global trusted state and is never copied into a workspace.
type ResolvedBlueprint struct {
	ID             string                    `json:"id"`
	QualifiedID    string                    `json:"qualified_id"`
	Version        int                       `json:"version"`
	Template       projecttemplates.Template `json:"template"`
	SkeletonRoot   string                    `json:"skeleton_root"`
	SkeletonDigest string                    `json:"skeleton_digest"`
}

// MCPServerSpec is one MCP server declared by a plugin, before resolution to a
// runtime mcp.ServerConfig. Cwd is the raw working directory from the manifest
// (Codex uses relative Command + Cwd); the installer resolves Cwd + a relative
// Command into an absolute command, since mcp.ServerConfig has no cwd field.
type MCPServerSpec struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
}

// SkillSpec points at a skill directory (containing SKILL.md) bundled by a plugin.
type SkillSpec struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// InterfaceMetadata holds Codex presentation metadata (the manifest "interface"
// block). It is display-only; the capability itself is delivered via the
// plugin's MCP server.
type InterfaceMetadata struct {
	DisplayName      string   `json:"display_name,omitempty"`
	ShortDescription string   `json:"short_description,omitempty"`
	LongDescription  string   `json:"long_description,omitempty"`
	Category         string   `json:"category,omitempty"`
	Logo             string   `json:"logo,omitempty"`
	DefaultPrompt    []string `json:"default_prompt,omitempty"`
}

// UnsupportedComponent records a component Ori does not yet register (commands,
// agents, hooks, Codex app connectors) so install can skip-and-report it rather
// than fail.
type UnsupportedComponent struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}
