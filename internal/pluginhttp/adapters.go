// Package pluginhttp wires the plugin installer (internal/plugin) to Ori's live
// MCP and skills managers and exposes plugin operations over HTTP.
package pluginhttp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

// mcpRegistrar adapts Ori's MCP config manager (persistence) + runtime registry
// to plugin.MCPRegistrar, registering to both — the same pair the MCP import
// path uses (configManager.AddServer + registry.AddServer).
type mcpRegistrar struct {
	config   *mcp.ConfigManager
	registry *mcp.Registry
}

func newMCPRegistrar(config *mcp.ConfigManager, registry *mcp.Registry) *mcpRegistrar {
	return &mcpRegistrar{config: config, registry: registry}
}

func (a *mcpRegistrar) AddServer(cfg mcp.ServerConfig) error {
	if err := a.config.AddServer(cfg); err != nil {
		return err
	}
	if err := a.registry.AddServer(cfg); err != nil {
		_ = a.config.RemoveServer(cfg.Name) // keep persisted + runtime in sync
		return err
	}
	return nil
}

func (a *mcpRegistrar) RemoveServer(name string) error {
	rerr := a.registry.RemoveServer(name)
	if cerr := a.config.RemoveServer(name); cerr != nil {
		return cerr
	}
	return rerr
}

var _ plugin.MCPRegistrar = (*mcpRegistrar)(nil)

// skillDirInstaller adapts a skills directory that Ori already scans (e.g.
// ~/.agents/skills) to plugin.SkillInstaller by copying a plugin's skill folder
// into it; the skills manager discovers it on its next scan.
type skillDirInstaller struct {
	skillsDir string
}

func newSkillDirInstaller(skillsDir string) *skillDirInstaller {
	return &skillDirInstaller{skillsDir: skillsDir}
}

func (s *skillDirInstaller) InstallSkill(_, skillName, srcDir string) error {
	if s.skillsDir == "" {
		return fmt.Errorf("pluginhttp: no skills directory configured")
	}
	if err := copyDir(srcDir, filepath.Join(s.skillsDir, skillName)); err != nil {
		return fmt.Errorf("pluginhttp: install skill %q: %w", skillName, err)
	}
	return nil
}

func (s *skillDirInstaller) RemoveSkill(_, skillName string) error {
	if s.skillsDir == "" {
		return nil
	}
	return os.RemoveAll(filepath.Join(s.skillsDir, skillName))
}

var _ plugin.SkillInstaller = (*skillDirInstaller)(nil)

// copyDir recursively copies src into dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	in, err := os.Open(src) // #nosec G304 -- copying a user-installed plugin's own files
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) // #nosec G304 -- destination within the managed skills dir
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
