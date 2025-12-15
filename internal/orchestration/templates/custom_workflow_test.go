package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNewCustomWorkflow(t *testing.T) {
	workflow := NewCustomWorkflow("Test Workflow", "A test workflow", "testing")

	if workflow.ID == "" {
		t.Error("expected workflow ID to be generated")
	}
	if workflow.Name != "Test Workflow" {
		t.Errorf("expected name 'Test Workflow', got '%s'", workflow.Name)
	}
	if workflow.Description != "A test workflow" {
		t.Errorf("expected description 'A test workflow', got '%s'", workflow.Description)
	}
	if workflow.Category != "testing" {
		t.Errorf("expected category 'testing', got '%s'", workflow.Category)
	}
	if workflow.Source != WorkflowSourceCustom {
		t.Errorf("expected source 'custom', got '%s'", workflow.Source)
	}
	if workflow.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if workflow.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestCustomWorkflow_Validate(t *testing.T) {
	tests := []struct {
		name        string
		workflow    *CustomWorkflow
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid workflow",
			workflow: &CustomWorkflow{
				Name: "Valid Workflow",
				Nodes: []WorkflowNode{
					{ID: "node-1", Type: NodeTypeTask, Config: map[string]interface{}{}},
				},
			},
			expectError: false,
		},
		{
			name: "missing name",
			workflow: &CustomWorkflow{
				Name: "",
				Nodes: []WorkflowNode{
					{ID: "node-1", Type: NodeTypeTask, Config: map[string]interface{}{}},
				},
			},
			expectError: true,
			errorMsg:    "workflow name is required",
		},
		{
			name: "no nodes",
			workflow: &CustomWorkflow{
				Name:  "Empty Workflow",
				Nodes: []WorkflowNode{},
			},
			expectError: true,
			errorMsg:    "workflow must contain at least one node",
		},
		{
			name: "too many nodes",
			workflow: func() *CustomWorkflow {
				nodes := make([]WorkflowNode, MaxWorkflowNodes+1)
				for i := range nodes {
					nodes[i] = WorkflowNode{
						ID:     fmt.Sprintf("node-%d", i),
						Type:   NodeTypeTask,
						Config: map[string]interface{}{},
					}
				}
				return &CustomWorkflow{
					Name:  "Too Many Nodes",
					Nodes: nodes,
				}
			}(),
			expectError: true,
			errorMsg:    "workflow exceeds maximum node count",
		},
		{
			name: "duplicate node ID",
			workflow: &CustomWorkflow{
				Name: "Duplicate IDs",
				Nodes: []WorkflowNode{
					{ID: "node-1", Type: NodeTypeTask, Config: map[string]interface{}{}},
					{ID: "node-1", Type: NodeTypeAgent, Config: map[string]interface{}{}},
				},
			},
			expectError: true,
			errorMsg:    "duplicate node ID",
		},
		{
			name: "invalid connection - missing source",
			workflow: &CustomWorkflow{
				Name: "Bad Connection",
				Nodes: []WorkflowNode{
					{ID: "node-1", Type: NodeTypeTask, Config: map[string]interface{}{}},
				},
				InternalConnections: []WorkflowConnection{
					{ID: "conn-1", FromNode: "nonexistent", ToNode: "node-1"},
				},
			},
			expectError: true,
			errorMsg:    "connection references non-existent source node",
		},
		{
			name: "invalid connection - missing target",
			workflow: &CustomWorkflow{
				Name: "Bad Connection",
				Nodes: []WorkflowNode{
					{ID: "node-1", Type: NodeTypeTask, Config: map[string]interface{}{}},
				},
				InternalConnections: []WorkflowConnection{
					{ID: "conn-1", FromNode: "node-1", ToNode: "nonexistent"},
				},
			},
			expectError: true,
			errorMsg:    "connection references non-existent target node",
		},
		{
			name: "valid workflow with connections",
			workflow: &CustomWorkflow{
				Name: "Connected Workflow",
				Nodes: []WorkflowNode{
					{ID: "node-1", Type: NodeTypeTask, Config: map[string]interface{}{}},
					{ID: "node-2", Type: NodeTypeTask, Config: map[string]interface{}{}},
				},
				InternalConnections: []WorkflowConnection{
					{ID: "conn-1", FromNode: "node-1", FromPort: "out", ToNode: "node-2", ToPort: "in"},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.workflow.Validate()
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error containing '%s', got nil", tt.errorMsg)
				} else if !contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got '%s'", err.Error())
				}
			}
		})
	}
}

func TestCustomWorkflow_GetAgentNames(t *testing.T) {
	workflow := &CustomWorkflow{
		Name: "Test Workflow",
		Nodes: []WorkflowNode{
			{
				ID:   "agent-1",
				Type: NodeTypeAgent,
				Config: map[string]interface{}{
					"name": "researcher",
				},
			},
			{
				ID:   "task-1",
				Type: NodeTypeTask,
				Config: map[string]interface{}{
					"to":          "analyzer",
					"description": "Analyze data",
				},
			},
			{
				ID:   "task-2",
				Type: NodeTypeTask,
				Config: map[string]interface{}{
					"to":          "unassigned",
					"description": "Pending task",
				},
			},
			{
				ID:   "task-3",
				Type: NodeTypeTask,
				Config: map[string]interface{}{
					"to":          "researcher", // Duplicate, should not appear twice
					"description": "Research task",
				},
			},
		},
	}

	names := workflow.GetAgentNames()

	// Should have "researcher" and "analyzer" (not "unassigned")
	if len(names) != 2 {
		t.Errorf("expected 2 agent names, got %d: %v", len(names), names)
	}

	nameSet := make(map[string]bool)
	for _, name := range names {
		nameSet[name] = true
	}

	if !nameSet["researcher"] {
		t.Error("expected 'researcher' in agent names")
	}
	if !nameSet["analyzer"] {
		t.Error("expected 'analyzer' in agent names")
	}
	if nameSet["unassigned"] {
		t.Error("'unassigned' should not be in agent names")
	}
}

