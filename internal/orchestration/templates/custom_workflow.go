package templates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// MaxWorkflowNodes is the maximum number of nodes allowed in a custom workflow
const MaxWorkflowNodes = 20

// WorkflowSource indicates whether a workflow is built-in or custom
type WorkflowSource string

const (
	WorkflowSourceBuiltin WorkflowSource = "builtin"
	WorkflowSourceCustom  WorkflowSource = "custom"
)

// CustomWorkflow represents a user-created workflow from canvas node selection
type CustomWorkflow struct {
	ID                  string               `json:"id"`
	Name                string               `json:"name"`
	Description         string               `json:"description,omitempty"`
	Category            string               `json:"category,omitempty"`
	Source              WorkflowSource       `json:"source"`
	Nodes               []WorkflowNode       `json:"nodes"`
	InternalConnections []WorkflowConnection `json:"internal_connections"`
	InputPorts          []WorkflowPort       `json:"input_ports,omitempty"`
	OutputPorts         []WorkflowPort       `json:"output_ports,omitempty"`
	Layout              WorkflowLayout       `json:"layout"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

// WorkflowNodeType represents the type of node in a workflow
type WorkflowNodeType string

const (
	NodeTypeTask       WorkflowNodeType = "task"
	NodeTypeAgent      WorkflowNodeType = "agent"
	NodeTypeScheduler  WorkflowNodeType = "scheduler"
	NodeTypeStore      WorkflowNodeType = "store"
	NodeTypeAttachment WorkflowNodeType = "attachment"
)

// WorkflowNode represents a single node in a custom workflow
type WorkflowNode struct {
	ID        string                 `json:"id"`
	Type      WorkflowNodeType       `json:"type"`
	Config    map[string]interface{} `json:"config"`
	RelativeX float64                `json:"relative_x"`
	RelativeY float64                `json:"relative_y"`
}

// WorkflowConnection represents a connection between two nodes within a workflow
type WorkflowConnection struct {
	ID       string `json:"id"`
	FromNode string `json:"from_node"`
	FromPort string `json:"from_port"`
	ToNode   string `json:"to_node"`
	ToPort   string `json:"to_port"`
}

// WorkflowPortType indicates whether a port is for input or output
type WorkflowPortType string

const (
	PortTypeInput  WorkflowPortType = "input"
	PortTypeOutput WorkflowPortType = "output"
)

// WorkflowPort represents an external connection point on a workflow
type WorkflowPort struct {
	ID     string           `json:"id"`
	NodeID string           `json:"node_id"`
	PortID string           `json:"port_id"`
	Type   WorkflowPortType `json:"type"`
	Label  string           `json:"label,omitempty"`
}

// WorkflowLayout stores the layout information for visual reconstruction
type WorkflowLayout struct {
	Width         float64            `json:"width"`
	Height        float64            `json:"height"`
	NodePositions map[string]NodePos `json:"node_positions"`
}

// NodePos represents a node's position
type NodePos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// NewCustomWorkflow creates a new CustomWorkflow with a generated ID
func NewCustomWorkflow(name, description, category string) *CustomWorkflow {
	now := time.Now()
	return &CustomWorkflow{
		ID:                  uuid.New().String(),
		Name:                name,
		Description:         description,
		Category:            category,
		Source:              WorkflowSourceCustom,
		Nodes:               make([]WorkflowNode, 0),
		InternalConnections: make([]WorkflowConnection, 0),
		InputPorts:          make([]WorkflowPort, 0),
		OutputPorts:         make([]WorkflowPort, 0),
		Layout: WorkflowLayout{
			NodePositions: make(map[string]NodePos),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// Validate checks if the workflow is valid
func (w *CustomWorkflow) Validate() error {
	if w.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	if len(w.Nodes) == 0 {
		return fmt.Errorf("workflow must contain at least one node")
	}

	if len(w.Nodes) > MaxWorkflowNodes {
		return fmt.Errorf("workflow exceeds maximum node count of %d (has %d nodes)", MaxWorkflowNodes, len(w.Nodes))
	}

	// Build a set of node IDs for connection validation
	nodeIDs := make(map[string]bool)
	for _, node := range w.Nodes {
		if node.ID == "" {
			return fmt.Errorf("node ID is required")
		}
		if nodeIDs[node.ID] {
			return fmt.Errorf("duplicate node ID: %s", node.ID)
		}
		nodeIDs[node.ID] = true
	}

	// Validate connections reference existing nodes
	for _, conn := range w.InternalConnections {
		if !nodeIDs[conn.FromNode] {
			return fmt.Errorf("connection references non-existent source node: %s", conn.FromNode)
		}
		if !nodeIDs[conn.ToNode] {
			return fmt.Errorf("connection references non-existent target node: %s", conn.ToNode)
		}
	}

	// Validate input/output ports reference existing nodes
	for _, port := range w.InputPorts {
		if !nodeIDs[port.NodeID] {
			return fmt.Errorf("input port references non-existent node: %s", port.NodeID)
		}
	}
	for _, port := range w.OutputPorts {
		if !nodeIDs[port.NodeID] {
			return fmt.Errorf("output port references non-existent node: %s", port.NodeID)
		}
	}

	return nil
}

// GetAgentNodeIDs returns the node IDs of all agent-type nodes in the workflow
func (w *CustomWorkflow) GetAgentNodeIDs() []string {
	var agentIDs []string
	for _, node := range w.Nodes {
		if node.Type == NodeTypeAgent {
			agentIDs = append(agentIDs, node.ID)
		}
	}
	return agentIDs
}

// GetAgentNames returns the names of all agents referenced in the workflow
func (w *CustomWorkflow) GetAgentNames() []string {
	agentNames := make(map[string]bool)

	for _, node := range w.Nodes {
		// Check agent nodes
		if node.Type == NodeTypeAgent {
			if name, ok := node.Config["name"].(string); ok && name != "" {
				agentNames[name] = true
			}
		}
		// Check task nodes for agent assignments
		if node.Type == NodeTypeTask {
			if assignedTo, ok := node.Config["to"].(string); ok && assignedTo != "" && assignedTo != "unassigned" {
				agentNames[assignedTo] = true
			}
		}
	}

	names := make([]string, 0, len(agentNames))
	for name := range agentNames {
		names = append(names, name)
	}
	return names
}

// CustomWorkflowManager manages custom workflows
type CustomWorkflowManager struct {
	workflowsDir string
	workflows    map[string]*CustomWorkflow
}

// NewCustomWorkflowManager creates a new custom workflow manager
func NewCustomWorkflowManager(workflowsDir string) *CustomWorkflowManager {
	return &CustomWorkflowManager{
		workflowsDir: workflowsDir,
		workflows:    make(map[string]*CustomWorkflow),
	}
}

// LoadWorkflows loads all custom workflows from the workflows directory
func (m *CustomWorkflowManager) LoadWorkflows() error {
	// Create workflows directory if it doesn't exist
	if err := os.MkdirAll(m.workflowsDir, 0755); err != nil {
		return fmt.Errorf("failed to create workflows directory: %w", err)
	}

	// Load workflows from disk
	files, err := os.ReadDir(m.workflowsDir)
	if err != nil {
		return fmt.Errorf("failed to read workflows directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}

		workflowPath := filepath.Join(m.workflowsDir, file.Name())
		data, err := os.ReadFile(workflowPath)
		if err != nil {
			continue
		}

		var workflow CustomWorkflow
		if err := json.Unmarshal(data, &workflow); err != nil {
			continue
		}

		m.workflows[workflow.ID] = &workflow
	}

	return nil
}

// SaveWorkflow saves a custom workflow to disk
func (m *CustomWorkflowManager) SaveWorkflow(workflow *CustomWorkflow) error {
	// Validate before saving
	if err := workflow.Validate(); err != nil {
		return fmt.Errorf("invalid workflow: %w", err)
	}

	workflow.UpdatedAt = time.Now()
	if workflow.CreatedAt.IsZero() {
		workflow.CreatedAt = time.Now()
	}

	// Ensure source is set to custom
	workflow.Source = WorkflowSourceCustom

	data, err := json.MarshalIndent(workflow, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workflow: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(m.workflowsDir, 0755); err != nil {
		return fmt.Errorf("failed to create workflows directory: %w", err)
	}

	workflowPath := filepath.Join(m.workflowsDir, workflow.ID+".json")
	if err := os.WriteFile(workflowPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write workflow file: %w", err)
	}

	m.workflows[workflow.ID] = workflow
	return nil
}

// GetWorkflow retrieves a workflow by ID
func (m *CustomWorkflowManager) GetWorkflow(id string) (*CustomWorkflow, error) {
	workflow, exists := m.workflows[id]
	if !exists {
		return nil, fmt.Errorf("workflow not found: %s", id)
	}
	return workflow, nil
}

// ListWorkflows returns all custom workflows
func (m *CustomWorkflowManager) ListWorkflows() []*CustomWorkflow {
	workflows := make([]*CustomWorkflow, 0, len(m.workflows))
	for _, workflow := range m.workflows {
		workflows = append(workflows, workflow)
	}
	return workflows
}

// DeleteWorkflow deletes a custom workflow
func (m *CustomWorkflowManager) DeleteWorkflow(id string) error {
	if _, exists := m.workflows[id]; !exists {
		return fmt.Errorf("workflow not found: %s", id)
	}

	workflowPath := filepath.Join(m.workflowsDir, id+".json")
	if err := os.Remove(workflowPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete workflow file: %w", err)
	}

	delete(m.workflows, id)
	return nil
}

// CheckAgentAvailability checks which agents from the workflow are missing from the provided list
func (m *CustomWorkflowManager) CheckAgentAvailability(workflow *CustomWorkflow, availableAgents []string) []string {
	availableSet := make(map[string]bool)
	for _, agent := range availableAgents {
		availableSet[agent] = true
	}

	requiredAgents := workflow.GetAgentNames()
	var missingAgents []string

	for _, agent := range requiredAgents {
		if !availableSet[agent] {
			missingAgents = append(missingAgents, agent)
		}
	}

	return missingAgents
}
