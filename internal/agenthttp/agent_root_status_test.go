package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func compositeForRoot(t *testing.T, root string) *store.CompositeStore {
	t.Helper()
	dataDir := t.TempDir()
	system, err := store.NewFileStore(filepath.Join(dataDir, "agents.json"), types.Settings{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("system store: %v", err)
	}
	c, err := store.NewCompositeStore(system, root, filepath.Join(dataDir, "agent_state"), types.Settings{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("composite: %v", err)
	}
	return c
}

func listAgentRoot(t *testing.T, st store.Store) store.AgentRootStatus {
	t.Helper()
	rr := httptest.NewRecorder()
	NewDashboardHandler(st).ListAgentsWithStats(rr, httptest.NewRequest(http.MethodGet, "/api/agents/dashboard/list", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		AgentRoot *store.AgentRootStatus `json:"agent_root"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.AgentRoot == nil {
		t.Fatalf("the list response carries no agent_root: %s", rr.Body.String())
	}
	return *body.AgentRoot
}

func TestRosterListReportsTheAgentsFolder(t *testing.T) {
	root := t.TempDir()
	status := listAgentRoot(t, compositeForRoot(t, root))
	if !status.Available || status.Path != filepath.Join(root, "Agents") {
		t.Errorf("agent_root = %+v, want available at %s", status, filepath.Join(root, "Agents"))
	}
}

func TestRosterListReportsAMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "unmounted")
	status := listAgentRoot(t, compositeForRoot(t, root))
	if status.Available || status.Reason != store.RootReasonMissing {
		t.Errorf("agent_root = %+v, want unavailable/missing", status)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("reporting the status created the root")
	}
}

func TestRosterListReportsABlockedMigration(t *testing.T) {
	c := compositeForRoot(t, t.TempDir())
	c.SetMigrationReport(&store.RootMigrationReport{Status: store.MigrationBlockedByWorkspace})
	status := listAgentRoot(t, c)
	if status.Migration == nil || status.Migration.Status != store.MigrationBlockedByWorkspace {
		t.Errorf("agent_root.migration = %+v, want blocked_by_workspace", status.Migration)
	}
}
