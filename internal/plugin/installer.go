package plugin

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

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

// ErrSkillNameTaken refuses a plugin whose skill name is already used by a
// skill in the Workspace Directory's Skills folder or by another installed
// plugin. The wrapping error names the skill.
var ErrSkillNameTaken = errors.New("plugin skill name is already in use")

// ErrSourceChanged refuses an install or update whose source changed while its
// components were being registered.
var ErrSourceChanged = errors.New("plugin source changed while it was being installed")

// SkillNameGuard refuses plugin skill names already used outside the plugin
// registry, such as a skill in the Workspace Directory's Skills folder. The
// returned error names the clashing skill.
type SkillNameGuard func(pluginName string, skillNames []string) error

// RegisterResult reports what an install registered, plus non-fatal warnings.
type RegisterResult struct {
	MCPServers     []string               // namespaced server names registered
	Skills         []string               // skill names the plugin bundles
	BinaryWarnings []string               // servers whose command is missing/not executable
	Unsupported    []UnsupportedComponent // components skipped-and-reported
}

// Register registers a descriptor's MCP servers through the injected
// registrar. Skills are not copied anywhere: they are read in place from the
// plugin's install folder, so they are only recorded. On any failure the
// partial registration is rolled back so install is transactional.
func Register(d PluginDescriptor, reg MCPRegistrar) (RegisterResult, error) {
	var res RegisterResult
	res.Unsupported = d.Unsupported

	for _, spec := range d.MCPServers {
		cfg := ToServerConfig(d.Name, spec, d.InstallDir)
		if !CommandAvailable(cfg.Command) {
			res.BinaryWarnings = append(res.BinaryWarnings,
				fmt.Sprintf("%s: command %q not found or not executable — install the binary, then re-enable", cfg.Name, cfg.Command))
		}
		if err := reg.AddServer(cfg); err != nil {
			rollback(reg, res)
			return RegisterResult{}, fmt.Errorf("plugin %q: register MCP server %q: %w", d.Name, cfg.Name, err)
		}
		res.MCPServers = append(res.MCPServers, cfg.Name)
	}

	for _, s := range d.Skills {
		res.Skills = append(res.Skills, s.Name)
	}

	return res, nil
}

// rollback removes whatever was registered so far (best effort).
func rollback(reg MCPRegistrar, res RegisterResult) {
	for _, name := range res.MCPServers {
		_ = reg.RemoveServer(name)
	}
}

// skillPathsOf records where each bundled skill lives, relative to the
// plugin's install folder, so the skills manager can read it in place. A skill
// outside the install folder is not recorded; it is found again from the
// manifest when needed.
func skillPathsOf(d PluginDescriptor) map[string]string {
	if len(d.Skills) == 0 {
		return nil
	}
	paths := make(map[string]string, len(d.Skills))
	for _, skill := range d.Skills {
		relative, err := filepath.Rel(d.InstallDir, skill.Path)
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		paths[skill.Name] = filepath.ToSlash(relative)
	}
	return paths
}

// checkSkillNames refuses a candidate whose skill names clash with another
// installed plugin's skills or, through the guard, with the Skills folder.
func (m *Manager) checkSkillNames(d PluginDescriptor) error {
	if len(d.Skills) == 0 {
		return nil
	}
	names := make([]string, 0, len(d.Skills))
	for _, skill := range d.Skills {
		names = append(names, skill.Name)
	}
	installed, err := m.store.List()
	if err != nil {
		return err
	}
	for _, other := range installed {
		if other.Name == d.Name {
			continue
		}
		for _, name := range names {
			for _, taken := range other.Skills {
				if strings.EqualFold(name, taken) {
					return fmt.Errorf("%w: a skill named %q is already provided by the plugin %q", ErrSkillNameTaken, name, other.Name)
				}
			}
		}
	}
	if m.skillNameGuard != nil {
		if err := m.skillNameGuard(d.Name, names); err != nil {
			return err
		}
	}
	return nil
}
