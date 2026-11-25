package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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

// The retired assistant's name is an ordinary one now: a person's agent called
// "Ask Ori" is written to the Workspace Directory like every other agent.
func TestCompositeWritesAUsersAskOriToTheRoot(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	for _, name := range []string{"Ask Ori", "Scout"} {
		if err := c.CreateAgent(name, &CreateAgentConfig{}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(f.root, "Agents", name)); err != nil {
			t.Errorf("%s is not in the root: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(f.dataDir, "agents", name)); !os.IsNotExist(err) {
			t.Errorf("%s was written into the data dir", name)
		}
	}
	if got := c.ListAgents(); len(got) != 2 {
		t.Errorf("ListAgents = %v, want Ask Ori and Scout", got)
	}
	reopened := f.open(t)
	for _, name := range []string{"Ask Ori", "Scout"} {
		if _, ok := reopened.GetAgent(name); !ok {
			t.Errorf("%s did not load after a restart", name)
		}
	}
}

// An existing profile's retired assistant record stays in the data dir, as
// it was, and no list or lookup reaches it — while a person's own agent of
// the same name in the root is reached normally.
func TestCompositeHidesTheRetiredAssistantRecord(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	markedDir := filepath.Join(f.dataDir, "agents", "Ask Ori")
	if err := c.system.CreateAgent("Ask Ori", &CreateAgentConfig{}); err != nil {
		t.Fatalf("seed the retired record: %v", err)
	}
	markRetiredAssistant(t, c.system, "Ask Ori")
	if err := c.system.Save(); err != nil {
		t.Fatalf("persist the retired record: %v", err)
	}

	reopened := f.open(t)
	if _, ok := reopened.GetAgent("Ask Ori"); ok {
		t.Fatal("the retired assistant record was reachable")
	}
	if got := reopened.ListAgents(); len(got) != 0 {
		t.Fatalf("ListAgents = %v, want nothing", got)
	}
	if err := reopened.CreateAgent("Ask Ori", &CreateAgentConfig{}); err != nil {
		t.Fatalf("a person must be able to make their own Ask Ori: %v", err)
	}
	if _, ok := reopened.GetAgent("Ask Ori"); !ok {
		t.Fatal("the person's own Ask Ori was not reachable")
	}
	if _, err := os.Stat(markedDir); err != nil {
		t.Fatalf("the retired record was removed from the data dir: %v", err)
	}
}

func TestCompositeWithAMissingRootCreatesNoAgents(t *testing.T) {
	f := newCompositeFixture(t)
	f.root = filepath.Join(f.root, "unmounted-drive")
	c := f.open(t)
	for _, name := range []string{"Ask Ori", "Scout"} {
		err := c.CreateAgent(name, &CreateAgentConfig{})
		if !errors.Is(err, ErrAgentRootUnavailable) {
			t.Fatalf("CreateAgent(%q) on a missing root: err = %v, want ErrAgentRootUnavailable", name, err)
		}
	}
	if _, err := os.Stat(f.root); !os.IsNotExist(err) {
		t.Fatal("the store created a missing root")
	}
	if got := c.ListAgents(); len(got) != 0 {
		t.Errorf("ListAgents = %v, want nothing", got)
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
