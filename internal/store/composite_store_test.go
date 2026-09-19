package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

type compositeFixture struct {
	dataDir string
	root    string
}

func newCompositeFixture(t *testing.T) compositeFixture {
	t.Helper()
	base := t.TempDir()
	f := compositeFixture{dataDir: filepath.Join(base, "data"), root: filepath.Join(base, "Ori Workspaces")}
	for _, dir := range []string{f.dataDir, f.root} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	return f
}

func (f compositeFixture) open(t *testing.T) *CompositeStore {
	t.Helper()
	defaults := types.Settings{Model: "gpt-4o-mini"}
	system, err := NewFileStore(filepath.Join(f.dataDir, "agents.json"), defaults)
	if err != nil {
		t.Fatalf("system store: %v", err)
	}
	c, err := NewCompositeStore(system, f.root, filepath.Join(f.dataDir, "agent_state"), defaults)
	if err != nil {
		t.Fatalf("NewCompositeStore: %v", err)
	}
	return c
}

func TestCompositeSatisfiesTheStoreInterfaces(t *testing.T) {
	var _ Store = (*CompositeStore)(nil)
	var _ AgentRenamer = (*CompositeStore)(nil)
}

func TestRootStoreNeverWritesAnIndexFile(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", "Scout", "agent_settings.json")); err != nil {
		t.Fatalf("Scout was not written to the root: %v", err)
	}
	for _, index := range []string{filepath.Join(f.root, "agents.json"), filepath.Join(f.root, "Agents", "agents.json")} {
		if _, err := os.Stat(index); !os.IsNotExist(err) {
			t.Errorf("the root holds an index file %s", index)
		}
	}
}

func TestCompositeKeepsTheAssistantInTheDataDir(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	if err := c.CreateAgent(systemassistant.CanonicalName, &CreateAgentConfig{}); err != nil {
		t.Fatalf("create assistant: %v", err)
	}
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("create Scout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "agents", systemassistant.CanonicalName)); err != nil {
		t.Errorf("the assistant is not in the data dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", systemassistant.CanonicalName)); !os.IsNotExist(err) {
		t.Error("the assistant was written into the root")
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "agents", "Scout")); !os.IsNotExist(err) {
		t.Error("a user agent was written into the data dir")
	}
	if got := c.ListAgents(); len(got) != 2 {
		t.Errorf("ListAgents = %v, want the assistant and Scout", got)
	}

	// Reopening finds both where they were written.
	reopened := f.open(t)
	for _, name := range []string{systemassistant.CanonicalName, "Scout"} {
		if _, ok := reopened.GetAgent(name); !ok {
			t.Errorf("%s did not load after a restart", name)
		}
	}
}

func TestCompositeWithAMissingRootHasOnlySystemAgents(t *testing.T) {
	f := newCompositeFixture(t)
	f.root = filepath.Join(f.root, "unmounted-drive")
	c := f.open(t)
	if err := c.CreateAgent(systemassistant.CanonicalName, &CreateAgentConfig{}); err != nil {
		t.Fatalf("the assistant must still be creatable: %v", err)
	}
	err := c.CreateAgent("Scout", &CreateAgentConfig{})
	if !errors.Is(err, ErrAgentRootUnavailable) {
		t.Fatalf("CreateAgent on a missing root: err = %v, want ErrAgentRootUnavailable", err)
	}
	if _, err := os.Stat(f.root); !os.IsNotExist(err) {
		t.Fatal("the store created a missing root")
	}
	if got := c.ListAgents(); len(got) != 1 {
		t.Errorf("ListAgents = %v, want only the assistant", got)
	}
	status := c.RootStatus()
	if status.Available || status.Reason != RootReasonMissing {
		t.Errorf("RootStatus = %+v, want unavailable/missing", status)
	}
}

func TestCompositeCreatesTheAgentsFolderOnlyOnFirstWrite(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	c.ListAgents()
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents")); !os.IsNotExist(err) {
		t.Fatal("the Agents folder was created before any agent was written")
	}
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", "Scout")); err != nil {
		t.Errorf("the first write did not create the agent folder: %v", err)
	}
}

func TestCompositeOpensARootThatAppearsAfterStartup(t *testing.T) {
	f := newCompositeFixture(t)
	f.root = filepath.Join(f.root, "created-later")
	c := f.open(t)
	if err := os.MkdirAll(f.root, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent once the root exists: %v", err)
	}
	if _, ok := c.GetAgent("Scout"); !ok {
		t.Error("Scout is not readable after creation")
	}
}

func TestCompositePrefersTheRootOverALegacyLeftover(t *testing.T) {
	f := newCompositeFixture(t)
	legacy := filepath.Join(f.dataDir, "agents", "Scout")
	if err := os.MkdirAll(legacy, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "agent_settings.json"), []byte(`{"Settings":{"model":"old-copy"}}`), 0o600); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	rootScout := filepath.Join(f.root, "Agents", "Scout")
	if err := os.MkdirAll(rootScout, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootScout, "agent_settings.json"), []byte(`{"Settings":{"model":"root-copy"}}`), 0o600); err != nil {
		t.Fatalf("seed root: %v", err)
	}

	c := f.open(t)
	got, ok := c.GetAgent("Scout")
	if !ok || got.Settings.Model != "root-copy" {
		t.Fatalf("GetAgent(Scout) = %+v, want the root copy", got)
	}
	if names := c.ListAgents(); len(names) != 1 {
		t.Errorf("ListAgents = %v, want Scout once", names)
	}
}

func TestCompositeSkipsFoldersThatAreNotAgents(t *testing.T) {
	f := newCompositeFixture(t)
	for _, dir := range []string{".git", "Notes about agents"} {
		if err := os.MkdirAll(filepath.Join(f.root, "Agents", dir), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	c := f.open(t)
	if names := c.ListAgents(); len(names) != 0 {
		t.Fatalf("ListAgents = %v, want none", names)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", "Notes about agents", "agent_settings.json")); !os.IsNotExist(err) {
		t.Error("Ori wrote a definition into a folder that is not an agent")
	}
}

func TestCompositeKeepsNewAgentsInTheDataDirWhileMigrationIsBlocked(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	c.SetMigrationReport(&RootMigrationReport{Status: MigrationBlockedByWorkspace})
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "agents", "Scout")); err != nil {
		t.Errorf("a blocked migration must keep new agents in the data dir: %v", err)
	}
	if status := c.RootStatus(); status.Migration == nil || status.Migration.Status != MigrationBlockedByWorkspace {
		t.Errorf("RootStatus.Migration = %+v", status.Migration)
	}
}

func TestCompositeRenamesAndDeletesWithinTheOwningStore(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := c.RenameAgent("Scout", "Ranger"); err != nil {
		t.Fatalf("RenameAgent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", "Ranger", "agent_settings.json")); err != nil {
		t.Errorf("rename did not move the folder in the root: %v", err)
	}
	if err := c.DeleteAgent("Ranger"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", "Ranger")); !os.IsNotExist(err) {
		t.Error("delete left the folder in the root")
	}
}
