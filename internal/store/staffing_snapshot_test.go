package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestStaffingSnapshotCanonicalReadOnlySources(t *testing.T) {
	c, f, _ := compositeWithWorkspaces(t,
		workspaceCopy("ws-1", "Earlier", "Private", "private canary"),
		workspaceCopy("ws-2", "Studio", "Scout", "customized canary"))
	for _, name := range []string{"Scout", systemassistant.CanonicalName} {
		if err := c.CreateAgent(name, &CreateAgentConfig{SystemPrompt: "secret canary"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.UpdateAgent("Scout", func(a *agent.Agent) error {
		a.Metadata = &types.AgentMetadata{Description: "Coordinates music portfolios"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, filepath.Dir(f.dataDir))
	got := c.ReadStaffingSnapshot(context.Background())
	if !got.Complete || len(got.Profiles) != 1 || got.Profiles[0].Name != "Scout" || got.Profiles[0].Source != "root" || got.Profiles[0].Purpose != "Coordinates music portfolios" || got.Profiles[0].Witness == "" {
		t.Fatalf("unexpected canonical projection: %+v", got)
	}
	if after := snapshotTree(t, filepath.Dir(f.dataDir)); !equalTrees(before, after) {
		t.Fatal("inspection wrote files")
	}
	// The private workspace reader is not consulted, even when its trust changes.
	c.SetWorkspaceAgentSource(panicWorkspaceSource{})
	again := c.ReadStaffingSnapshot(context.Background())
	if len(again.Profiles) != 1 || again.Profiles[0].Witness != got.Profiles[0].Witness {
		t.Fatal("workspace view changed root identity")
	}
}

type panicWorkspaceSource struct{}

func (panicWorkspaceSource) WorkspaceAgents() []WorkspaceAgentEntry {
	panic("staffing must not read workspace copies")
}

func TestStaffingSnapshotWitnessExpiresOnChanges(t *testing.T) {
	c, f, _ := compositeWithWorkspaces(t)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatal(err)
	}
	witness := func() string {
		t.Helper()
		snapshot := c.ReadStaffingSnapshot(context.Background())
		if len(snapshot.Profiles) != 1 {
			t.Fatalf("missing profile: %+v", snapshot)
		}
		return snapshot.Profiles[0].Witness
	}
	original := witness()
	if original != witness() {
		t.Fatal("unchanged source is unstable")
	}
	if _, err := MutateDefinition(c, "Scout", func(a *agent.Agent) error { a.Settings.SystemPrompt = "private edit"; return nil }); err != nil {
		t.Fatal(err)
	}
	changed := witness()
	if changed == original {
		t.Fatal("private configuration change kept witness")
	}
	if err := c.DeleteAgent("Scout"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "private edit"}); err != nil {
		t.Fatal(err)
	}
	replaced := witness()
	if replaced == changed {
		t.Fatal("same-name replacement kept witness")
	}
	c.SetRoot(t.TempDir())
	c.SetRoot(f.root)
	if witness() == replaced {
		t.Fatal("root switch kept witness")
	}
	beforeRestart := witness()
	c = f.open(t)
	if witness() == beforeRestart {
		t.Fatal("restart accepted an uncommitted witness")
	}
}

func TestStaffingSnapshotUnavailableAndAmbiguous(t *testing.T) {
	for _, mode := range []string{"missing_root", "deleted_definition", "changed_definition", "unreadable", "new_definition", "symlink", "case_collision"} {
		t.Run(mode, func(t *testing.T) {
			c, f, _ := compositeWithWorkspaces(t)
			if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.root, "Agents", "Scout", definitionFileName)
			switch mode {
			case "missing_root":
				c.SetRoot(filepath.Join(t.TempDir(), "missing"))
			case "deleted_definition":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "changed_definition":
				if err := os.WriteFile(path, []byte(`{"Settings":{"Model":"changed"}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if err := os.WriteFile(path, []byte(`broken`), 0o600); err != nil {
					t.Fatal(err)
				}
				c.ReloadRoot()
			case "new_definition":
				dir := filepath.Join(f.root, "Agents", "New")
				if err := os.MkdirAll(dir, 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, definitionFileName), []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				outside := filepath.Join(t.TempDir(), "profile")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(outside, data, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "case_collision":
				// A legacy namesake is distinct even on case-insensitive filesystems.
				if err := c.system.CreateAgent("scout", &CreateAgentConfig{}); err != nil {
					t.Fatal(err)
				}
			}
			got := c.ReadStaffingSnapshot(context.Background())
			if got.Complete {
				t.Fatalf("%s was reported complete", mode)
			}
			if mode != "new_definition" && len(got.Profiles) != 0 {
				t.Fatalf("unavailable source leaked: %+v", got)
			}
		})
	}
}

// A store lacking explicit source evidence must not be promoted by name lookup.
type unknownStaffingStore struct{ Store }

func TestStaffingSnapshotUnknownStoresAndPrivateReferences(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t)
	if got := ReadStaffingSnapshot(context.Background(), unknownStaffingStore{c}); got.Complete || len(got.Profiles) != 0 {
		t.Fatal("unknown source acquired library authority")
	}
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	c.root.mu.Lock()
	c.root.agents["Scout"].WorkspaceLocalConfigID = "PRIVATE_REFERENCE_CANARY"
	c.root.mu.Unlock()
	if got := c.ReadStaffingSnapshot(context.Background()); len(got.Profiles) != 0 {
		t.Fatal("workspace-private reference leaked")
	}
}

func TestStaffingSnapshotConcurrentDefinitionChanges(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			_, err := MutateDefinition(c, "Scout", func(a *agent.Agent) error { a.Metadata = &types.AgentMetadata{Description: fmt.Sprint(i)}; return nil })
			if err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			if got := c.ReadStaffingSnapshot(context.Background()); !got.Complete || len(got.Profiles) != 1 {
				t.Error("lost coherent profile snapshot")
				return
			}
		}
	}()
	wg.Wait()
}

func TestStaffingSnapshotUnreadableRootDoesNotAuthorizeLegacyNamesake(t *testing.T) {
	c, f, _ := compositeWithWorkspaces(t)
	if err := c.system.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.root, "Agents", "Scout")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, definitionFileName), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.ReloadRoot()
	if got := c.ReadStaffingSnapshot(context.Background()); got.Complete || len(got.Profiles) != 0 {
		t.Fatal("unreadable root authorized legacy namesake")
	}
}

func TestStaffingSnapshotIncludesLegacyAndBeyondFifty(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t)
	if err := c.system.CreateAgent("Legacy", &CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 61; i++ {
		if err := c.CreateAgent(fmt.Sprintf("Agent-%02d", i), &CreateAgentConfig{}); err != nil {
			t.Fatal(err)
		}
	}
	got := c.ReadStaffingSnapshot(context.Background())
	if !got.Complete || len(got.Profiles) != 62 {
		t.Fatalf("roster truncated: %+v", got)
	}
	if got.Profiles[len(got.Profiles)-1].Source != "legacy" {
		t.Fatal("legacy source not retained")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := c.ReadStaffingSnapshot(ctx); got.Complete || len(got.Profiles) != 0 {
		t.Fatal("cancelled scan returned success")
	}
}
