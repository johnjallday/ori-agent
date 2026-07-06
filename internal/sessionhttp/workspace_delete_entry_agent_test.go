package sessionhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// TestDeleteWorkspace_DeletesEntryAgent verifies that permanently deleting a
// workspace also deletes its designated entry agent from the agent store. The
// default delete now moves a workspace to the trash (which preserves the entry
// agent for restore), so delete_sessions=true is used to force a permanent
// delete on every platform.
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
	// prompt; delete_sessions=true forces a permanent delete rather than trash).
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+workspaceID+"?confirm=true&delete_sessions=true", bytes.NewReader(nil))
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

// TestDeleteWorkspace_PurgesSessionsForEntryAgent verifies that permanently
// deleting a workspace also purges any session that still references the
// deleted entry agent — including a general (non-workspace-linked) session —
// so the UI cannot restore stale state that resolves to a 404 on /api/agents.
func TestDeleteWorkspace_PurgesSessionsForEntryAgent(t *testing.T) {
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

	// A general chat session that references the entry agent but is NOT linked
	// to the workspace — this is the reported case that used to survive with a
	// dangling agent_name after the workspace (and its entry agent) was deleted.
	ctx := context.Background()
	strays := &session.Session{ID: "stray-session", Title: "Chat", AgentName: entryAgentName}
	if err := handler.store.CreateSession(ctx, strays); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+workspaceID+"?confirm=true&delete_sessions=true", bytes.NewReader(nil))
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on workspace delete, got %d: %s", w.Code, w.Body.String())
	}

	if _, err := handler.store.GetSession(ctx, "stray-session"); err != session.ErrSessionNotFound {
		t.Fatalf("expected stray session referencing deleted entry agent to be purged, got err=%v", err)
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

	// Either 204 (permanent delete) or 200 (moved to trash, where supported) is
	// a success; the point is that a workspace without an entry agent deletes
	// cleanly.
	if w.Code != http.StatusNoContent && w.Code != http.StatusOK {
		t.Fatalf("expected 204 or 200, got %d: %s", w.Code, w.Body.String())
	}
}
