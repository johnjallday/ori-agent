package plugin

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// SkillLocation is one skill an installed plugin bundles, read in place from
// its folder inside the plugin's install folder.
type SkillLocation struct {
	Plugin string
	Name   string
	Dir    string
}

// skillDirCache remembers skill folders worked out from the manifest of a
// record that has no SkillPaths, keyed by the record's identity, so a skills
// listing does not parse manifests every time.
type skillDirCache struct {
	mu      sync.Mutex
	entries map[string]map[string]string
}

func (c *skillDirCache) get(key string) (map[string]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dirs, ok := c.entries[key]
	return dirs, ok
}

func (c *skillDirCache) put(key string, dirs map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]map[string]string{}
	}
	c.entries[key] = dirs
}

// Installed reads the installed-plugin registry directly, without waiting for
// an install or update in progress to finish. Skill listings use it so a long
// git clone never holds up a chat.
func (m *Manager) Installed() ([]InstalledPlugin, error) {
	return m.store.List()
}

// EnabledSkills lists the skills of every enabled plugin in installed, each
// read in place from the plugin's install folder.
func (m *Manager) EnabledSkills(installed []InstalledPlugin) []SkillLocation {
	var locations []SkillLocation
	for _, record := range installed {
		if !record.Enabled || len(record.Skills) == 0 {
			continue
		}
		dirs := m.skillDirsOf(record)
		for _, name := range record.Skills {
			if dir := dirs[name]; dir != "" {
				locations = append(locations, SkillLocation{Plugin: record.Name, Name: name, Dir: dir})
			}
		}
	}
	return locations
}

// skillDirsOf returns each recorded skill's absolute folder. A folder must lie
// inside the install folder; anything else is left out.
func (m *Manager) skillDirsOf(record InstalledPlugin) map[string]string {
	root, err := canonicalInstallRoot(record.InstallDir, record.Source, m.cloneDir)
	if err != nil {
		return nil
	}
	if len(record.SkillPaths) > 0 {
		dirs := make(map[string]string, len(record.SkillPaths))
		for name, relative := range record.SkillPaths {
			relative = filepath.FromSlash(strings.TrimSpace(relative))
			if !filepath.IsLocal(relative) {
				continue
			}
			dirs[name] = filepath.Join(root, relative)
		}
		return dirs
	}
	key := fmt.Sprintf("%s\x00%s\x00%d", record.Name, root, record.Generation)
	if dirs, ok := m.skillDirs.get(key); ok {
		return dirs
	}
	dirs := map[string]string{}
	if manifest, err := DetectManifest(root, record.Format); err == nil {
		for _, skill := range loadSkills(manifest) {
			relative, relErr := filepath.Rel(root, skill.Path)
			if relErr == nil && filepath.IsLocal(relative) {
				dirs[skill.Name] = skill.Path
			}
		}
	}
	m.skillDirs.put(key, dirs)
	return dirs
}
