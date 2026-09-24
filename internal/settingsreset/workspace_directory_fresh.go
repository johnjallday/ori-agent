package settingsreset

// Start Fresh's reach into the Workspace Directory beyond the agents folder:
// the Skills folder and the plugin list (Plugins.json) beside it. Both are
// removed only when the reset removes the agents folder, under the same
// second confirmation, and they are derived from that reviewed folder rather
// than recorded independently, so a receipt cannot name anywhere else.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

// maxResetSkillEntries bounds one Skills folder reset.
const maxResetSkillEntries = 4096

// siblingFreshTargets are the Skills folder and plugin list beside a reviewed
// agents folder, whether or not they exist.
func siblingFreshTargets(agentsFolder string) (skillsFolder, pluginList string) {
	root := filepath.Dir(agentsFolder)
	return filepath.Join(root, config.SkillsFolderName), filepath.Join(root, plugin.PluginListFileName)
}

// workspaceDirectoryFreshTargets returns the Skills folder and plugin list
// Start Fresh removes, each only when it is there to remove: a real folder
// and a regular file, never a link.
func workspaceDirectoryFreshTargets(agentsFolder string) (skillsFolder, pluginList string) {
	skills, list := siblingFreshTargets(agentsFolder)
	if info, err := os.Lstat(skills); err == nil && info.IsDir() {
		skillsFolder = skills
	}
	if info, err := os.Lstat(list); err == nil && info.Mode().IsRegular() {
		pluginList = list
	}
	return skillsFolder, pluginList
}

// validFreshSiblings reports whether recorded Skills and plugin-list evidence
// is exactly what the recorded agents folder implies.
func validFreshSiblings(evidence resolvedEvidence) bool {
	if evidence.SkillsFolder == "" && evidence.PluginList == "" {
		return true
	}
	if evidence.AgentsFolder == "" {
		return false
	}
	skills, list := siblingFreshTargets(evidence.AgentsFolder)
	return (evidence.SkillsFolder == "" || evidence.SkillsFolder == skills) &&
		(evidence.PluginList == "" || evidence.PluginList == list)
}

// resetRootSkills removes each skill folder (one holding a SKILL.md) from the
// Skills folder, through an os.Root confined to it. Links and other files are
// kept. A missing folder has nothing to remove.
func resetRootSkills(skillsFolder string) (int, error) {
	root, err := os.OpenRoot(skillsFolder)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open the Skills folder: %w", err)
	}
	defer func() { _ = root.Close() }()
	directory, err := root.Open(".")
	if err != nil {
		return 0, fmt.Errorf("inspect the Skills folder: %w", err)
	}
	entries, readErr := directory.ReadDir(maxResetSkillEntries + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return 0, fmt.Errorf("list the Skills folder: %w", readErr)
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if len(entries) > maxResetSkillEntries {
		return 0, errors.New("the Skills folder reset exceeds the bounded entry limit")
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !filepath.IsLocal(entry.Name()) {
			continue
		}
		if info, err := root.Lstat(filepath.Join(entry.Name(), "SKILL.md")); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			return removed, fmt.Errorf("remove from the Skills folder: %w", err)
		}
		removed++
	}
	return removed, nil
}

// resetPluginList removes the plugin list when it is a regular file.
func resetPluginList(pluginList string) error {
	info, err := os.Lstat(pluginList)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("the plugin list is not a regular file")
	}
	return os.Remove(pluginList)
}
