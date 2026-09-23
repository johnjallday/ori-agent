package server

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/skills"
)

// pluginSkillsBuilder wires the skills manager and plugin handler the way the
// server does, inside a sandbox: HOME, the data dir, and the Workspace
// Directory are all temporary.
func pluginSkillsBuilder(t *testing.T) (*ServerBuilder, string, string) {
	t.Helper()
	home := t.TempDir()
	dataDir := t.TempDir()
	root := filepath.Join(t.TempDir(), "Ori Workspaces")
	t.Setenv("HOME", home)
	t.Setenv("ORI_DATA_DIR", dataDir)
	t.Setenv("WORKSPACE_DIR", root)

	b := &ServerBuilder{agentStorePath: filepath.Join(dataDir, "agents.json")}
	b.skillsManager = skills.NewManager(skills.ManagerConfig{
		AgentStorePath:            b.agentStorePath,
		PersonalSkillsDirResolver: func() string { return workspaceSkillsDir(nil) },
	})
	b.pluginHandler = pluginhttp.NewHandler(mcp.NewConfigManager(dataDir), mcp.NewRegistry(), filepath.Join(dataDir, "plugins"))
	b.wirePluginSkills()
	return b, home, root
}

// localPluginBundle writes a plugin with one skill and no runnable server.
func localPluginBundle(t *testing.T, skillName string) string {
	t.Helper()
	bundle := filepath.Join(t.TempDir(), "demo-plugin")
	writeFixtureFile(t, filepath.Join(bundle, ".claude-plugin", "plugin.json"), `{"name":"demo-plugin","version":"1.0.0"}`)
	writeFixtureFile(t, filepath.Join(bundle, "skills", skillName, "SKILL.md"),
		"---\nname: "+skillName+"\ndescription: demo skill\n---\nDemo instructions.\n")
	return bundle
}

func skillSources(t *testing.T, manager *skills.Manager) map[string]string {
	t.Helper()
	listed, err := manager.ListSkills("")
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	sources := map[string]string{}
	for _, skill := range listed {
		sources[skill.Name] = skill.Source
	}
	return sources
}

func assertNothingUnder(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err == nil && path != dir {
			t.Errorf("something was written under HOME: %s", path)
		}
		return nil
	})
}

func TestPluginSkillsAreServedFromTheInstallFolderWhileEnabled(t *testing.T) {
	b, home, _ := pluginSkillsBuilder(t)
	manager := b.pluginHandler.Manager()
	bundle := localPluginBundle(t, "demo-skill")

	installed, err := manager.Install(bundle, plugin.FormatClaude, func(plugin.TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if sources := skillSources(t, b.skillsManager); sources["demo-skill"] != "" {
		t.Fatalf("a disabled plugin's skill is listed: %v", sources)
	}
	if err := manager.SetEnabled(installed.Name, true); err != nil {
		t.Fatal(err)
	}
	if sources := skillSources(t, b.skillsManager); sources["demo-skill"] != skills.SourcePlugin {
		t.Fatalf("an enabled plugin's skill is missing: %v", sources)
	}
	skill, found, err := b.skillsManager.ResolvePluginSkillByName("demo-skill")
	if err != nil || !found || skill.Path != filepath.Join(bundle, "skills", "demo-skill", "SKILL.md") {
		t.Fatalf("the skill was not read in place: %+v %v %v", skill, found, err)
	}

	writeFixtureFile(t, filepath.Join(bundle, ".claude-plugin", "plugin.json"), `{"name":"demo-plugin","version":"1.1.0"}`)
	if _, err := manager.Update(installed.Name, func(plugin.TrustReport) bool { return true }); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := manager.SetEnabled(installed.Name, false); err != nil {
		t.Fatal(err)
	}
	if sources := skillSources(t, b.skillsManager); sources["demo-skill"] != "" {
		t.Fatalf("a disabled plugin's skill is still listed: %v", sources)
	}
	if err := manager.SetEnabled(installed.Name, true); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(installed.Name); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if sources := skillSources(t, b.skillsManager); sources["demo-skill"] != "" {
		t.Fatalf("an uninstalled plugin's skill is still listed: %v", sources)
	}
	assertNothingUnder(t, home)
}

func TestInstallingAPluginWhoseSkillIsInTheSkillsFolderIsRefused(t *testing.T) {
	b, home, root := pluginSkillsBuilder(t)
	writeFixtureFile(t, filepath.Join(config.RootSkillsDir(root), "demo-skill", "SKILL.md"), "---\nname: demo-skill\n---\nMine.\n")

	_, err := b.pluginHandler.Manager().Install(localPluginBundle(t, "demo-skill"), plugin.FormatClaude, func(plugin.TrustReport) bool { return true })
	if !errors.Is(err, plugin.ErrSkillNameTaken) || err.Error() != `plugin skill name is already in use: a skill named "demo-skill" is already in your Skills folder` {
		t.Fatalf("install err = %v", err)
	}
	if records, _ := b.pluginHandler.Manager().Installed(); len(records) != 0 {
		t.Fatalf("the refused plugin was recorded: %+v", records)
	}
	assertNothingUnder(t, home)
}

func TestTheLegacyCleanupRunsOnceWhenPluginSkillsAreWired(t *testing.T) {
	_, _, _ = pluginSkillsBuilder(t)
	if _, err := os.Stat(filepath.Join(config.DefaultDataDir(), plugin.LegacySkillCleanupMarkerName)); err != nil {
		t.Fatalf("the one-time cleanup left no marker: %v", err)
	}
}
