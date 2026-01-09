package workspace

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// AddAgent adds a new agent instance to the workspace
func (w *Workspace) AddAgent(agentName string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Count existing instances of this agent type to get next instance number
	instanceNumber := 1
	for _, inst := range w.AgentInstances {
		if inst.Name == agentName && inst.InstanceNumber >= instanceNumber {
			instanceNumber = inst.InstanceNumber + 1
		}
	}

	// Create new agent instance with stable ID
	instance := AgentInstance{
		ID:             uuid.New().String(),
		Name:           agentName,
		InstanceNumber: instanceNumber,
		NodeID:         fmt.Sprintf("%s-node-%d", agentName, instanceNumber),
		CreatedAt:      time.Now(),
	}

	w.AgentInstances = append(w.AgentInstances, instance)

	// Also update legacy Agents array for backward compatibility
	w.Agents = append(w.Agents, agentName)

	w.UpdatedAt = time.Now()
	return nil
}

// RemoveAgent removes an agent from the workspace (legacy method)
func (w *Workspace) RemoveAgent(agentName string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i, agent := range w.Agents {
		if agent == agentName {
			w.Agents = append(w.Agents[:i], w.Agents[i+1:]...)

			// Clean up tasks assigned to this agent
			for j := range w.Tasks {
				if w.Tasks[j].To == agentName {
					// Unassign tasks that were assigned to this agent
					w.Tasks[j].To = "unassigned"
					w.Tasks[j].AssignedNodeID = ""
				}
				if w.Tasks[j].From == agentName {
					// Clear from field for tasks created by this agent
					w.Tasks[j].From = ""
				}
			}

			// Clean up canvas layout agent positions for this agent
			if w.Layout != nil && w.Layout.AgentPositions != nil {
				// Remove all agent node positions for this agent name
				for nodeID := range w.Layout.AgentPositions {
					// Check if this position belongs to the removed agent
					// NodeID format: "agentname-node-#"
					if len(nodeID) > len(agentName) && nodeID[:len(agentName)] == agentName {
						delete(w.Layout.AgentPositions, nodeID)
					}
				}
			}

			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("agent %s not found in workspace", agentName)
}

// RemoveAgentInstance removes a specific agent instance by its stable ID or NodeID
func (w *Workspace) RemoveAgentInstance(instanceID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Find and remove the agent instance
	foundIndex := -1
	var removedInstance AgentInstance
	for i, inst := range w.AgentInstances {
		if inst.ID == instanceID || inst.NodeID == instanceID {
			foundIndex = i
			removedInstance = inst
			break
		}
	}

	if foundIndex == -1 {
		return fmt.Errorf("agent instance %s not found", instanceID)
	}

	// Remove from AgentInstances
	w.AgentInstances = append(w.AgentInstances[:foundIndex], w.AgentInstances[foundIndex+1:]...)

	// Also remove from legacy Agents array (first occurrence of this name)
	for i, agent := range w.Agents {
		if agent == removedInstance.Name {
			w.Agents = append(w.Agents[:i], w.Agents[i+1:]...)
			break
		}
	}

	// Unassign tasks that were specifically assigned to this agent instance
	for j := range w.Tasks {
		if w.Tasks[j].AssignedNodeID == removedInstance.NodeID {
			w.Tasks[j].To = "unassigned"
			w.Tasks[j].AssignedNodeID = ""
			// Clear input connections for unassigned tasks
			w.Tasks[j].InputTaskIDs = nil
		}
		if w.Tasks[j].From == removedInstance.Name {
			w.Tasks[j].From = ""
		}
	}

	// Clean up canvas layout position for this specific node
	if w.Layout != nil && w.Layout.AgentPositions != nil {
		delete(w.Layout.AgentPositions, removedInstance.NodeID)
	}

	w.UpdatedAt = time.Now()
	logger.Debug("Removed agent instance ( #)", logger.Fields{"instancenumber": removedInstance.InstanceNumber, "agent": removedInstance.ID, "name": removedInstance.Name})
	return nil
}

// hasAgent checks if an agent is part of the workspace (NOT thread-safe, caller must hold lock)
func (w *Workspace) hasAgent(agentName string) bool {
	for _, agent := range w.Agents {
		if agent == agentName {
			return true
		}
	}
	return false
}

// HasAgent checks if an agent is part of the workspace (thread-safe)
func (w *Workspace) HasAgent(agentName string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.hasAgent(agentName)
}

// SyncAgentsFromTasks ensures all agents assigned to tasks are added to the workspace
// This handles cases where tasks were assigned agents before the auto-add feature
func (w *Workspace) SyncAgentsFromTasks() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	added := 0
	for _, task := range w.Tasks {
		if task.To != "" && task.To != "unassigned" && !w.hasAgent(task.To) {
			// Add agent without lock (we already hold it)
			instanceNumber := 1
			for _, inst := range w.AgentInstances {
				if inst.Name == task.To && inst.InstanceNumber >= instanceNumber {
					instanceNumber = inst.InstanceNumber + 1
				}
			}

			instance := AgentInstance{
				ID:             fmt.Sprintf("%s-%d", task.To, instanceNumber),
				Name:           task.To,
				InstanceNumber: instanceNumber,
				NodeID:         fmt.Sprintf("%s-node-%d", task.To, instanceNumber),
				CreatedAt:      time.Now(),
			}

			w.AgentInstances = append(w.AgentInstances, instance)
			w.Agents = append(w.Agents, task.To)
			w.UpdatedAt = time.Now()
			added++

			logger.Info("Synced missing agent from task", logger.Fields{"agent": task.To, "workspace_id": w.ID})
		}
	}

	return added
}
