package sessionhttp

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceCreationOperation_DeletedParentRefusesBeforeAcceptance(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	_, groupID, _, _ := setupGroupWithChild(t, h)
	id, digest := reviewCreationOperation(t, h, map[string]any{
		"name": "Must not become standalone", "parent_id": groupID,
		"blank": true, "create_template_agents": false,
	})
	// A real, undoable Group-only deletion keeps the trash record for recovery.
	// Its existence is not permission to create beneath that unavailable owner.
	deleteWorkspaceReq(t, h, groupID, "confirm=true&delete_mode=group_only", http.StatusOK)
	before, err := h.store.ListWorkspaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	agents := len(h.agentStore.ListAgents())
	response, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Parent group not found") {
		t.Fatalf("unavailable parent confirmed: %d %s", response.Code, response.Body)
	}
	assertCreationOperationState(t, h, id, "refused")
	after, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(after) != len(before) || len(h.agentStore.ListAgents()) != agents {
		t.Fatalf("refused parent created resources: %v %v", after, err)
	}
	for _, workspace := range after {
		if workspace.Name == "Must not become standalone" {
			t.Fatal("silently created a replacement or standalone workspace")
		}
	}
}

func TestWorkspaceCreationOperation_OrdinaryGroupKeepsReviewedManagerAndExactResource(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	_, root := newTestFileStore(t, h)
	var request map[string]any
	if err := json.Unmarshal([]byte(groupRosterCreateBody(t, h, "Reviewed Group", "Reviewed Manager")), &request); err != nil {
		t.Fatal(err)
	}
	id, digest := reviewCreationOperation(t, h, request)
	before, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(before) != 0 || len(h.agentStore.ListAgents()) != 0 {
		t.Fatalf("group review executed effects: %v %v", before, err)
	}
	response, body := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if response.Code != http.StatusCreated {
		t.Fatalf("confirm: %d %s", response.Code, response.Body)
	}
	group := body["folder"].(map[string]any)
	groupID := group["id"].(string)
	if group["kind"] != "group" || group["name"] != "Reviewed Group" {
		t.Fatalf("wrong result: %v", group)
	}
	members := group["agent_instances"].([]any)
	if len(members) != 1 || members[0].(map[string]any)["name"] != "Reviewed Manager" || members[0].(map[string]any)["entry_point"] != true {
		t.Fatalf("reviewed manager changed: %v", members)
	}
	assertScopedGroupScaffolding(t, h, groupID, filepath.Join(root, "reviewed-group"))
	for range 3 {
		observed := assertCreationOperationState(t, h, id, "complete")
		if observed["result"].(map[string]any)["folder"].(map[string]any)["id"] != groupID {
			t.Fatalf("observation lost exact group identity: %v", observed)
		}
		replay, same := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if replay.Code != http.StatusCreated || same["folder"].(map[string]any)["id"] != groupID {
			t.Fatalf("replay: %d %s", replay.Code, replay.Body)
		}
	}
	after, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(after) != 1 || len(h.agentStore.ListAgents()) != 1 {
		t.Fatalf("group confirmation replayed effects: %v %v", after, err)
	}
}
