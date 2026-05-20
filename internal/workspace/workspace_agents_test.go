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

func TestWorkspaceAddAgentSetsFirstEntryAgent(t *testing.T) {
	ws := &Workspace{}

	if err := ws.AddAgent("Trip Planning Manager"); err != nil {
		t.Fatalf("AddAgent() error = %v", err)
	}

	if got := ws.EntryAgentName(); got != "Trip Planning Manager" {
		t.Fatalf("EntryAgentName() = %q, want %q", got, "Trip Planning Manager")
	}
	if len(ws.AgentInstances) != 1 || !ws.AgentInstances[0].EntryPoint {
		t.Fatalf("expected first agent instance to be entry point, got %#v", ws.AgentInstances)
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

func TestWorkspaceEntryAgentNameUsesConfiguredEntryAgent(t *testing.T) {
	ws := &Workspace{
		Agents: []string{"Trip Planning Manager", "Trip Planner"},
		AgentInstances: []AgentInstance{
			{Name: "Trip Planning Manager", NodeID: "trip-manager-node", EntryPoint: true},
			{Name: "Trip Planner", NodeID: "trip-planner-node"},
		},
		SharedData: map[string]any{},
	}

	if err := ws.SetEntryAgentName("Trip Planning Manager"); err != nil {
		t.Fatalf("SetEntryAgentName() error = %v", err)
	}

	if got := ws.EntryAgentName(); got != "Trip Planning Manager" {
		t.Fatalf("EntryAgentName() = %q, want %q", got, "Trip Planning Manager")
	}

	if ws.SharedData["entry_agent_name"] != "Trip Planning Manager" {
		t.Fatalf("expected shared entry agent name to be stored, got %#v", ws.SharedData["entry_agent_name"])
	}
}

func TestWorkspaceEntryAgentNameFallsBackToEntryPointInstance(t *testing.T) {
	ws := &Workspace{
		Agents: []string{"Music Project Manager", "DAW Agent"},
		AgentInstances: []AgentInstance{
			{Name: "Music Project Manager", NodeID: "manager-node", EntryPoint: true},
			{Name: "DAW Agent", NodeID: "daw-node"},
		},
	}

	if got := ws.EntryAgentName(); got != "Music Project Manager" {
		t.Fatalf("EntryAgentName() = %q, want %q", got, "Music Project Manager")
	}
}

func TestNormalizeAgentInstancesPreservesEntryPointMetadata(t *testing.T) {
	ws := &Workspace{
		Agents: []string{"Portfolio Manager", "Portfolio Manager"},
		AgentInstances: []AgentInstance{
			{ID: "manager-1", Name: "Portfolio Manager", InstanceNumber: 1, NodeID: "Portfolio Manager-node-1"},
			{ID: "manager-2", Name: "Portfolio Manager", InstanceNumber: 2, NodeID: "Portfolio Manager-node-2", Role: "Manager", Description: "Primary entry point", EntryPoint: true},
		},
	}

	if changed := ws.NormalizeAgentInstances(); !changed {
		t.Fatal("NormalizeAgentInstances() = false, want true")
	}

	if len(ws.AgentInstances) != 1 {
		t.Fatalf("expected 1 canonical agent instance, got %d", len(ws.AgentInstances))
	}

	got := ws.AgentInstances[0]
	if got.Role != "Manager" {
		t.Fatalf("expected canonical role Manager, got %q", got.Role)
	}
	if got.Description != "Primary entry point" {
		t.Fatalf("expected canonical description to be preserved, got %q", got.Description)
	}
	if !got.EntryPoint {
		t.Fatal("expected canonical instance to remain entry_point")
	}
}

func TestWorkspaceRemoveAgentInstancePromotesNextEntryAgent(t *testing.T) {
	ws := &Workspace{}
	if err := ws.AddAgent("Portfolio Manager"); err != nil {
		t.Fatalf("AddAgent(first) error = %v", err)
	}
	if err := ws.AddAgent("Chart Analysis Agent"); err != nil {
		t.Fatalf("AddAgent(second) error = %v", err)
	}

	entryInstanceID := ws.AgentInstances[0].ID
	if err := ws.RemoveAgentInstance(entryInstanceID); err != nil {
		t.Fatalf("RemoveAgentInstance() error = %v", err)
	}

	if got := ws.EntryAgentName(); got != "Chart Analysis Agent" {
		t.Fatalf("EntryAgentName() = %q, want %q", got, "Chart Analysis Agent")
	}
	if len(ws.AgentInstances) != 1 || !ws.AgentInstances[0].EntryPoint {
		t.Fatalf("expected remaining agent instance to become entry point, got %#v", ws.AgentInstances)
	}
}

func TestWorkspaceRemoveAgentInstanceRejectsRemovingLastEntryAgent(t *testing.T) {
	ws := &Workspace{}
	if err := ws.AddAgent("Music Project Manager"); err != nil {
		t.Fatalf("AddAgent() error = %v", err)
	}

	err := ws.RemoveAgentInstance(ws.AgentInstances[0].ID)
	if !errors.Is(err, ErrWorkspaceEntryAgentRequired) {
		t.Fatalf("RemoveAgentInstance(last entry) error = %v, want %v", err, ErrWorkspaceEntryAgentRequired)
	}
	if got := ws.EntryAgentName(); got != "Music Project Manager" {
		t.Fatalf("EntryAgentName() after rejected removal = %q, want %q", got, "Music Project Manager")
	}
}
