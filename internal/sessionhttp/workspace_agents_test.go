package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestHandleWorkspaceAgents_DeleteCleansWorkspaceState(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	now := time.Now()
	writerOne := session.AgentInstance{
		ID:             "writer-1",
		Name:           "Writer",
		InstanceNumber: 1,
		NodeID:         "writer-node-1",
		CreatedAt:      now,
	}
	writerTwo := session.AgentInstance{
		ID:             "writer-2",
		Name:           "Writer",
		InstanceNumber: 2,
		NodeID:         "writer-node-2",
		CreatedAt:      now,
	}
	reviewer := session.AgentInstance{
		ID:             "reviewer-1",
		Name:           "Reviewer",
		InstanceNumber: 1,
		NodeID:         "reviewer-node-1",
		CreatedAt:      now,
	}

	tasks := []agentworkspace.Task{
		{
			ID:             "task-1",
			To:             "Writer",
			AssignedNodeID: writerOne.NodeID,
			From:           "Writer",
			Description:    "Draft outline",
			Status:         agentworkspace.TaskStatusPending,
		},
		{
			ID:             "task-2",
			To:             "Writer",
			AssignedNodeID: writerTwo.NodeID,
			From:           "Reviewer",
			Description:    "Polish summary",
			Status:         agentworkspace.TaskStatusPending,
		},
		{
			ID:             "task-3",
			To:             "Reviewer",
			AssignedNodeID: reviewer.NodeID,
			From:           "Writer",
			Description:    "Final review",
			Status:         agentworkspace.TaskStatusPending,
		},
	}
	tasksJSON, err := json.Marshal(tasks)
	if err != nil {
		t.Fatalf("failed to marshal tasks: %v", err)
	}

	access := []agentworkspace.WorkspaceAgentMCPAccess{
		{AgentInstanceID: writerOne.ID, EnabledBindingIDs: []string{"filesystem"}},
		{AgentInstanceID: writerTwo.ID, EnabledBindingIDs: []string{"filesystem"}},
		{AgentInstanceID: reviewer.ID, EnabledBindingIDs: []string{"filesystem"}},
	}
	accessJSON, err := json.Marshal(access)
	if err != nil {
		t.Fatalf("failed to marshal mcp access: %v", err)
	}

	ws := &session.Workspace{
		ID:             "workspace-1",
		Name:           "Agent Cleanup",
		Agents:         []string{"Writer", "Reviewer"},
		AgentInstances: []session.AgentInstance{writerOne, writerTwo, reviewer},
		SharedData: map[string]any{
			"entry_agent_name": "Writer",
		},
		Layout: &session.CanvasLayout{
			AgentPositions: map[string]session.Position{
				writerOne.NodeID: {X: 10, Y: 20},
				writerTwo.NodeID: {X: 30, Y: 40},
				reviewer.NodeID:  {X: 50, Y: 60},
			},
			WorkflowConnections: []session.WorkflowConnectionLayout{
				{ID: "conn-1", From: writerOne.NodeID, To: "task-1"},
				{ID: "conn-2", From: "task-2", To: writerTwo.NodeID},
				{ID: "conn-3", From: reviewer.NodeID, To: "task-3"},
			},
		},
		TasksJSON:          tasksJSON,
		AgentMCPAccessJSON: accessJSON,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := handler.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/workspace-1/agents/Writer", nil)
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	updated, err := handler.store.GetWorkspace(context.Background(), ws.ID)
	if err != nil {
		t.Fatalf("failed to load updated workspace: %v", err)
	}

	if len(updated.AgentInstances) != 1 {
		t.Fatalf("expected 1 remaining agent instance, got %d", len(updated.AgentInstances))
	}
	if updated.AgentInstances[0].Name != "Reviewer" {
		t.Fatalf("expected reviewer to remain, got %q", updated.AgentInstances[0].Name)
	}
	if !updated.AgentInstances[0].EntryPoint {
		t.Fatalf("expected reviewer to be promoted to entry agent, got %#v", updated.AgentInstances[0])
	}
	if got := currentWorkspaceEntryAgentName(updated); got != "Reviewer" {
		t.Fatalf("expected entry agent to promote to Reviewer, got %q", got)
	}
	if len(updated.Agents) != 1 || updated.Agents[0] != "Reviewer" {
		t.Fatalf("expected legacy agents to contain only reviewer, got %#v", updated.Agents)
	}

	if updated.Layout == nil {
		t.Fatalf("expected layout to remain present")
	}
	if _, exists := updated.Layout.AgentPositions[writerOne.NodeID]; exists {
		t.Fatalf("expected writer one node position removed")
	}
	if _, exists := updated.Layout.AgentPositions[writerTwo.NodeID]; exists {
		t.Fatalf("expected writer two node position removed")
	}
	if _, exists := updated.Layout.AgentPositions[reviewer.NodeID]; !exists {
		t.Fatalf("expected reviewer node position preserved")
	}
	if len(updated.Layout.WorkflowConnections) != 1 || updated.Layout.WorkflowConnections[0].ID != "conn-3" {
		t.Fatalf("expected only reviewer workflow connection to remain, got %#v", updated.Layout.WorkflowConnections)
	}

	var updatedTasks []agentworkspace.Task
	if err := json.Unmarshal(updated.TasksJSON, &updatedTasks); err != nil {
		t.Fatalf("failed to decode updated tasks: %v", err)
	}
	if updatedTasks[0].To != "unassigned" || updatedTasks[0].AssignedNodeID != "" || updatedTasks[0].From != "" {
		t.Fatalf("expected task 1 to be unassigned and cleared, got %#v", updatedTasks[0])
	}
	if updatedTasks[1].To != "unassigned" || updatedTasks[1].AssignedNodeID != "" {
		t.Fatalf("expected task 2 to be unassigned, got %#v", updatedTasks[1])
	}
	if updatedTasks[2].To != "Reviewer" || updatedTasks[2].AssignedNodeID != reviewer.NodeID || updatedTasks[2].From != "" {
		t.Fatalf("expected reviewer task to stay assigned but clear removed sender, got %#v", updatedTasks[2])
	}

	var updatedAccess []agentworkspace.WorkspaceAgentMCPAccess
	if err := json.Unmarshal(updated.AgentMCPAccessJSON, &updatedAccess); err != nil {
		t.Fatalf("failed to decode updated mcp access: %v", err)
	}
	if len(updatedAccess) != 1 || updatedAccess[0].AgentInstanceID != reviewer.ID {
		t.Fatalf("expected only reviewer access entry to remain, got %#v", updatedAccess)
	}
}

func TestHandleWorkspaceAgents_AddSyncsWorkspaceJSON(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Portable Agents")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/"+workspaceID+"/agents",
		bytes.NewBufferString(`{"agent_name":"Writer"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	folderWS, err := fileStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("Get workspace from file store: %v", err)
	}
	if len(folderWS.AgentInstances) != 1 {
		t.Fatalf("expected 1 agent instance in workspace.json, got %#v", folderWS.AgentInstances)
	}
	if folderWS.AgentInstances[0].Name != "Writer" {
		t.Fatalf("expected Writer in workspace.json, got %#v", folderWS.AgentInstances[0])
	}
	if len(folderWS.Agents) != 1 || folderWS.Agents[0] != "Writer" {
		t.Fatalf("expected legacy agents to contain Writer in workspace.json, got %#v", folderWS.Agents)
	}
	if got := folderWS.EntryAgentName(); got != "Writer" {
		t.Fatalf("expected entry agent Writer in workspace.json, got %q", got)
	}
}

func TestHandleWorkspaceAgents_DeleteSyncsWorkspaceJSON(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Portable Cleanup")

	addAgent := func(agentName string) {
		t.Helper()
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/workspaces/"+workspaceID+"/agents",
			bytes.NewBufferString(`{"agent_name":"`+agentName+`"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.HandleWorkspaces(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 adding %s, got %d: %s", agentName, w.Code, w.Body.String())
		}
	}

	addAgent("Writer")
	addAgent("Reviewer")

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+workspaceID+"/agents/Writer", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting Writer, got %d: %s", w.Code, w.Body.String())
	}

	folderWS, err := fileStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("Get workspace from file store: %v", err)
	}
	if len(folderWS.AgentInstances) != 1 {
		t.Fatalf("expected 1 remaining agent instance in workspace.json, got %#v", folderWS.AgentInstances)
	}
	if folderWS.AgentInstances[0].Name != "Reviewer" || !folderWS.AgentInstances[0].EntryPoint {
		t.Fatalf("expected Reviewer to remain as entry point in workspace.json, got %#v", folderWS.AgentInstances[0])
	}
	if len(folderWS.Agents) != 1 || folderWS.Agents[0] != "Reviewer" {
		t.Fatalf("expected legacy agents to contain only Reviewer in workspace.json, got %#v", folderWS.Agents)
	}
	if got := folderWS.EntryAgentName(); got != "Reviewer" {
		t.Fatalf("expected entry agent Reviewer in workspace.json, got %q", got)
	}
}

