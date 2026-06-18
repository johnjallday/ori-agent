package plugin

import (
	"fmt"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

// MCPRegistrar registers and removes MCP servers. It is satisfied by an adapter
// over Ori's MCP config manager + runtime registry, wired during server setup
// (task 4.x). Keeping it an interface lets the installer be unit-tested without
// a live server.
type MCPRegistrar interface {
	AddServer(cfg mcp.ServerConfig) error
	RemoveServer(name string) error
}

// SkillInstaller makes a plugin's skill directory discoverable by Ori and
// removes it on uninstall. Satisfied by an adapter over the skills manager
// (task 4.x).
type SkillInstaller interface {
	InstallSkill(pluginName, skillName, srcDir string) error
	RemoveSkill(pluginName, skillName string) error
}

// RegisterResult reports what an install registered, plus non-fatal warnings.
type RegisterResult struct {
	MCPServers     []string               // namespaced server names registered
	Skills         []string               // skill names registered
	BinaryWarnings []string               // servers whose command is missing/not executable
	Unsupported    []UnsupportedComponent // components skipped-and-reported
}

// Register registers a descriptor's Phase-1 components (MCP servers + skills)
// through the injected registrar/installer. Components are registered disabled;
// the trust gate and per-workspace binding (task 3.x) enable them. On any
// failure the partial registration is rolled back so install is transactional.
func Register(d PluginDescriptor, reg MCPRegistrar, skills SkillInstaller) (RegisterResult, error) {
	var res RegisterResult
	res.Unsupported = d.Unsupported

	for _, spec := range d.MCPServers {
		cfg := ToServerConfig(d.Name, spec, d.InstallDir)
		if !CommandAvailable(cfg.Command) {
			res.BinaryWarnings = append(res.BinaryWarnings,
				fmt.Sprintf("%s: command %q not found or not executable — install the binary, then re-enable", cfg.Name, cfg.Command))
		}
		if err := reg.AddServer(cfg); err != nil {
			rollback(reg, skills, d.Name, res)
			return RegisterResult{}, fmt.Errorf("plugin %q: register MCP server %q: %w", d.Name, cfg.Name, err)
		}
		res.MCPServers = append(res.MCPServers, cfg.Name)
	}

	for _, s := range d.Skills {
		if err := skills.InstallSkill(d.Name, s.Name, s.Path); err != nil {
			rollback(reg, skills, d.Name, res)
			return RegisterResult{}, fmt.Errorf("plugin %q: install skill %q: %w", d.Name, s.Name, err)
		}
		res.Skills = append(res.Skills, s.Name)
	}

	return res, nil
}

// rollback removes whatever was registered so far (best effort).
func rollback(reg MCPRegistrar, skills SkillInstaller, pluginName string, res RegisterResult) {
	for _, name := range res.MCPServers {
		_ = reg.RemoveServer(name)
	}
	for _, name := range res.Skills {
		_ = skills.RemoveSkill(pluginName, name)
	}
}
