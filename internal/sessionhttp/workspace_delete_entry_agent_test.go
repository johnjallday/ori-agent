package sessionhttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// TestDeleteWorkspace_DeletesEntryAgent verifies that permanently deleting a
// workspace also deletes its designated entry agent from the agent store.
// (The default delete moves the workspace to Trash and preserves the agent so
// it can be restored — see TestTrashWorkspace_PreservesEntryAgent.)
func TestDeleteWorkspace_DeletesEntryAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	wsStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = wsStore.Close() }()
	handler.SetWorkspaceStore(wsStore)

	// Create a workspace via the API so it exists in the session store too.
	workspaceID := createTestWorkspace(t, handler, "Travel Plans")

	// Create the entry agent and attach it to the folder-based workspace.
	entryAgentName := "Travel Plans Manager"
	if err := handler.agentStore.CreateAgent(entryAgentName, &agentstore.CreateAgentConfig{
		Type: agent.TypeGeneral,
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	folderWS, err := wsStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("Get workspace from folder store: %v", err)
	}
	folderWS.AgentInstances = []agentworkspace.AgentInstance{
		{ID: "inst-1", Name: entryAgentName, EntryPoint: true},
	}
	if folderWS.SharedData == nil {
		folderWS.SharedData = map[string]any{}
	}
	folderWS.SharedData["entry_agent_name"] = entryAgentName
	if err := wsStore.Save(folderWS); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	// Sanity: agent exists before the delete.
	if _, ok := handler.agentStore.GetAgent(entryAgentName); !ok {
		t.Fatalf("expected entry agent to exist before workspace deletion")
	}

	// Permanently delete the workspace (confirm=true skips the session-count
	// prompt; permanent=true performs the hard delete rather than trashing).
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+workspaceID+"?confirm=true&permanent=true", bytes.NewReader(nil))
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on workspace delete, got %d: %s", w.Code, w.Body.String())
	}

	// Entry agent should be gone from the agent store.
	if _, ok := handler.agentStore.GetAgent(entryAgentName); ok {
		t.Fatalf("expected entry agent %q to be deleted with workspace", entryAgentName)
	}
}

// TestTrashWorkspace_PreservesEntryAgent verifies that the default delete moves
// the workspace to Trash without removing its entry agent, so a later restore
// keeps the agent intact.
func TestTrashWorkspace_PreservesEntryAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	wsStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = wsStore.Close() }()
	handler.SetWorkspaceStore(wsStore)

	workspaceID := createTestWorkspace(t, handler, "Travel Plans")

	entryAgentName := "Travel Plans Manager"
	if err := handler.agentStore.CreateAgent(entryAgentName, &agentstore.CreateAgentConfig{
		Type: agent.TypeGeneral,
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	folderWS, err := wsStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("Get workspace from folder store: %v", err)
	}
	folderWS.AgentInstances = []agentworkspace.AgentInstance{
		{ID: "inst-1", Name: entryAgentName, EntryPoint: true},
	}
	if folderWS.SharedData == nil {
		folderWS.SharedData = map[string]any{}
	}
	folderWS.SharedData["entry_agent_name"] = entryAgentName
	if err := wsStore.Save(folderWS); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	// Default delete (no permanent flag) — moves to Trash.
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+workspaceID+"?confirm=true", bytes.NewReader(nil))
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on trash, got %d: %s", w.Code, w.Body.String())
	}

	// Entry agent must still exist after trashing.
	if _, ok := handler.agentStore.GetAgent(entryAgentName); !ok {
		t.Fatalf("expected entry agent %q to be preserved when workspace is trashed", entryAgentName)
	}
}

// TestDeleteWorkspace_NoEntryAgent_StillSucceeds verifies that deleting a
// workspace without an entry agent works normally.
func TestDeleteWorkspace_NoEntryAgent_StillSucceeds(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	wsStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = wsStore.Close() }()
	handler.SetWorkspaceStore(wsStore)

	workspaceID := createTestWorkspace(t, handler, "Empty Space")

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+workspaceID+"?confirm=true", bytes.NewReader(nil))
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}
