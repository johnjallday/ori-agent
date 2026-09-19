package skills

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The user's agents live in the Workspace Directory, so an agent's skill state
// and its own skills follow the folder the agent store reports, not the
// data-dir folder beside agents.json.
func TestSkillStateFollowsTheAgentsOwnFolder(t *testing.T) {
	dataDir := t.TempDir()
	agentStorePath := filepath.Join(dataDir, "agents.json")
	writeTestSkill(t, filepath.Join(dataDir, "agents", "skills", "skill-a"), "skill-a", "Skill A", "Prompt A")

	rootAgent := filepath.Join(t.TempDir(), "Ori Workspaces", "Agents", "Scout")
	writeTestSkill(t, filepath.Join(rootAgent, "skills", "scout-only"), "scout-only", "Scout's own", "Scout prompt")

	manager := NewManager(ManagerConfig{AgentStorePath: agentStorePath})
	manager.SetAgentFolderResolver(func(name string) (string, bool) {
		switch name {
		case "Scout":
			return rootAgent, true
		case "Stranger":
			return "", true // only a workspace holds it
		}
		return "", false
	})

	if err := manager.SetSkillEnabled("Scout", "skill-a", true); err != nil {
		t.Fatalf("SetSkillEnabled: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootAgent, "skills_state.json")); err != nil {
		t.Errorf("skill state was not written in the agent's folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "agents", "Scout")); !os.IsNotExist(err) {
		t.Error("skill state was written beside agents.json instead")
	}

	listed, err := manager.ListSkills("Scout")
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	found := map[string]Skill{}
	for _, skill := range listed {
		found[skill.Name] = skill
	}
	if _, ok := found["scout-only"]; !ok {
		t.Errorf("the agent's own skill in its folder was not found: %v", listed)
	}
	if !found["skill-a"].Enabled {
		t.Errorf("skill-a is not enabled for Scout: %+v", found["skill-a"])
	}

	if err := manager.SetSkillEnabled("Stranger", "skill-a", true); !errors.Is(err, ErrNoAgentFolder) {
		t.Errorf("a workspace-only agent: err = %v, want ErrNoAgentFolder", err)
	}
	if _, err := manager.ListSkills("Stranger"); err != nil {
		t.Errorf("listing skills for a workspace-only agent must not fail: %v", err)
	}
}

// A definition synced from another machine can name a skill this machine does
// not have. It is ignored at run time and the state file is left as it is.
func TestAnUninstalledSkillInTheStateFileIsIgnoredAndKept(t *testing.T) {
	dataDir := t.TempDir()
	agentStorePath := filepath.Join(dataDir, "agents.json")
	writeTestSkill(t, filepath.Join(dataDir, "agents", "skills", "skill-a"), "skill-a", "Skill A", "Prompt A")
	statePath := filepath.Join(dataDir, "agents", "Scout", "skills_state.json")
	state := `{"skills":{"skill-a":{"enabled":true},"not-installed-here":{"enabled":true}}}`
	if err := os.MkdirAll(filepath.Dir(statePath), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(state), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	enabled, err := NewManager(ManagerConfig{AgentStorePath: agentStorePath}).ListEnabledSkillsWithPrompts("Scout")
	if err != nil {
		t.Fatalf("ListEnabledSkillsWithPrompts: %v", err)
	}
	if len(enabled) != 1 || enabled[0].Name != "skill-a" {
		t.Errorf("enabled = %+v, want only the installed skill-a", enabled)
	}
	if data, _ := os.ReadFile(statePath); string(data) != state {
		t.Errorf("reading skills rewrote the state file: %s", data)
	}
}
