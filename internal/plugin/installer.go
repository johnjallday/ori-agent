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

// SkillOwnershipVerifier is implemented by installers that can prove a shared
// destination is still the exact plugin-owned copy before a lifecycle operation
// mutates any other component.
type SkillOwnershipVerifier interface {
	VerifySkill(pluginName, skillName string) error
}

// SkillRollbackSnapshot holds immutable copies of the currently installed
// skill bytes while an update replaces shared personal-skill destinations.
// Restore is intentionally scoped to destinations actually removed before a
// failure; Discard releases the private staging area after commit or rollback.
type SkillRollbackSnapshot interface {
	Restore(skillNames []string) error
	Discard() error
}

// SkillRollbackSnapshotter is implemented by installers that publish skills to
// shared mutable storage. The lifecycle manager uses it to restore the exact
// receipt-owned bytes rather than re-reading a source checkout that an update
// may already have changed.
type SkillRollbackSnapshotter interface {
	PrepareSkillRollback(pluginName string, skillNames []string) (SkillRollbackSnapshot, error)
}

func verifyOwnedSkills(installer SkillInstaller, pluginName string, skills []string) error {
	verifier, ok := installer.(SkillOwnershipVerifier)
	if !ok {
		return nil
	}
	for _, skill := range skills {
		if err := verifier.VerifySkill(pluginName, skill); err != nil {
			return fmt.Errorf("plugin %q: verify skill %q ownership: %w", pluginName, skill, err)
		}
	}
	return nil
}

func prepareSkillRollback(installer SkillInstaller, pluginName string, skills []string) (SkillRollbackSnapshot, error) {
	if len(skills) == 0 {
		return nil, nil
	}
	snapshotter, ok := installer.(SkillRollbackSnapshotter)
	if !ok {
		return nil, nil
	}
	snapshot, err := snapshotter.PrepareSkillRollback(pluginName, skills)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: preserve installed skills for rollback: %w", pluginName, err)
	}
	return snapshot, nil
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
