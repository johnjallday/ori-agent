package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// seedLegacyAgent writes an agent in the pre-root layout: <data dir>/agents/<name>/.
func seedLegacyAgent(t *testing.T, dataDir, name string) {
	t.Helper()
	dir := filepath.Join(dataDir, "agents", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent_settings.json"), []byte(`{"Settings":{"model":"gpt-4o-mini"}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func agentStoreEnv(t *testing.T) (dataDir, root string) {
	t.Helper()
	base := t.TempDir()
	dataDir = filepath.Join(base, "data")
	root = filepath.Join(base, "Ori Workspaces")
	for _, dir := range []string{dataDir, root} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	t.Setenv("ORI_DATA_DIR", dataDir)
	t.Setenv("AGENT_STORE_PATH", "")
	// An operator-set root counts as confirmed, like startup maintenance.
	t.Setenv("WORKSPACE_DIR", root)
	return dataDir, root
}

func TestAgentStoreMovesLegacyAgentsIntoAConfirmedRoot(t *testing.T) {
	dataDir, root := agentStoreEnv(t)
	seedLegacyAgent(t, dataDir, "Scout")

	st, err := createAgentStore(filepath.Join(dataDir, "agents.json"), types.Settings{}, nil, false)
	if err != nil {
		t.Fatalf("createAgentStore: %v", err)
	}
	if _, ok := st.(*store.CompositeStore); !ok {
		t.Fatalf("store is %T, want the composite", st)
	}
	if _, err := os.Stat(filepath.Join(root, "Agents", "Scout", "agent_settings.json")); err != nil {
		t.Fatalf("Scout was not moved into the root: %v", err)
	}
	if _, ok := st.GetAgent("Scout"); !ok {
		t.Error("Scout is not in the roster after the move")
	}
	if _, err := os.Stat(filepath.Join(dataDir, store.RootMigrationMarker)); err != nil {
		t.Errorf("no migration marker: %v", err)
	}
}

func TestAgentStorePathKeepsTheSingleLegacyStore(t *testing.T) {
	dataDir, root := agentStoreEnv(t)
	seedLegacyAgent(t, dataDir, "Scout")
	index := filepath.Join(dataDir, "agents.json")
	t.Setenv("AGENT_STORE_PATH", index)

	st, err := createAgentStore(index, types.Settings{}, nil, false)
	if err != nil {
		t.Fatalf("createAgentStore: %v", err)
	}
	if _, ok := st.(*store.CompositeStore); ok {
		t.Fatal("AGENT_STORE_PATH must keep the single store")
	}
	if _, err := os.Stat(filepath.Join(root, "Agents")); !os.IsNotExist(err) {
		t.Error("AGENT_STORE_PATH wrote into the root")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "agents", "Scout")); err != nil {
		t.Errorf("AGENT_STORE_PATH moved the agent: %v", err)
	}
}

func TestPendingAgentResetSuppressesTheMigration(t *testing.T) {
	dataDir, root := agentStoreEnv(t)
	seedLegacyAgent(t, dataDir, "Scout")

	if _, err := createAgentStore(filepath.Join(dataDir, "agents.json"), types.Settings{}, nil, true); err != nil {
		t.Fatalf("createAgentStore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Agents")); !os.IsNotExist(err) {
		t.Error("a pending agents reset must not be undone by a migration")
	}
}