func TestHandleWorkspaceAgents_DeleteRejectsRemovingLastEntryAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	now := time.Now()
	manager := session.AgentInstance{
		ID:             "manager-1",
		Name:           "Trip Planning Manager",
		InstanceNumber: 1,
		NodeID:         "trip-manager-node-1",
		EntryPoint:     true,
		CreatedAt:      now,
	}

	ws := &session.Workspace{
		ID:             "workspace-last-entry",
		Name:           "Trip Planning",
		Agents:         []string{"Trip Planning Manager"},
		AgentInstances: []session.AgentInstance{manager},
		SharedData: map[string]any{
			"entry_agent_name": "Trip Planning Manager",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := handler.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/workspace-last-entry/agents/manager-1", nil)
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when removing last entry agent, got %d: %s", w.Code, w.Body.String())
	}

	updated, err := handler.store.GetWorkspace(context.Background(), ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	if len(updated.AgentInstances) != 1 || !updated.AgentInstances[0].EntryPoint {
		t.Fatalf("expected last entry agent to remain intact, got %#v", updated.AgentInstances)
	}
}

func TestHandleWorkspaceAgents_DeleteSupportsNameInstanceIdentifier(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	now := time.Now()
	writerOne := session.AgentInstance{
		ID:             "writer-1",
		Name:           "Writer",
		InstanceNumber: 1,
		NodeID:         "writer-node-1",
		EntryPoint:     true,
		CreatedAt:      now,
	}
	writerTwo := session.AgentInstance{
		ID:             "writer-2",
		Name:           "Writer",
		InstanceNumber: 2,
		NodeID:         "writer-node-2",
		CreatedAt:      now,
	}
	reviewer := session.AgentInstance{
		ID:             "reviewer-1",
		Name:           "Reviewer",
		InstanceNumber: 1,
		NodeID:         "reviewer-node-1",
		CreatedAt:      now,
	}

	ws := &session.Workspace{
		ID:             "workspace-instance-delete",
		Name:           "Instance Delete",
		Agents:         []string{"Writer", "Reviewer"},
		AgentInstances: []session.AgentInstance{writerOne, writerTwo, reviewer},
		SharedData: map[string]any{
			"entry_agent_name": "Writer",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := handler.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/workspace-instance-delete/agents/Writer:2", nil)
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	updated, err := handler.store.GetWorkspace(context.Background(), ws.ID)
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}

	if len(updated.AgentInstances) != 2 {
		t.Fatalf("expected 2 remaining agent instances, got %d", len(updated.AgentInstances))
	}
	for _, inst := range updated.AgentInstances {
		if inst.ID == writerTwo.ID {
			t.Fatalf("expected writer instance 2 to be removed, got %#v", updated.AgentInstances)
		}
	}
	if got := currentWorkspaceEntryAgentName(updated); got != "Writer" {
		t.Fatalf("expected Writer to remain entry agent, got %q", got)
	}
}

func TestGetWorkspaceIncludesEntryAgentMetadata(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	if err := handler.agentStore.CreateAgent("Writer", &agentstore.CreateAgentConfig{}); err != nil {
		t.Fatalf("failed to create workspace agent: %v", err)
	}

	now := time.Now()
	tasks := []agentworkspace.Task{
		{
			ID:             "task-1",
			To:             "Writer",
			AssignedNodeID: "writer-node-1",
			Description:    "Draft itinerary",
			Status:         agentworkspace.TaskStatusCompleted,
		},
	}
	tasksJSON, err := json.Marshal(tasks)
	if err != nil {
		t.Fatalf("failed to marshal tasks: %v", err)
	}

	ws := &session.Workspace{
		ID:        "workspace-detail",
		Name:      "Workspace Detail",
		Agents:    []string{"Writer"},
		Status:    session.WorkspaceStatusActive,
		TasksJSON: tasksJSON,
		AgentInstances: []session.AgentInstance{
			{
				ID:             "writer-1",
				Name:           "Writer",
				InstanceNumber: 1,
				NodeID:         "writer-node-1",
				EntryPoint:     true,
				CreatedAt:      now,
			},
		},
		SharedData: map[string]any{
			"entry_agent_name": "Writer",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := handler.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-detail", nil)
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if got := response["entry_agent_name"]; got != "Writer" {
		t.Fatalf("expected entry_agent_name Writer, got %#v", got)
	}

	agentStats, ok := response["agent_stats"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent_stats object, got %#v", response["agent_stats"])
	}
	if _, ok := agentStats["Writer"]; !ok {
		t.Fatalf("expected Writer stats, got %#v", agentStats)
	}

	workspaceProgress, ok := response["workspace_progress"].(map[string]any)
	if !ok {
		t.Fatalf("expected workspace_progress object, got %#v", response["workspace_progress"])
	}
	if got := workspaceProgress["total_tasks"]; got != float64(1) {
		t.Fatalf("expected total_tasks 1, got %#v", got)
	}
}
