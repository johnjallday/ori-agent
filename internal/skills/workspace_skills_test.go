package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Installed skills live in <Workspace Directory>/Skills. The manager asks for
// that folder on every read, so it follows the current root.

type rootSkillsFixture struct {
	agentStorePath string
	root           string
	manager        *Manager
}

func newRootSkillsFixture(t *testing.T) *rootSkillsFixture {
	t.Helper()
	base := t.TempDir()
	f := &rootSkillsFixture{
		agentStorePath: filepath.Join(base, "data", "agents.json"),
		root:           filepath.Join(base, "Ori Workspaces"),
	}
	if err := os.MkdirAll(filepath.Dir(f.agentStorePath), 0o750); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	f.manager = NewManager(ManagerConfig{
		AgentStorePath:            f.agentStorePath,
		PersonalSkillsDirResolver: func() string { return filepath.Join(f.root, "Skills") },
	})
	return f
}

func skillNames(skills []Skill) map[string]string {
	names := make(map[string]string, len(skills))
	for _, skill := range skills {
		names[skill.Name] = skill.Source
	}
	return names
}

func TestSkillsLoadFromTheWorkspaceDirectorySkillsFolder(t *testing.T) {
	f := newRootSkillsFixture(t)
	writeTestSkill(t, filepath.Join(f.root, "Skills", "find-skills"), "find-skills", "Find skills", "Prompt")

	listed, err := f.manager.ListSkills("Scout")
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if got := skillNames(listed); got["find-skills"] != SourcePersonal || len(got) != 1 {
		t.Fatalf("skills = %v, want find-skills from %q", got, SourcePersonal)
	}
	if dir := f.manager.PersonalSkillsDir(); dir != filepath.Join(f.root, "Skills") {
		t.Errorf("PersonalSkillsDir = %q", dir)
	}
	skill, found, err := f.manager.ResolveSkillByName("find-skills")
	if err != nil || !found || skill.Prompt != "Prompt" {
		t.Fatalf("ResolveSkillByName = %+v, %v, %v", skill, found, err)
	}
}

func TestSwitchingTheWorkspaceDirectorySwitchesTheSkills(t *testing.T) {
	f := newRootSkillsFixture(t)
	first := f.root
	second := filepath.Join(filepath.Dir(first), "Other Workspaces")
	writeTestSkill(t, filepath.Join(first, "Skills", "first-skill"), "first-skill", "First", "Prompt")
	writeTestSkill(t, filepath.Join(second, "Skills", "second-skill"), "second-skill", "Second", "Prompt")

	before, err := f.manager.ListSkills("")
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	f.root = second
	after, err := f.manager.ListSkills("")
	if err != nil {
		t.Fatalf("ListSkills after switch: %v", err)
	}
	if got := skillNames(before); len(got) != 1 || got["first-skill"] == "" {
		t.Errorf("before the switch = %v", got)
	}
	if got := skillNames(after); len(got) != 1 || got["second-skill"] == "" {
		t.Errorf("after the switch = %v", got)
	}
	if _, err := os.Stat(filepath.Join(second, "Skills", "first-skill")); !os.IsNotExist(err) {
		t.Error("the switch moved or copied a skill")
	}
}

func TestSkillsInTheHomeAgentsFolderNoLongerAppear(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTestSkill(t, filepath.Join(home, ".agents", "skills", "claude-code-skill"), "claude-code-skill", "Someone else's", "Prompt")
	f := newRootSkillsFixture(t)
	writeTestSkill(t, filepath.Join(f.root, "Skills", "mine"), "mine", "Mine", "Prompt")

	listed, err := f.manager.ListSkills("Scout")
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if got := skillNames(listed); got["claude-code-skill"] != "" || got["mine"] == "" {
		t.Fatalf("skills = %v, want only the Skills folder", got)
	}
	if _, found, err := f.manager.ResolveSkillByName("claude-code-skill"); err != nil || found {
		t.Fatalf("ResolveSkillByName found a ~/.agents skill: %v %v", found, err)
	}
}

// FR 8: reading skills never writes into the Skills folder, so a synced
// Workspace Directory does not see a change every time Ori starts.
func TestReadingSkillsNeverChangesTheSkillsFolder(t *testing.T) {
	f := newRootSkillsFixture(t)
	skillsDir := filepath.Join(f.root, "Skills")
	writeTestSkill(t, filepath.Join(skillsDir, "find-skills"), "find-skills", "Find skills", "Prompt")
	writeTestSkill(t, filepath.Join(skillsDir, "category", "nested"), "nested", "Nested", "Prompt")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	_ = filepath.WalkDir(skillsDir, func(path string, _ fs.DirEntry, _ error) error {
		return os.Chtimes(path, old, old)
	})
	before := modTimes(t, skillsDir)

	for range 2 { // two "starts"
		manager := NewManager(ManagerConfig{
			AgentStorePath:            f.agentStorePath,
			PersonalSkillsDirResolver: func() string { return skillsDir },
		})
		if _, err := manager.ListSkills("Scout"); err != nil {
			t.Fatalf("ListSkills: %v", err)
		}
		if _, err := manager.ListEnabledSkillsWithPrompts("Scout"); err != nil {
			t.Fatalf("ListEnabledSkillsWithPrompts: %v", err)
		}
		if _, _, err := manager.GetSkill("Scout", "find-skills"); err != nil {
			t.Fatalf("GetSkill: %v", err)
		}
	}

	after := modTimes(t, skillsDir)
	if len(after) != len(before) {
		t.Fatalf("the Skills folder changed: %d entries before, %d after", len(before), len(after))
	}
	for path, stamp := range before {
		if !after[path].Equal(stamp) {
			t.Errorf("%s changed: %v -> %v", path, stamp, after[path])
		}
	}
}

func modTimes(t *testing.T, dir string) map[string]time.Time {
	t.Helper()
	stamps := map[string]time.Time{}
	err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		stamps[path] = info.ModTime()
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return stamps
}
