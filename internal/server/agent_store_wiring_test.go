package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// seedLegacyScout writes Scout in the pre-root layout: <data dir>/agents/Scout/.
func seedLegacyScout(t *testing.T, dataDir string) {
	t.Helper()
	dir := filepath.Join(dataDir, "agents", "Scout")
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
	seedLegacyScout(t, dataDir)

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

// The image move is independent of the agent migration's marker: an image an
// older build left in the shared folder moves in on the next start.
func TestStartupMovesAnAgentsOlderImageIntoItsFolder(t *testing.T) {
	dataDir, root := agentStoreEnv(t)
	seedLegacyScout(t, dataDir)
	st, err := createAgentStore(filepath.Join(dataDir, "agents.json"), types.Settings{}, nil, false)
	if err != nil {
		t.Fatalf("createAgentStore: %v", err)
	}
	ag, _ := st.GetAgent("Scout")
	ag.EnsureAppearance()
	ag.Appearance.SetUpload("Scout.png")
	if err := st.SetAgent("Scout", ag); err != nil {
		t.Fatalf("SetAgent: %v", err)
	}
	shared := filepath.Join(dataDir, "agent_avatars")
	if err := os.MkdirAll(shared, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shared, "Scout.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	if _, err := createAgentStore(filepath.Join(dataDir, "agents.json"), types.Settings{}, nil, false); err != nil {
		t.Fatalf("second createAgentStore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Agents", "Scout", "Scout.png")); err != nil {
		t.Fatalf("the image did not move into Scout's folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(shared, "Scout.png")); !os.IsNotExist(err) {
		t.Error("the shared copy is still there")
	}
}

func TestAgentStorePathKeepsTheSingleLegacyStore(t *testing.T) {
	dataDir, root := agentStoreEnv(t)
	seedLegacyScout(t, dataDir)
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
	seedLegacyScout(t, dataDir)

	if _, err := createAgentStore(filepath.Join(dataDir, "agents.json"), types.Settings{}, nil, true); err != nil {
		t.Fatalf("createAgentStore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Agents")); !os.IsNotExist(err) {
		t.Error("a pending agents reset must not be undone by a migration")
	}
}
