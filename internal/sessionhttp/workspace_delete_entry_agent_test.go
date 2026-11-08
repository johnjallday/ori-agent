package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func cleanupEntryFixture(t *testing.T) (*Handler, string, string) {
	t.Helper()
	h, _ := createTestHandler(t)
	newTestFileStore(t, h)
	id := createTestWorkspace(t, h, "Travel Plans")
	name := "Travel Plans Manager"
	stampOwnedCleanupEntry(t, h, id, name)
	if err := h.store.CreateSession(context.Background(), &session.Session{ID: "stray-session", Title: "Unrelated chat", AgentName: name}); err != nil {
		t.Fatal(err)
	}
	return h, id, name
}

func stampOwnedCleanupEntry(t *testing.T, h *Handler, id, name string) {
	t.Helper()
	operation := "create-" + id
	if err := h.agentStore.CreateAgent(name, &agentstore.CreateAgentConfig{AssistantSetup: &agent.AssistantSetupProvenance{ID: "profile-" + id, OperationID: operation, ConfigDigest: "reviewed-config"}}); err != nil {
		t.Fatal(err)
	}
	ws, err := h.workspaceStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	ws.AgentInstances = []agentworkspace.AgentInstance{{ID: "entry-" + id, Name: name, EntryPoint: true, RoleSource: agentworkspace.RoleSourceCreated}}
	ws.SetTemplateProvenance(&agentworkspace.TemplateProvenance{AssistantSetup: &agentworkspace.AssistantSetupCreation{RunID: "run-" + id, OperationID: operation, ReviewDigest: "reviewed-team"}})
	if err := ws.SetEntryAgentName(name); err != nil {
		t.Fatal(err)
	}
	if err := h.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
}

func deleteWorkspacePermanently(t *testing.T, h *Handler, id, query string) {
	t.Helper()
	w := httptest.NewRecorder()
	h.HandleWorkspaces(w, httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+id+"?confirm=true&delete_sessions=true"+query, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d: %s", w.Code, w.Body.String())
	}
}

func assertEntryRetained(t *testing.T, h *Handler, name string) {
	t.Helper()
	if _, exists := h.agentStore.GetAgent(name); !exists {
		t.Fatal("cleanup deleted a retained profile")
	}
	if _, err := h.store.GetSession(context.Background(), "stray-session"); err != nil {
		t.Fatalf("cleanup purged unrelated session: %v", err)
	}
}

// Only matching host creation provenance plus verified absence of references
// keeps the former automatic cleanup behavior. Display RoleSource is not enough.
func TestDeleteWorkspace_DeletesEntryAgent(t *testing.T) {
	h, id, name := cleanupEntryFixture(t)
	deleteWorkspacePermanently(t, h, id, "")
	if _, exists := h.agentStore.GetAgent(name); exists {
		t.Fatal("verified owned, unshared entry profile was not cleaned")
	}
}

func TestDeleteWorkspace_PurgesSessionsForEntryAgent(t *testing.T) {
	h, id, _ := cleanupEntryFixture(t)
	deleteWorkspacePermanently(t, h, id, "")
	if _, err := h.store.GetSession(context.Background(), "stray-session"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("owned entry session was not purged: %v", err)
	}
}

