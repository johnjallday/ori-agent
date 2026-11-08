package sessionhttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
)

func TestRoleStaffingRollbackPreservesConcurrentAssignmentOrReplacement(t *testing.T) {
	for _, change := range []string{"assignment", "replacement", "unknown_references"} {
		t.Run(change, func(t *testing.T) {
			h, _ := createTestHandler(t)
			newTestFileStore(t, h)
			var seed seedAgentsResult
			if err := h.createSeededProfile(context.Background(), &seed, "New manager", &agentstore.CreateAgentConfig{}); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "assignment":
				if err := h.store.CreateWorkspace(context.Background(), &session.Workspace{ID: "other", Name: "Other", FolderSlug: "other", AgentInstances: []session.AgentInstance{{Name: "New manager"}}}); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := h.agentStore.DeleteAgent("New manager"); err != nil {
					t.Fatal(err)
				}
				if err := h.agentStore.CreateAgent("New manager", &agentstore.CreateAgentConfig{}); err != nil {
					t.Fatal(err)
				}
			case "unknown_references":
				h.store = &withoutReferenceReader{HybridStore: h.store}
			}
			if failures := h.rollbackSeededAgents(seed); len(failures) != 1 {
				t.Fatalf("uncertain cleanup not reported: %v", failures)
			}
			if _, exists := h.agentStore.GetAgent("New manager"); !exists {
				t.Fatal("rollback deleted another operation's profile")
			}
		})
	}
}

func TestRoleStaffingRollbackPreservesPreviouslyCreatedSharedProfile(t *testing.T) {
	h, otherID, name := cleanupEntryFixture(t)
	before := agentstore.ReadStaffingSnapshot(context.Background(), h.agentStore)
	if len(before.Profiles) != 1 {
		t.Fatal("expected one saved profile")
	}
	tpl := projecttemplates.Template{Agents: []projecttemplates.AgentSpec{{Name: "Coordinator"}, {Name: "Helper"}, {Name: "Reviewer"}}}
	roles := projecttemplates.AgentRoleIDs(tpl.Agents)
	seed, err := h.seedRoleStaffedAgents(context.Background(), &session.Workspace{ID: "failed-creation"}, tpl, map[string]roleStaffingInput{
		roles[0]: {Name: name, Mode: roleStaffingModeAssign},
		roles[1]: {Name: "Temporary helper", Mode: roleStaffingModeCreate},
		roles[2]: {Name: "Missing reviewer", Mode: roleStaffingModeAssign},
	})
	if err == nil || len(seed.OwnedNames) != 1 || seed.OwnedNames[0] != "Temporary helper" {
		t.Fatalf("wrong partial creation ownership: %+v err=%v", seed.OwnedNames, err)
	}
	if failures := h.rollbackSeededAgents(seed); len(failures) != 0 {
		t.Fatalf("rollback failed: %v", failures)
	}
	assertEntryRetained(t, h, name)
	after := agentstore.ReadStaffingSnapshot(context.Background(), h.agentStore)
	if len(after.Profiles) != 1 || before.Profiles[0].Witness != after.Profiles[0].Witness {
		t.Fatal("rollback changed shared definition/configuration")
	}
	other, err := h.workspaceStore.Get(otherID)
	if err != nil || other.EntryAgentName() != name || len(other.AgentInstances) != 1 {
		t.Fatal("rollback changed the original assignment")
	}
}
