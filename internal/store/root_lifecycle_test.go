package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/systemassistant"
)

func TestSwitchingRootShowsTheNewRootsAgentsAndKeepsTheAssistant(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	if err := c.CreateAgent(systemassistant.CanonicalName, &CreateAgentConfig{}); err != nil {
		t.Fatalf("assistant: %v", err)
	}
	if err := c.CreateAgent("FirstRootAgent", &CreateAgentConfig{}); err != nil {
		t.Fatalf("first root agent: %v", err)
	}

	second := filepath.Join(filepath.Dir(f.root), "Second Root")
	agentDir := filepath.Join(second, "Agents", "SecondRootAgent")
	if err := os.MkdirAll(agentDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "agent_settings.json"), []byte(`{"Settings":{"model":"m"}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	c.SetRoot(second)
	names := strings.Join(c.ListAgents(), ",")
	if names != systemassistant.CanonicalName+",SecondRootAgent" {
		t.Fatalf("after switching, ListAgents = %s", names)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", "FirstRootAgent")); err != nil {
		t.Errorf("switching moved or removed the first root's agent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, "Agents", "FirstRootAgent")); !os.IsNotExist(err) {
		t.Error("switching copied an agent into the new root")
	}
	if err := c.CreateAgent("Newcomer", &CreateAgentConfig{}); err != nil {
		t.Fatalf("create after switching: %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, "Agents", "Newcomer")); err != nil {
		t.Errorf("a new agent did not go to the new root: %v", err)
	}
}

// An agent synced from another machine may name an MCP server this machine
// does not have. The store neither fails on it nor rewrites the sidecar.
func TestASidecarNamingAnUninstalledMCPServerIsLeftAlone(t *testing.T) {
	f := newCompositeFixture(t)
	dir := filepath.Join(f.root, "Agents", "Traveller")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent_settings.json"), []byte(`{"Settings":{"model":"m"}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sidecar := filepath.Join(dir, "mcp_servers.json")
	body := `{"enabled_servers":["only-on-the-other-machine"]}`
	if err := os.WriteFile(sidecar, []byte(body), 0o600); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	backdate(t, sidecar)

	c := f.open(t)
	if _, ok := c.GetAgent("Traveller"); !ok {
		t.Fatal("the agent did not load")
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertUntouched(t, sidecar)
	if data, _ := os.ReadFile(sidecar); string(data) != body {
		t.Errorf("the sidecar was rewritten: %s", data)
	}
}

func TestReloadRootPicksUpAnEditMadeOutsideOri(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	editOnDisk(t, filepath.Join(f.root, "Agents", "Scout", "agent_settings.json"), setEditedPrompt)

	c.ReloadRoot()
	got, ok := c.GetAgent("Scout")
	if !ok || got.Settings.SystemPrompt != "edited in a text editor" {
		t.Fatalf("after ReloadRoot the prompt is %+v, want the edit on disk", got)
	}
}
