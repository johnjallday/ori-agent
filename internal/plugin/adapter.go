package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Normalize converts a located manifest into a PluginDescriptor, reading the
// plugin's .mcp.json and skills/ directory and recording unsupported components.
func Normalize(m locatedManifest, sourceLocation string) (PluginDescriptor, error) {
	d := PluginDescriptor{
		Name:           m.raw.Name,
		Version:        m.raw.Version,
		Description:    m.raw.Description,
		Author:         authorName(m.raw.Author),
		Homepage:       m.raw.Homepage,
		Repository:     m.raw.Repository,
		Keywords:       m.raw.Keywords,
		SourceFormat:   m.format,
		SourceLocation: sourceLocation,
		InstallDir:     m.root,
	}

	servers, err := loadMCPServers(m)
	if err != nil {
		return PluginDescriptor{}, err
	}
	d.MCPServers = servers
	d.Skills = loadSkills(m)

	if m.raw.Interface != nil {
		d.Interface = &InterfaceMetadata{
			DisplayName:      m.raw.Interface.DisplayName,
			ShortDescription: m.raw.Interface.ShortDescription,
			LongDescription:  m.raw.Interface.LongDescription,
			Category:         m.raw.Interface.Category,
			Logo:             m.raw.Interface.Logo,
			DefaultPrompt:    m.raw.Interface.DefaultPrompt,
		}
	}

	d.WorkspaceSurfaces = m.contribution
	d.Unsupported = collectUnsupported(m)
	return d, nil
}

// loadMCPServers resolves the manifest's mcpServers declaration, which may be:
//   - a path string ("./.mcp.json"), Codex-style;
//   - an inline object, Claude inline-style; or
//   - absent, in which case a root .mcp.json is auto-discovered.
//
// The .mcp.json file itself may be a bare keyed map (Claude) or wrapped under a
// "mcpServers" key (Codex).
func loadMCPServers(m locatedManifest) ([]MCPServerSpec, error) {
	if raw := m.raw.MCPServers; len(raw) > 0 {
		if path, ok := jsonString(raw); ok {
			return readMCPFile(filepath.Join(m.root, filepath.Clean(path)))
		}
		return parseMCPMap(raw)
	}
	def := filepath.Join(m.root, ".mcp.json")
	if fileExists(def) {
		return readMCPFile(def)
	}
	return nil, nil
}

func readMCPFile(path string) ([]MCPServerSpec, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path within a user-provided plugin directory
	if err != nil {
		return nil, fmt.Errorf("plugin: read mcp config %s: %w", path, err)
	}
	// Codex-style wrapper: {"mcpServers": {...}}.
	var wrapped struct {
		MCPServers json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.MCPServers) > 0 {
		return parseMCPMap(wrapped.MCPServers)
	}
	// Claude-style bare keyed map.
	return parseMCPMap(data)
}

type rawMCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Cwd     string            `json:"cwd"`
}

func parseMCPMap(raw json.RawMessage) ([]MCPServerSpec, error) {
	var mm map[string]rawMCPServer
	if err := json.Unmarshal(raw, &mm); err != nil {
		return nil, fmt.Errorf("plugin: parse mcp servers: %w", err)
	}
	out := make([]MCPServerSpec, 0, len(mm))
	for name, s := range mm {
		out = append(out, MCPServerSpec{
			Name:    name,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			Cwd:     s.Cwd,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func loadSkills(m locatedManifest) []SkillSpec {
	skillsDir := filepath.Join(m.root, "skills")
	if path, ok := jsonString(m.raw.Skills); ok && strings.TrimSpace(path) != "" {
		skillsDir = filepath.Join(m.root, filepath.Clean(path))
	}
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var out []SkillSpec
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(skillsDir, e.Name())
		if fileExists(filepath.Join(dir, "SKILL.md")) {
			out = append(out, SkillSpec{Name: e.Name(), Path: dir})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func collectUnsupported(m locatedManifest) []UnsupportedComponent {
	var out []UnsupportedComponent
	if hasComponent(m.raw.Commands) || dirHasFiles(filepath.Join(m.root, "commands")) {
		out = append(out, UnsupportedComponent{Kind: "command", Detail: "slash commands are not yet registered (P2)"})
	}
	if hasComponent(m.raw.Agents) || dirHasFiles(filepath.Join(m.root, "agents")) {
		out = append(out, UnsupportedComponent{Kind: "agent", Detail: "agents are not yet registered (P2)"})
	}
	if hasComponent(m.raw.Hooks) || fileExists(filepath.Join(m.root, "hooks", "hooks.json")) {
		out = append(out, UnsupportedComponent{Kind: "hook", Detail: "hooks are not yet executed (P3)"})
	}
	if fileExists(filepath.Join(m.root, ".app.json")) {
		out = append(out, UnsupportedComponent{Kind: "app-connector", Detail: "Codex app connectors are metadata-only"})
	}
	return out
}

func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	return "", false
}

func hasComponent(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

func dirHasFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}