func TestDeleteWorkspace_PreservesAssignedSharedAndUnverifiableEntry(t *testing.T) {
	for _, scenario := range []string{"assigned", "legacy", "created_without_operation", "replacement", "different_operation", "folder_only_reference", "primary_only_reference", "restorable_reference", "reference_reader_unavailable", "reference_read_error"} {
		t.Run(scenario, func(t *testing.T) {
			h, id, name := cleanupEntryFixture(t)
			switch scenario {
			case "assigned", "legacy", "created_without_operation":
				ws, err := h.workspaceStore.Get(id)
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "assigned":
					ws.AgentInstances[0].RoleSource = agentworkspace.RoleSourceAssigned
				case "legacy":
					ws.AgentInstances[0].RoleSource = ""
				case "created_without_operation":
					ws.SetTemplateProvenance(nil)
				}
				if err := h.workspaceStore.Save(ws); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := h.agentStore.DeleteAgent(name); err != nil {
					t.Fatal(err)
				}
				if err := h.agentStore.CreateAgent(name, &agentstore.CreateAgentConfig{}); err != nil {
					t.Fatal(err)
				}
			case "different_operation":
				if _, err := agentstore.MutateDefinition(h.agentStore, name, func(a *agent.Agent) error { a.AssistantSetup.OperationID = "unrelated"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "folder_only_reference":
				otherID := createTestWorkspace(t, h, "Other assignment")
				other, err := h.workspaceStore.Get(otherID)
				if err != nil {
					t.Fatal(err)
				}
				other.AgentInstances = []agentworkspace.AgentInstance{{ID: "other", Name: name, RoleSource: agentworkspace.RoleSourceAssigned}}
				if err := h.workspaceStore.Save(other); err != nil {
					t.Fatal(err)
				}
			case "primary_only_reference", "restorable_reference":
				// No corresponding folder exists. The primary row alone protects
				// the assignment, including a Home retained for supported Undo.
				status := session.WorkspaceStatusActive
				if scenario == "restorable_reference" {
					status = session.WorkspaceStatusTrashed
				}
				if err := h.store.CreateWorkspace(context.Background(), &session.Workspace{ID: "other", Name: "Other assignment", FolderSlug: "other", Status: status, AgentInstances: []session.AgentInstance{{ID: "other", Name: name, RoleSource: agentworkspace.RoleSourceAssigned}}}); err != nil {
					t.Fatal(err)
				}
			case "reference_reader_unavailable":
				h.store = &withoutReferenceReader{HybridStore: h.store}
			case "reference_read_error":
				h.store = &failedReferenceReader{HybridStore: h.store}
			}
			deleteWorkspacePermanently(t, h, id, "")
			assertEntryRetained(t, h, name)
		})
	}
}

type withoutReferenceReader struct{ session.HybridStore }
type failedReferenceReader struct{ session.HybridStore }

func (*failedReferenceReader) HasWorkspaceAgentReference(context.Context, string, string) (bool, error) {
	return false, errors.New("reference storage unavailable")
}

func TestDeleteWorkspace_CleanupRefusesSourceChangedAfterCapture(t *testing.T) {
	h, id, name := cleanupEntryFixture(t)
	candidate := h.prepareEntryAgentCleanup(context.Background(), id)
	if candidate == nil {
		t.Fatal("fixture has no cleanup witness")
	}
	if err := h.agentStore.DeleteAgent(name); err != nil {
		t.Fatal(err)
	}
	if err := h.agentStore.CreateAgent(name, &agentstore.CreateAgentConfig{AssistantSetup: &agent.AssistantSetupProvenance{ID: "replacement", OperationID: candidate.operationID, ConfigDigest: "reviewed-config"}}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.DeleteWorkspace(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := h.workspaceStore.Delete(id); err != nil {
		t.Fatal(err)
	}
	h.cleanupEntryAgent(context.Background(), candidate, id)
	assertEntryRetained(t, h, name)
}

func TestDeleteGroup_PreservesSharedOrAssignedEntry(t *testing.T) {
	for _, mode := range []string{"contents", "group_only"} {
		t.Run(mode, func(t *testing.T) {
			h, id, name := cleanupEntryFixture(t)
			groupID := createTestGroup(t, h, "Parent")
			member, err := h.store.GetWorkspace(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			member.ParentID = groupID
			if err := h.store.UpdateWorkspace(context.Background(), member); err != nil {
				t.Fatal(err)
			}
			group, err := h.workspaceStore.Get(groupID)
			if err != nil {
				t.Fatal(err)
			}
			group.AgentInstances = []agentworkspace.AgentInstance{{ID: "group-entry", Name: name, EntryPoint: true, RoleSource: agentworkspace.RoleSourceAssigned}}
			if err := group.SetEntryAgentName(name); err != nil {
				t.Fatal(err)
			}
			if err := h.workspaceStore.Save(group); err != nil {
				t.Fatal(err)
			}
			// A separate, restorable assignment protects even the original
			// creation's profile when its containing group is removed.
			if err := h.store.CreateWorkspace(context.Background(), &session.Workspace{ID: "restorable", Name: "Retained", FolderSlug: "retained", Status: session.WorkspaceStatusTrashed, AgentInstances: []session.AgentInstance{{ID: "retained-entry", Name: name}}}); err != nil {
				t.Fatal(err)
			}
			deleteWorkspacePermanently(t, h, groupID, "&delete_mode="+mode)
			assertEntryRetained(t, h, name)
		})
	}
}

func TestDeleteGroup_CleansVerifiedOwnedUnsharedEntry(t *testing.T) {
	for _, mode := range []string{"contents", "group_only"} {
		t.Run(mode, func(t *testing.T) {
			h, _ := createTestHandler(t)
			newTestFileStore(t, h)
			id := createTestGroup(t, h, "Owned group")
			name := "Verified group entry"
			stampOwnedCleanupEntry(t, h, id, name)
			if err := h.store.CreateSession(context.Background(), &session.Session{ID: "stray-session", Title: "Chat", AgentName: name}); err != nil {
				t.Fatal(err)
			}
			deleteWorkspacePermanently(t, h, id, "&delete_mode="+mode)
			if _, exists := h.agentStore.GetAgent(name); exists {
				t.Fatal("owned group entry survived cleanup")
			}
			if _, err := h.store.GetSession(context.Background(), "stray-session"); !errors.Is(err, session.ErrSessionNotFound) {
				t.Fatalf("deleted entry session survived: %v", err)
			}
		})
	}
}

func TestDeleteWorkspace_NoEntryAgent_StillSucceeds(t *testing.T) {
	h, _ := createTestHandler(t)
	newTestFileStore(t, h)
	id := createTestWorkspace(t, h, "Empty Space")
	deleteWorkspacePermanently(t, h, id, "")
}
