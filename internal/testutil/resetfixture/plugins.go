package resetfixture

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

// Synthetic installed-plugin state for destructive reset tests.
//
// Nothing here resolves, downloads, clones, or executes a real plugin: the
// files are inert placeholders written through the fixture's confined root, and
// the manifests are domain neutral. A seeded plugin exists to be described and
// removed, never to run.

// PluginSeed describes one synthetic installed plugin. Managed places its source
// inside the managed clone directory (Ori owns and removes it); otherwise the
// source is an external directory that must survive reset byte-for-byte.
type PluginSeed struct {
	Name       string
	Version    string
	Enabled    bool
	Managed    bool
	MCPServers []string
	Skills     []string
	Surfaces   bool
	Artifacts  bool
}

// UserAuthoredSkill names a skill no plugin installed, in the fixture's
// ~/.agents/skills. It is a byte-checked sentinel: plugin reset must never
// touch that folder at all.
const UserAuthoredSkill = "user-authored-skill"

// PluginResetPaths reports the owner-resolved locations plugin reset operates
// within, derived exactly as production derives them from this fixture's roots.
func (f *Fixture) PluginResetPaths() plugin.ResetPaths {
	return plugin.DefaultResetPaths(f.paths.DataDir)
}

// PersonalSkillsRoot is the fixture's isolated stand-in for ~/.agents/skills,
// where plugin skills used to be copied. The fixture sets HOME, so production
// resolution lands here.
func (f *Fixture) PersonalSkillsRoot() string {
	return filepath.Join(f.paths.Home, ".agents", "skills")
}

// SeedPlugins writes a complete installed registry plus every component the
// seeds declare, and registers the plugins' namespaced MCP entries beside one
// unrelated registration that must be preserved.
func (f *Fixture) SeedPlugins(t testing.TB, seeds ...PluginSeed) []plugin.InstalledPlugin {
	t.Helper()
	paths := f.PluginResetPaths()
	records := make([]plugin.InstalledPlugin, 0, len(seeds))
	servers := []mcp.ServerConfig{{
		Name: "unrelated-user-server", Command: "never-started-by-reset", Transport: "stdio",
	}}
	for _, seed := range seeds {
		record := plugin.InstalledPlugin{
			Name: seed.Name, Version: seed.Version, Format: plugin.FormatClaude,
			Generation: 2, Enabled: seed.Enabled, InstalledAt: time.Unix(0, 0).UTC(),
		}
		if seed.Managed {
			record.Source = "https://example.test/" + seed.Name + ".git"
			record.InstallDir = filepath.Join(paths.CloneDir, seed.Name+"-repo")
		} else {
			record.Source = filepath.Join(f.paths.Root, "external", "plugin-"+seed.Name)
			record.InstallDir = record.Source
		}
		f.mustWrite(t, record.InstallDir, ".claude-plugin", "plugin.json")
		for _, server := range seed.MCPServers {
			namespaced := plugin.NamespacedServerName(seed.Name, server)
			record.MCPServers = append(record.MCPServers, namespaced)
			servers = append(servers, mcp.ServerConfig{
				Name: namespaced, Command: "never-started-by-reset", Transport: "stdio",
			})
		}
		// A plugin's skills live in its own install folder.
		for _, skill := range seed.Skills {
			record.Skills = append(record.Skills, skill)
			if record.SkillPaths == nil {
				record.SkillPaths = map[string]string{}
			}
			record.SkillPaths[skill] = "skills/" + skill
			f.mustWrite(t, filepath.Join(record.InstallDir, "skills", skill), "SKILL.md")
		}
		if seed.Surfaces {
			record.WorkspaceSurfaces = &plugin.SurfaceContribution{}
			// Host-owned namespaced state and the plugin service's own data root
			// are separate directories; both belong to this plugin.
			for _, directory := range pluginStateDirectories(paths, seed.Name) {
				f.mustWrite(t, directory, "state.json")
			}
		}
		if seed.Artifacts {
			record.ResolvedArtifacts = []plugin.ResolvedArtifact{{ServiceID: "svc", Available: true}}
			f.mustWrite(t, filepath.Join(paths.ArtifactsRoot(), seed.Name, "0123456789abcdef", "svc"), "artifact.bin")
		}
		records = append(records, record)
	}
	f.writeJSON(t, paths.RegistryPath(), records)
	f.writeJSON(t, paths.MCPRegistry, mcp.GlobalConfig{Servers: servers})
	f.writeJSON(t, paths.MarketplacesPath(), []map[string]string{{"name": "fixture-marketplace", "source": "https://example.test/market"}})
	f.mustWrite(t, paths.PreviewRoot(), "cached-checkout.json")
	return records
}

// SeedUnreadablePluginRegistry writes a corrupt registry so a test can prove
// reset blocks instead of reporting an empty, safe-looking inventory.
func (f *Fixture) SeedUnreadablePluginRegistry(t testing.TB) {
	t.Helper()
	f.mustWriteContent(t, f.PluginResetPaths().RegistryPath(), []byte("{not a plugin registry"))
}

// pluginStateDirectories mirrors the two managed namespaced-state locations
// without importing the hashing helper, by asking the plugin package to remove
// nothing and instead reusing its documented layout.
func pluginStateDirectories(paths plugin.ResetPaths, name string) []string {
	return []string{
		filepath.Join(paths.StateRoot(), plugin.ResetStateNamespace(name)),
		filepath.Join(paths.StateRoot(), name),
	}
}

func (f *Fixture) mustWrite(t testing.TB, directory string, parts ...string) {
	t.Helper()
	path := filepath.Join(append([]string{directory}, parts...)...)
	f.mustWriteContent(t, path, []byte("reset fixture: synthetic plugin component\n"))
}

func (f *Fixture) mustWriteContent(t testing.TB, path string, data []byte) {
	t.Helper()
	relative, err := filepath.Rel(f.paths.Root, path)
	must(t, err)
	must(t, f.WriteFile(relative, data))
}

func (f *Fixture) writeJSON(t testing.TB, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	must(t, err)
	f.mustWriteContent(t, path, data)
}
