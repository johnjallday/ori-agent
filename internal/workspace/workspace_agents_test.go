package workspace

import (
	"errors"
	"reflect"
	"testing"
)

func TestWorkspaceAddAgentRejectsDuplicateName(t *testing.T) {
	ws := &Workspace{}

	if err := ws.AddAgent("Writer"); err != nil {
		t.Fatalf("AddAgent(Writer) error = %v", err)
	}

	err := ws.AddAgent(" writer ")
	if !errors.Is(err, ErrAgentAlreadyInWorkspace) {
		t.Fatalf("AddAgent(duplicate) error = %v, want %v", err, ErrAgentAlreadyInWorkspace)
	}

	if len(ws.AgentInstances) != 1 {
		t.Fatalf("expected 1 agent instance, got %d", len(ws.AgentInstances))
	}

	if got := ws.AgentInstances[0].NodeID; got != "Writer-node-1" {
		t.Fatalf("expected canonical node id Writer-node-1, got %q", got)
	}

	if got := ws.Agents; !reflect.DeepEqual(got, []string{"Writer"}) {
		t.Fatalf("expected Agents to contain one Writer entry, got %#v", got)
	}
}

func TestNormalizeAgentInstancesDedupesAndRewritesReferences(t *testing.T) {
	ws := &Workspace{
		ID:     "ws-1",
		Agents: []string{"Writer", "Writer", "Reviewer"},
		AgentInstances: []AgentInstance{
			{ID: "writer-1", Name: "Writer", InstanceNumber: 2, NodeID: "Writer-node-2"},
			{ID: "writer-2", Name: "Writer", InstanceNumber: 3, NodeID: "Writer-node-3"},
			{ID: "reviewer-1", Name: "Reviewer", InstanceNumber: 1, NodeID: "Reviewer-node-1"},
		},
		Tasks: []Task{
			{ID: "task-1", To: "Writer", AssignedNodeID: "Writer-node-3"},
			{ID: "task-2", To: "writer", AssignedNodeID: ""},
			{ID: "task-3", To: "Reviewer", AssignedNodeID: ""},
		},
		StoreNodes: []StoreNode{
			{ID: "store-1", AgentNodeID: "Writer-node-3"},
		},
		AgentMCPAccess: []WorkspaceAgentMCPAccess{
			{AgentInstanceID: "writer-1", EnabledBindingIDs: []string{"filesystem"}},
			{AgentInstanceID: "writer-2", EnabledBindingIDs: []string{"github"}},
		},
		AgentSkillAccess: []WorkspaceAgentSkillAccess{
			{AgentInstanceID: "writer-1", EnabledBindingIDs: []string{"skill-a"}},
			{AgentInstanceID: "writer-2", EnabledBindingIDs: []string{"skill-b"}},
		},
		Layout: &CanvasLayout{
			AgentPositions: map[string]Position{
				"Writer-node-2":   {X: 10, Y: 10},
				"Writer-node-3":   {X: 20, Y: 20},
				"Reviewer-node-1": {X: 30, Y: 30},
			},
			WorkflowConnections: []WorkflowConnectionLayout{
				{ID: "conn-1", From: "Writer-node-3", To: "task-1"},
				{ID: "conn-2", From: "task-2", To: "Writer-node-3"},
			},
		},
	}

	if changed := ws.NormalizeAgentInstances(); !changed {
		t.Fatal("NormalizeAgentInstances() = false, want true")
	}

	if len(ws.AgentInstances) != 2 {
		t.Fatalf("expected 2 agent instances after normalization, got %d", len(ws.AgentInstances))
	}

	writer := ws.AgentInstances[0]
	if writer.Name != "Writer" {
		t.Fatalf("expected first canonical instance to be Writer, got %q", writer.Name)
	}
	if writer.InstanceNumber != 1 {
		t.Fatalf("expected Writer instance number 1, got %d", writer.InstanceNumber)
	}
	if writer.NodeID != "Writer-node-1" {
		t.Fatalf("expected Writer node id Writer-node-1, got %q", writer.NodeID)
	}

	if got := ws.Agents; !reflect.DeepEqual(got, []string{"Writer", "Reviewer"}) {
		t.Fatalf("expected deduped Agents list, got %#v", got)
	}

	if ws.Tasks[0].AssignedNodeID != "Writer-node-1" {
		t.Fatalf("expected task-1 assigned node to normalize, got %q", ws.Tasks[0].AssignedNodeID)
	}
	if ws.Tasks[1].To != "Writer" || ws.Tasks[1].AssignedNodeID != "Writer-node-1" {
		t.Fatalf("expected task-2 to normalize to Writer-node-1, got to=%q assigned=%q", ws.Tasks[1].To, ws.Tasks[1].AssignedNodeID)
	}
	if ws.Tasks[2].AssignedNodeID != "Reviewer-node-1" {
		t.Fatalf("expected reviewer task to receive canonical node id, got %q", ws.Tasks[2].AssignedNodeID)
	}

	if ws.StoreNodes[0].AgentNodeID != "Writer-node-1" {
		t.Fatalf("expected store node agent node id to normalize, got %q", ws.StoreNodes[0].AgentNodeID)
	}

	if _, exists := ws.Layout.AgentPositions["Writer-node-3"]; exists {
		t.Fatal("expected duplicate Writer-node-3 position to be removed")
	}
	if _, exists := ws.Layout.AgentPositions["Writer-node-1"]; !exists {
		t.Fatal("expected canonical Writer-node-1 position to exist")
	}
	if ws.Layout.WorkflowConnections[0].From != "Writer-node-1" {
		t.Fatalf("expected workflow connection from node to normalize, got %q", ws.Layout.WorkflowConnections[0].From)
	}
	if ws.Layout.WorkflowConnections[1].To != "Writer-node-1" {
		t.Fatalf("expected workflow connection to node to normalize, got %q", ws.Layout.WorkflowConnections[1].To)
	}

	if len(ws.AgentMCPAccess) != 1 {
		t.Fatalf("expected merged MCP access entry, got %d", len(ws.AgentMCPAccess))
	}
	if ws.AgentMCPAccess[0].AgentInstanceID != "writer-1" {
		t.Fatalf("expected canonical MCP access to keep first writer instance id, got %q", ws.AgentMCPAccess[0].AgentInstanceID)
	}
	if got := ws.AgentMCPAccess[0].EnabledBindingIDs; !reflect.DeepEqual(got, []string{"filesystem", "github"}) {
		t.Fatalf("expected merged MCP bindings, got %#v", got)
	}

	if len(ws.AgentSkillAccess) != 1 {
		t.Fatalf("expected merged skill access entry, got %d", len(ws.AgentSkillAccess))
	}
	if ws.AgentSkillAccess[0].AgentInstanceID != "writer-1" {
		t.Fatalf("expected canonical skill access to keep first writer instance id, got %q", ws.AgentSkillAccess[0].AgentInstanceID)
	}
	if got := ws.AgentSkillAccess[0].EnabledBindingIDs; !reflect.DeepEqual(got, []string{"skill-a", "skill-b"}) {
		t.Fatalf("expected merged skill bindings, got %#v", got)
	}
}
