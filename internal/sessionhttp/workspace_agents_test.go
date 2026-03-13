package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
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
