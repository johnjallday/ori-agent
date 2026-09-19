// Package pluginhttp wires the plugin installer (internal/plugin) to Ori's live
// MCP and skills managers and exposes plugin operations over HTTP.
package pluginhttp

import (
	"errors"
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

func (s *skillDirInstaller) InstallSkill(pluginName, skillName, srcDir string) error {
	if s.skillsDir == "" {
		return fmt.Errorf("pluginhttp: no skills directory configured")
	}
	if !plugin.ValidSkillOwnershipSegment(pluginName) || !plugin.ValidSkillOwnershipSegment(skillName) {
		return fmt.Errorf("pluginhttp: install skill %q: %w", skillName, plugin.ErrSkillDestinationConflict)
	}
	if err := os.MkdirAll(s.skillsDir, 0o750); err != nil {
		return fmt.Errorf("pluginhttp: prepare skills directory: %w", err)
	}
	digest, err := plugin.SkillTreeDigest(srcDir)
	if err != nil {
		return fmt.Errorf("pluginhttp: inspect skill %q: %w", skillName, err)
	}
	destination := filepath.Join(s.skillsDir, skillName)
	if _, statErr := os.Lstat(destination); statErr == nil {
		if verifyErr := plugin.VerifySkillOwnership(destination, pluginName, skillName); verifyErr != nil {
			return fmt.Errorf("pluginhttp: install skill %q: %w", skillName, verifyErr)
		}
		existingDigest, digestErr := plugin.SkillTreeDigest(destination)
		if digestErr != nil || existingDigest != digest {
			return fmt.Errorf("pluginhttp: install skill %q: %w", skillName, plugin.ErrSkillDestinationConflict)
		}
		return nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("pluginhttp: inspect skill %q destination: %w", skillName, statErr)
	}

	staging, err := os.MkdirTemp(s.skillsDir, ".ori-skill-stage-")
	if err != nil {
		return fmt.Errorf("pluginhttp: stage skill %q: %w", skillName, err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	published := filepath.Join(staging, "skill")
	if err := copyDir(srcDir, published); err != nil {
		return fmt.Errorf("pluginhttp: install skill %q: %w", skillName, err)
	}
	stagedDigest, err := plugin.SkillTreeDigest(published)
	if err != nil || stagedDigest != digest {
		return fmt.Errorf("pluginhttp: install skill %q: %w", skillName, plugin.ErrSkillOwnershipChanged)
	}
	if err := plugin.WriteSkillOwnershipReceipt(published, plugin.NewSkillOwnershipReceipt(pluginName, skillName, digest)); err != nil {
		return fmt.Errorf("pluginhttp: record skill %q ownership: %w", skillName, err)
	}
	if err := renameNoReplace(published, destination); err != nil {
		if _, statErr := os.Lstat(destination); statErr == nil {
			return fmt.Errorf("pluginhttp: install skill %q: %w", skillName, plugin.ErrSkillDestinationConflict)
		}
		return fmt.Errorf("pluginhttp: publish skill %q: %w", skillName, err)
	}
	return nil
}

func (s *skillDirInstaller) VerifySkill(pluginName, skillName string) error {
	if s.skillsDir == "" {
		return nil
	}
	if !plugin.ValidSkillOwnershipSegment(pluginName) || !plugin.ValidSkillOwnershipSegment(skillName) {
		return plugin.ErrSkillDestinationConflict
	}
	return plugin.VerifySkillOwnership(filepath.Join(s.skillsDir, skillName), pluginName, skillName)
}

type skillRollbackSnapshot struct {
	installer  *skillDirInstaller
	pluginName string
	root       string
	paths      map[string]string
}

func (s *skillDirInstaller) PrepareSkillRollback(pluginName string, skillNames []string) (plugin.SkillRollbackSnapshot, error) {
	if s.skillsDir == "" || !plugin.ValidSkillOwnershipSegment(pluginName) {
		return nil, plugin.ErrSkillDestinationConflict
	}
	if err := os.MkdirAll(s.skillsDir, 0o750); err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(s.skillsDir, ".ori-skill-rollback-")
	if err != nil {
		return nil, err
	}
	snapshot := &skillRollbackSnapshot{installer: s, pluginName: pluginName, root: root, paths: make(map[string]string, len(skillNames))}
	fail := func(err error) (plugin.SkillRollbackSnapshot, error) {
		_ = snapshot.Discard()
		return nil, err
	}
	for _, skillName := range skillNames {
		if !plugin.ValidSkillOwnershipSegment(skillName) {
			return fail(plugin.ErrSkillDestinationConflict)
		}
		if _, duplicate := snapshot.paths[skillName]; duplicate {
			return fail(plugin.ErrSkillDestinationConflict)
		}
		destination := filepath.Join(s.skillsDir, skillName)
		if err := plugin.VerifySkillOwnership(destination, pluginName, skillName); err != nil {
			return fail(err)
		}
		if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fail(err)
		}
		receipt, managed, err := plugin.ReadSkillOwnershipReceipt(destination)
		if err != nil || !managed {
			return fail(plugin.ErrSkillDestinationConflict)
		}
		backup := filepath.Join(root, skillName)
		if err := copyDir(destination, backup); err != nil {
			return fail(err)
		}
		digest, err := plugin.SkillTreeDigest(backup)
		if err != nil || digest != receipt.TreeDigest {
			return fail(plugin.ErrSkillOwnershipChanged)
		}
		if err := plugin.WriteSkillOwnershipReceipt(backup, receipt); err != nil {
			return fail(err)
		}
		if err := plugin.VerifySkillOwnership(backup, pluginName, skillName); err != nil {
			return fail(err)
		}
		// Refuse a source tree changed while it was copied instead of keeping a
		// rollback image that was never one verified installed generation.
		if err := plugin.VerifySkillOwnership(destination, pluginName, skillName); err != nil {
			return fail(err)
		}
		snapshot.paths[skillName] = backup
	}
	return snapshot, nil
}

func (snapshot *skillRollbackSnapshot) Restore(skillNames []string) error {
	if snapshot == nil || snapshot.installer == nil {
		return plugin.ErrSkillDestinationConflict
	}
	for _, skillName := range skillNames {
		backup, ok := snapshot.paths[skillName]
		if !ok {
			continue
		}
		destination := filepath.Join(snapshot.installer.skillsDir, skillName)
		if _, err := os.Lstat(destination); err == nil {
			if err := snapshot.installer.RemoveSkill(snapshot.pluginName, skillName); err != nil {
				return fmt.Errorf("restore skill %q; rollback copy remains at %q: %w", skillName, snapshot.root, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("restore skill %q; rollback copy remains at %q: %w", skillName, snapshot.root, err)
		}
		if err := renameNoReplace(backup, destination); err != nil {
			return fmt.Errorf("restore skill %q; rollback copy remains at %q: %w", skillName, snapshot.root, err)
		}
		delete(snapshot.paths, skillName)
	}
	return nil
}

func (snapshot *skillRollbackSnapshot) Discard() error {
	if snapshot == nil || snapshot.root == "" {
		return nil
	}
	err := os.RemoveAll(snapshot.root)
	if err == nil {
		snapshot.root = ""
		snapshot.paths = nil
	}
	return err
}

func (s *skillDirInstaller) RemoveSkill(pluginName, skillName string) error {
	if s.skillsDir == "" {
		return nil
	}
	if !plugin.ValidSkillOwnershipSegment(pluginName) || !plugin.ValidSkillOwnershipSegment(skillName) {
		return plugin.ErrSkillDestinationConflict
	}
	destination := filepath.Join(s.skillsDir, skillName)
	if err := plugin.VerifySkillOwnership(destination, pluginName, skillName); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	quarantineRoot, err := os.MkdirTemp(s.skillsDir, ".ori-skill-remove-")
	if err != nil {
		return err
	}
	quarantine := filepath.Join(quarantineRoot, skillName)
	cleanup := false
	defer func() {
		if cleanup {
			_ = os.RemoveAll(quarantineRoot)
		}
	}()
	if err := os.Rename(destination, quarantine); err != nil {
		cleanup = true
		return err
	}
	if err := plugin.VerifySkillOwnership(quarantine, pluginName, skillName); err != nil {
		if restoreErr := renameNoReplace(quarantine, destination); restoreErr != nil {
			return fmt.Errorf("pluginhttp: skill changed during removal and remains at %q: %w", quarantineRoot, err)
		}
		cleanup = true
		return err
	}
	cleanup = true
	return os.RemoveAll(quarantineRoot)
}

var _ plugin.SkillInstaller = (*skillDirInstaller)(nil)
var _ plugin.SkillRollbackSnapshotter = (*skillDirInstaller)(nil)

// copyDir recursively copies src into dst.
func copyDir(src, dst string) error {
	sourceRoot, err := os.OpenRoot(src)
	if err != nil {
		return err
	}
	defer func() { _ = sourceRoot.Close() }()
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if rel == plugin.SkillOwnershipFileName {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return plugin.ErrSkillDestinationConflict
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		in, openErr := sourceRoot.Open(rel)
		if openErr != nil {
			return openErr
		}
		openedInfo, statErr := in.Stat()
		if statErr != nil || !openedInfo.Mode().IsRegular() {
			_ = in.Close()
			return plugin.ErrSkillDestinationConflict
		}
		return copyFile(in, target, info.Mode().Perm())
	})
}

func copyFile(in *os.File, dst string, mode os.FileMode) error {
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) // #nosec G304 -- destination within the managed skills dir
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
