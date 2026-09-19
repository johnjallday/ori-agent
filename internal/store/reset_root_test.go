package store

import (
	"os"
	"path/filepath"
	"testing"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResetRootAgentsRemovesAgentFoldersOnly(t *testing.T) {
	agents := filepath.Join(t.TempDir(), "Agents")
	mustWrite(t, filepath.Join(agents, "Scout", "agent_settings.json"), `{"Settings":{}}`)
	mustWrite(t, filepath.Join(agents, "Scout", "Scout.png"), "png")
	mustWrite(t, filepath.Join(agents, "Scout", "skills", "notes", "SKILL.md"), "skill")
	mustWrite(t, filepath.Join(agents, "README.txt"), "mine")
	mustWrite(t, filepath.Join(agents, "Drafts", "idea.md"), "not an agent")
	external := t.TempDir()
	mustWrite(t, filepath.Join(external, "agent_settings.json"), `{"Settings":{}}`)
	if err := os.Symlink(external, filepath.Join(agents, "Linked")); err != nil {
		t.Fatal(err)
	}

	removed, err := ResetRootAgents(agents)
	if err != nil || removed != 1 {
		t.Fatalf("ResetRootAgents = %d, %v; want 1 agent", removed, err)
	}
	if _, err := os.Stat(filepath.Join(agents, "Scout")); !os.IsNotExist(err) {
		t.Error("the agent's folder survived")
	}
	for _, kept := range []string{"README.txt", filepath.Join("Drafts", "idea.md"), "Linked"} {
		if _, err := os.Lstat(filepath.Join(agents, kept)); err != nil {
			t.Errorf("%s was not kept: %v", kept, err)
		}
	}
	if _, err := os.Stat(filepath.Join(external, "agent_settings.json")); err != nil {
		t.Error("a linked folder outside Agents was followed")
	}
	if removed, err := ResetRootAgents(filepath.Join(t.TempDir(), "missing")); err != nil || removed != 0 {
		t.Errorf("a missing Agents folder = %d, %v; want nothing to do", removed, err)
	}
}

func TestResetAgentStateRemovesRootKeyFoldersOnly(t *testing.T) {
	state := filepath.Join(t.TempDir(), "agent_state")
	key := RootKey(t.TempDir())
	mustWrite(t, filepath.Join(state, key, "Scout.json"), `{}`)
	mustWrite(t, filepath.Join(state, "notes.txt"), "mine")
	mustWrite(t, filepath.Join(state, "Not-A-Key", "x.json"), `{}`)

	removed, err := ResetAgentState(state)
	if err != nil || removed != 1 {
		t.Fatalf("ResetAgentState = %d, %v; want 1 root", removed, err)
	}
	if _, err := os.Stat(filepath.Join(state, key)); !os.IsNotExist(err) {
		t.Error("runtime state survived")
	}
	for _, kept := range []string{"notes.txt", "Not-A-Key"} {
		if _, err := os.Stat(filepath.Join(state, kept)); err != nil {
			t.Errorf("%s was not kept: %v", kept, err)
		}
	}
}

func TestCompositeReportsEveryAgentResetLocation(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	got := c.ResetLocations()
	want := ResetLocations{
		Index: filepath.Join(f.dataDir, "agents.json"), Profiles: filepath.Join(f.dataDir, "agents"),
		Projection: filepath.Join(f.dataDir, "agents.json"), State: filepath.Join(f.dataDir, "agent_state"),
		RootAgents: filepath.Join(f.root, "Agents"),
	}
	if got != want {
		t.Errorf("ResetLocations = %+v\nwant %+v", got, want)
	}
	system, _, _ := c.PersistencePaths()
	if system != want.Index {
		t.Errorf("PersistencePaths index = %s, want the system store's", system)
	}
}