func TestCustomWorkflowManager_SaveAndLoad(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "workflow-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager := NewCustomWorkflowManager(tmpDir)

	// Create and save a workflow
	workflow := NewCustomWorkflow("Test Workflow", "Description", "test")
	workflow.Nodes = []WorkflowNode{
		{ID: "node-1", Type: NodeTypeTask, Config: map[string]interface{}{"description": "Task 1"}},
		{ID: "node-2", Type: NodeTypeAgent, Config: map[string]interface{}{"name": "agent1"}},
	}
	workflow.InternalConnections = []WorkflowConnection{
		{ID: "conn-1", FromNode: "node-1", FromPort: "out", ToNode: "node-2", ToPort: "in"},
	}

	err = manager.SaveWorkflow(workflow)
	if err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	// Verify file was created
	workflowPath := filepath.Join(tmpDir, workflow.ID+".json")
	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		t.Error("workflow file was not created")
	}

	// Create new manager and load
	manager2 := NewCustomWorkflowManager(tmpDir)
	err = manager2.LoadWorkflows()
	if err != nil {
		t.Fatalf("failed to load workflows: %v", err)
	}

	// Verify workflow was loaded
	loaded, err := manager2.GetWorkflow(workflow.ID)
	if err != nil {
		t.Fatalf("failed to get workflow: %v", err)
	}

	if loaded.Name != workflow.Name {
		t.Errorf("expected name '%s', got '%s'", workflow.Name, loaded.Name)
	}
	if len(loaded.Nodes) != len(workflow.Nodes) {
		t.Errorf("expected %d nodes, got %d", len(workflow.Nodes), len(loaded.Nodes))
	}
	if len(loaded.InternalConnections) != len(workflow.InternalConnections) {
		t.Errorf("expected %d connections, got %d", len(workflow.InternalConnections), len(loaded.InternalConnections))
	}
}

func TestCustomWorkflowManager_Delete(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "workflow-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager := NewCustomWorkflowManager(tmpDir)

	// Create and save a workflow
	workflow := NewCustomWorkflow("Test Workflow", "Description", "test")
	workflow.Nodes = []WorkflowNode{
		{ID: "node-1", Type: NodeTypeTask, Config: map[string]interface{}{}},
	}

	err = manager.SaveWorkflow(workflow)
	if err != nil {
		t.Fatalf("failed to save workflow: %v", err)
	}

	// Delete the workflow
	err = manager.DeleteWorkflow(workflow.ID)
	if err != nil {
		t.Fatalf("failed to delete workflow: %v", err)
	}

	// Verify file was deleted
	workflowPath := filepath.Join(tmpDir, workflow.ID+".json")
	if _, err := os.Stat(workflowPath); !os.IsNotExist(err) {
		t.Error("workflow file was not deleted")
	}

	// Verify workflow is no longer in manager
	_, err = manager.GetWorkflow(workflow.ID)
	if err == nil {
		t.Error("expected error when getting deleted workflow")
	}
}

func TestCustomWorkflowManager_CheckAgentAvailability(t *testing.T) {
	manager := NewCustomWorkflowManager("")

	workflow := &CustomWorkflow{
		Name: "Test Workflow",
		Nodes: []WorkflowNode{
			{ID: "agent-1", Type: NodeTypeAgent, Config: map[string]interface{}{"name": "researcher"}},
			{ID: "task-1", Type: NodeTypeTask, Config: map[string]interface{}{"to": "analyzer"}},
			{ID: "task-2", Type: NodeTypeTask, Config: map[string]interface{}{"to": "synthesizer"}},
		},
	}

	// Test with all agents available
	availableAgents := []string{"researcher", "analyzer", "synthesizer", "validator"}
	missing := manager.CheckAgentAvailability(workflow, availableAgents)
	if len(missing) != 0 {
		t.Errorf("expected no missing agents, got %v", missing)
	}

	// Test with some agents missing
	availableAgents = []string{"researcher"}
	missing = manager.CheckAgentAvailability(workflow, availableAgents)
	if len(missing) != 2 {
		t.Errorf("expected 2 missing agents, got %d: %v", len(missing), missing)
	}

	missingSet := make(map[string]bool)
	for _, agent := range missing {
		missingSet[agent] = true
	}
	if !missingSet["analyzer"] {
		t.Error("expected 'analyzer' in missing agents")
	}
	if !missingSet["synthesizer"] {
		t.Error("expected 'synthesizer' in missing agents")
	}
}

func TestCustomWorkflowManager_ListWorkflows(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "workflow-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager := NewCustomWorkflowManager(tmpDir)

	// Initially empty
	workflows := manager.ListWorkflows()
	if len(workflows) != 0 {
		t.Errorf("expected 0 workflows, got %d", len(workflows))
	}

	// Add some workflows
	for i := 0; i < 3; i++ {
		workflow := NewCustomWorkflow(fmt.Sprintf("Workflow %d", i), "", "test")
		workflow.Nodes = []WorkflowNode{
			{ID: "node-1", Type: NodeTypeTask, Config: map[string]interface{}{}},
		}
		if err := manager.SaveWorkflow(workflow); err != nil {
			t.Fatalf("failed to save workflow: %v", err)
		}
	}

	// Verify count
	workflows = manager.ListWorkflows()
	if len(workflows) != 3 {
		t.Errorf("expected 3 workflows, got %d", len(workflows))
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
