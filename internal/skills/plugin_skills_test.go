package skills

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Plugin skills are read in place from each plugin's install folder, through
// a provider the server builds from the enabled installed plugins.

type pluginSkillsFixture struct {
	base    string
	skills  []PluginSkill
	manager *Manager
}

func newPluginSkillsFixture(t *testing.T) *pluginSkillsFixture {
	t.Helper()
	base := t.TempDir()
	agentStorePath := filepath.Join(base, "data", "agents.json")
	if err := os.MkdirAll(filepath.Dir(agentStorePath), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f := &pluginSkillsFixture{base: base}
	f.manager = NewManager(ManagerConfig{
		AgentStorePath:    agentStorePath,
		PersonalSkillsDir: filepath.Join(base, "Ori Workspaces", "Skills"),
	})
	f.manager.SetPluginSkillProvider(func(string) []PluginSkill { return f.skills })
	return f
}

// install bundles a skill in a plugin's install folder and makes it available.
func (f *pluginSkillsFixture) install(t *testing.T, pluginName, skillName string) string {
	t.Helper()
	dir := filepath.Join(f.base, "plugins", "src", pluginName, "skills", skillName)
	writeTestSkill(t, dir, skillName, "From "+pluginName, "Plugin prompt")
	f.skills = append(f.skills, PluginSkill{Plugin: pluginName, Name: skillName, Dir: dir})
	return dir
}

func TestPluginSkillsAreReadFromTheInstallFolder(t *testing.T) {
	f := newPluginSkillsFixture(t)
	dir := f.install(t, "reaper", "reaper-mixing")

	listed, err := f.manager.ListSkills("Scout")
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(listed) != 1 || listed[0].Source != SourcePlugin || listed[0].Plugin != "reaper" ||
		listed[0].Path != filepath.Join(dir, "SKILL.md") {
		t.Fatalf("listed = %+v", listed)
	}
	skill, found, err := f.manager.ResolveSkillByName("reaper-mixing")
	if err != nil || !found || skill.Prompt != "Plugin prompt" {
		t.Fatalf("ResolveSkillByName = %+v, %v, %v", skill, found, err)
	}
	skill, found, err = f.manager.ResolvePluginSkillByName("REAPER-MIXING")
	if err != nil || !found || skill.Source != SourcePlugin {
		t.Fatalf("ResolvePluginSkillByName = %+v, %v, %v", skill, found, err)
	}
}

func TestAPluginSkillDisappearsWhenTheProviderStopsListingIt(t *testing.T) {
	f := newPluginSkillsFixture(t)
	f.install(t, "reaper", "reaper-mixing")
	f.skills = nil // the plugin was disabled or uninstalled

	listed, err := f.manager.ListSkills("Scout")
	if err != nil || len(listed) != 0 {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	if _, found, err := f.manager.ResolvePluginSkillByName("reaper-mixing"); err != nil || found {
		t.Fatalf("ResolvePluginSkillByName found a disabled plugin's skill: %v %v", found, err)
	}
}

func TestAPluginSkillClashingWithTheSkillsFolderIsReported(t *testing.T) {
	f := newPluginSkillsFixture(t)
	f.install(t, "reaper", "reaper-mixing")
	writeTestSkill(t, filepath.Join(f.base, "Ori Workspaces", "Skills", "reaper-mixing"), "reaper-mixing", "Mine", "Mine")

	_, err := f.manager.ListSkills("Scout")
	var conflictErr *SkillConflictError
	if !errors.As(err, &conflictErr) || len(conflictErr.Conflicts) != 1 || conflictErr.Conflicts[0].Name != "reaper-mixing" {
		t.Fatalf("ListSkills err = %v, want a reaper-mixing SkillConflictError", err)
	}
	if got := conflictErr.Conflicts[0].Sources; len(got) != 2 || got[0] != SourcePersonal || got[1] != SourcePlugin {
		t.Errorf("conflict sources = %v", got)
	}
	if _, found, err := f.manager.ResolvePluginSkillByName("reaper-mixing"); !errors.Is(err, ErrSkillSourceConflict) || found {
		t.Fatalf("ResolvePluginSkillByName = %v, %v, want ErrSkillSourceConflict", found, err)
	}
	if _, found, err := f.manager.ResolveSkillByName("reaper-mixing"); !errors.Is(err, ErrSkillSourceConflict) || found {
		t.Fatalf("ResolveSkillByName = %v, %v, want ErrSkillSourceConflict", found, err)
	}
}

func TestAPluginSkillClashingWithAnAgentSkillIsReported(t *testing.T) {
	f := newPluginSkillsFixture(t)
	f.install(t, "reaper", "reaper-mixing")
	writeTestSkill(t, filepath.Join(f.base, "data", "agents", "Scout", "skills", "reaper-mixing"), "reaper-mixing", "Agent", "Agent")

	var conflictErr *SkillConflictError
	if _, err := f.manager.ListSkills("Scout"); !errors.As(err, &conflictErr) {
		t.Fatalf("ListSkills err = %v, want a SkillConflictError", err)
	}
	if listed, err := f.manager.ListSkills("Other"); err != nil || len(listed) != 1 {
		t.Fatalf("another agent's list = %+v, %v", listed, err)
	}
}

func TestTwoPluginsWithOneSkillNameClash(t *testing.T) {
	f := newPluginSkillsFixture(t)
	f.install(t, "first", "shared")
	f.install(t, "second", "shared")

	var conflictErr *SkillConflictError
	if _, err := f.manager.ListSkills(""); !errors.As(err, &conflictErr) {
		t.Fatalf("ListSkills err = %v, want a SkillConflictError", err)
	}
	if _, found, err := f.manager.ResolvePluginSkillByName("shared"); err != nil || found {
		t.Fatalf("an ambiguous plugin skill resolved: %v %v", found, err)
	}
}

func TestTheProviderIsAskedForTheAgent(t *testing.T) {
	f := newPluginSkillsFixture(t)
	dir := f.install(t, "reaper", "reaper-mixing")
	f.manager.SetPluginSkillProvider(func(agentName string) []PluginSkill {
		if agentName == "Held" {
			return nil // a role whose reviewed provider changed
		}
		return []PluginSkill{{Plugin: "reaper", Name: "reaper-mixing", Dir: dir}}
	})

	if listed, err := f.manager.ListSkills("Held"); err != nil || len(listed) != 0 {
		t.Fatalf("Held = %+v, %v", listed, err)
	}
	if listed, err := f.manager.ListSkills("Scout"); err != nil || len(listed) != 1 {
		t.Fatalf("Scout = %+v, %v", listed, err)
	}
}
