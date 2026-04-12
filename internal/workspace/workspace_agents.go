package workspace

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
)

var ErrAgentAlreadyInWorkspace = errors.New("agent is already in workspace")
var ErrWorkspaceEntryAgentRequired = errors.New("workspace must keep an entry agent")

func normalizeAgentNameKey(agentName string) string {
	return strings.ToLower(strings.TrimSpace(agentName))
}

func canonicalAgentNodeID(agentName string) string {
	return fmt.Sprintf("%s-node-1", strings.TrimSpace(agentName))
}

func buildAgentInstance(agentName string) AgentInstance {
	trimmedName := strings.TrimSpace(agentName)
	return AgentInstance{
		ID:             uuid.New().String(),
		Name:           trimmedName,
		InstanceNumber: 1,
		NodeID:         canonicalAgentNodeID(trimmedName),
		CreatedAt:      time.Now(),
	}
}

// AddAgent adds a new agent instance to the workspace
func (w *Workspace) AddAgent(agentName string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	trimmedName := strings.TrimSpace(agentName)
	if trimmedName == "" {
		return fmt.Errorf("agent name is required")
	}

	if w.hasAgent(trimmedName) {
		return fmt.Errorf("%w: %s", ErrAgentAlreadyInWorkspace, trimmedName)
	}

	currentEntryAgent := w.entryAgentNameLocked()
	instance := buildAgentInstance(trimmedName)
	w.AgentInstances = append(w.AgentInstances, instance)

	// Also update legacy Agents array for backward compatibility
	w.Agents = append(w.Agents, trimmedName)

	if currentEntryAgent == "" {
		if err := w.setEntryAgentNameLocked(trimmedName); err != nil {
			return err
		}
	} else {
		w.UpdatedAt = time.Now()
	}
	return nil
}

// RemoveAgent removes an agent from the workspace (legacy method)
func (w *Workspace) RemoveAgent(agentName string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	currentEntryAgent := w.entryAgentNameLocked()

	for i, agent := range w.Agents {
		if normalizeAgentNameKey(agent) == normalizeAgentNameKey(agentName) {
			if normalizeAgentNameKey(agent) == normalizeAgentNameKey(currentEntryAgent) && len(w.Agents) == 1 {
				return ErrWorkspaceEntryAgentRequired
			}

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

			switch {
			case normalizeAgentNameKey(agent) == normalizeAgentNameKey(currentEntryAgent) && len(w.Agents) > 0:
				if err := w.setEntryAgentNameLocked(w.Agents[0]); err != nil {
					return err
				}
			case currentEntryAgent != "" && w.hasAgent(currentEntryAgent):
				if err := w.setEntryAgentNameLocked(currentEntryAgent); err != nil {
					return err
				}
			default:
				w.UpdatedAt = time.Now()
			}
			return nil
		}
	}

	return fmt.Errorf("agent %s not found in workspace", agentName)
}

// RemoveAgentInstance removes a specific agent instance by its stable ID or NodeID
func (w *Workspace) RemoveAgentInstance(instanceID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	currentEntryAgent := w.entryAgentNameLocked()

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

	if normalizeAgentNameKey(removedInstance.Name) == normalizeAgentNameKey(currentEntryAgent) && len(w.AgentInstances) == 1 {
		return ErrWorkspaceEntryAgentRequired
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

	switch {
	case normalizeAgentNameKey(removedInstance.Name) == normalizeAgentNameKey(currentEntryAgent) && len(w.AgentInstances) > 0:
		if err := w.setEntryAgentNameLocked(w.AgentInstances[0].Name); err != nil {
			return err
		}
	case currentEntryAgent != "" && w.hasAgent(currentEntryAgent):
		if err := w.setEntryAgentNameLocked(currentEntryAgent); err != nil {
			return err
		}
	default:
		w.UpdatedAt = time.Now()
	}

	logger.Debug("Removed agent instance ( #)", logger.Fields{"instancenumber": removedInstance.InstanceNumber, "agent": removedInstance.ID, "name": removedInstance.Name})
	return nil
}

// hasAgent checks if an agent is part of the workspace (NOT thread-safe, caller must hold lock)
func (w *Workspace) hasAgent(agentName string) bool {
	for _, agent := range w.Agents {
		if normalizeAgentNameKey(agent) == normalizeAgentNameKey(agentName) {
			return true
		}
	}
	for _, inst := range w.AgentInstances {
		if normalizeAgentNameKey(inst.Name) == normalizeAgentNameKey(agentName) {
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
			instance := buildAgentInstance(task.To)
			w.AgentInstances = append(w.AgentInstances, instance)
			w.Agents = append(w.Agents, strings.TrimSpace(task.To))
			w.UpdatedAt = time.Now()
			added++

			logger.Info("Synced missing agent from task", logger.Fields{"agent": task.To, "workspace_id": w.ID})
		}
	}

	return added
}

// NormalizeAgentInstances collapses duplicate agent instances so each agent
// profile appears at most once in a workspace. It rewrites old node and
// instance references to the canonical single instance.
func (w *Workspace) NormalizeAgentInstances() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	changed := false

	keptByName := make(map[string]AgentInstance)
	normalizedInstances := make([]AgentInstance, 0, len(w.AgentInstances))
	instanceIDMap := make(map[string]string, len(w.AgentInstances))
	nodeIDMap := make(map[string]string, len(w.AgentInstances))

	for _, inst := range w.AgentInstances {
		name := strings.TrimSpace(inst.Name)
		if name == "" {
			changed = true
			continue
		}

		key := normalizeAgentNameKey(name)
		if canonical, exists := keptByName[key]; exists {
			if strings.TrimSpace(canonical.Role) == "" && strings.TrimSpace(inst.Role) != "" {
				canonical.Role = strings.TrimSpace(inst.Role)
			}
			if strings.TrimSpace(canonical.Description) == "" && strings.TrimSpace(inst.Description) != "" {
				canonical.Description = strings.TrimSpace(inst.Description)
			}
			if inst.EntryPoint && !canonical.EntryPoint {
				canonical.EntryPoint = true
			}
			keptByName[key] = canonical
			for idx := range normalizedInstances {
				if normalizedInstances[idx].ID == canonical.ID {
					normalizedInstances[idx] = canonical
					break
				}
			}
			instanceIDMap[inst.ID] = canonical.ID
			if oldNodeID := strings.TrimSpace(inst.NodeID); oldNodeID != "" {
				nodeIDMap[oldNodeID] = canonical.NodeID
			}
			changed = true
			continue
		}

		canonical := inst
		if canonical.ID == "" {
			canonical.ID = uuid.New().String()
			changed = true
		}
		if canonical.Name != name {
			canonical.Name = name
			changed = true
		}
		if canonical.InstanceNumber != 1 {
			canonical.InstanceNumber = 1
			changed = true
		}

		expectedNodeID := canonicalAgentNodeID(name)
		if oldNodeID := strings.TrimSpace(canonical.NodeID); oldNodeID != "" {
			nodeIDMap[oldNodeID] = expectedNodeID
		}
		if canonical.NodeID != expectedNodeID {
			canonical.NodeID = expectedNodeID
			changed = true
		}

		instanceIDMap[inst.ID] = canonical.ID
		keptByName[key] = canonical
		normalizedInstances = append(normalizedInstances, canonical)
	}

	normalizedAgents := make([]string, 0, len(normalizedInstances))
	seenAgents := make(map[string]struct{}, len(normalizedInstances))
	for _, inst := range normalizedInstances {
		key := normalizeAgentNameKey(inst.Name)
		if _, exists := seenAgents[key]; exists {
			continue
		}
		seenAgents[key] = struct{}{}
		normalizedAgents = append(normalizedAgents, inst.Name)
	}
	if len(normalizedAgents) != len(w.Agents) {
		changed = true
	} else {
		for i := range normalizedAgents {
			if normalizeAgentNameKey(normalizedAgents[i]) != normalizeAgentNameKey(w.Agents[i]) {
				changed = true
				break
			}
		}
	}

	if len(normalizedInstances) != len(w.AgentInstances) {
		changed = true
	}

	if len(keptByName) > 0 {
		for i := range w.Tasks {
			task := &w.Tasks[i]
			if task.To == "" || task.To == "unassigned" {
				continue
			}
			key := normalizeAgentNameKey(task.To)
			canonical, exists := keptByName[key]
			if !exists {
				continue
			}
			if task.To != canonical.Name {
				task.To = canonical.Name
				changed = true
			}

			assignedNodeID := strings.TrimSpace(task.AssignedNodeID)
			switch {
			case assignedNodeID == "" || assignedNodeID == "unassigned":
				task.AssignedNodeID = canonical.NodeID
				changed = true
			case nodeIDMap[assignedNodeID] != "" && nodeIDMap[assignedNodeID] != assignedNodeID:
				task.AssignedNodeID = nodeIDMap[assignedNodeID]
				changed = true
			case assignedNodeID != canonical.NodeID:
				task.AssignedNodeID = canonical.NodeID
				changed = true
			}
		}
	}

	if len(w.StoreNodes) > 0 {
		for i := range w.StoreNodes {
			agentNodeID := strings.TrimSpace(w.StoreNodes[i].AgentNodeID)
			if mapped := nodeIDMap[agentNodeID]; mapped != "" && mapped != agentNodeID {
				w.StoreNodes[i].AgentNodeID = mapped
				changed = true
			}
		}
	}

	if w.Layout != nil {
		if len(w.Layout.AgentPositions) > 0 {
			normalizedPositions := make(map[string]Position, len(w.Layout.AgentPositions))
			for nodeID, pos := range w.Layout.AgentPositions {
				mappedNodeID := nodeID
				if mapped := nodeIDMap[nodeID]; mapped != "" {
					mappedNodeID = mapped
				}
				if _, exists := normalizedPositions[mappedNodeID]; !exists {
					normalizedPositions[mappedNodeID] = pos
				}
				if mappedNodeID != nodeID {
					changed = true
				}
			}
			w.Layout.AgentPositions = normalizedPositions
		}

		if len(w.Layout.WorkflowConnections) > 0 {
			for i := range w.Layout.WorkflowConnections {
				conn := &w.Layout.WorkflowConnections[i]
				if mapped := nodeIDMap[strings.TrimSpace(conn.From)]; mapped != "" && mapped != conn.From {
					conn.From = mapped
					changed = true
				}
				if mapped := nodeIDMap[strings.TrimSpace(conn.To)]; mapped != "" && mapped != conn.To {
					conn.To = mapped
					changed = true
				}
			}
		}
	}

	if len(w.AgentMCPAccess) > 0 {
		merged := make(map[string]WorkspaceAgentMCPAccess, len(w.AgentMCPAccess))
		for _, entry := range w.AgentMCPAccess {
			canonicalID := strings.TrimSpace(entry.AgentInstanceID)
			if mapped := instanceIDMap[canonicalID]; mapped != "" {
				canonicalID = mapped
			}
			if canonicalID == "" {
				changed = true
				continue
			}
			current, exists := merged[canonicalID]
			if !exists {
				entry.AgentInstanceID = canonicalID
				entry.EnabledBindingIDs = dedupeNormalizedValues(entry.EnabledBindingIDs)
				merged[canonicalID] = cloneAccessEntry(entry)
				continue
			}
			current.EnabledBindingIDs = dedupeNormalizedValues(append(current.EnabledBindingIDs, entry.EnabledBindingIDs...))
			if entry.UpdatedAt.After(current.UpdatedAt) {
				current.UpdatedAt = entry.UpdatedAt
			}
			merged[canonicalID] = current
			changed = true
		}
		normalized := make([]WorkspaceAgentMCPAccess, 0, len(merged))
		for _, inst := range normalizedInstances {
			if entry, exists := merged[inst.ID]; exists {
				normalized = append(normalized, entry)
			}
		}
		if len(normalized) != len(w.AgentMCPAccess) {
			changed = true
		}
		w.AgentMCPAccess = normalized
	}

	if len(w.AgentSkillAccess) > 0 {
		merged := make(map[string]WorkspaceAgentSkillAccess, len(w.AgentSkillAccess))
		for _, entry := range w.AgentSkillAccess {
			canonicalID := strings.TrimSpace(entry.AgentInstanceID)
			if mapped := instanceIDMap[canonicalID]; mapped != "" {
				canonicalID = mapped
			}
			if canonicalID == "" {
				changed = true
				continue
			}
			current, exists := merged[canonicalID]
			if !exists {
				entry.AgentInstanceID = canonicalID
				entry.EnabledBindingIDs = dedupeNormalizedValues(entry.EnabledBindingIDs)
				merged[canonicalID] = cloneSkillAccessEntry(entry)
				continue
			}
			current.EnabledBindingIDs = dedupeNormalizedValues(append(current.EnabledBindingIDs, entry.EnabledBindingIDs...))
			if entry.UpdatedAt.After(current.UpdatedAt) {
				current.UpdatedAt = entry.UpdatedAt
			}
			merged[canonicalID] = current
			changed = true
		}
		normalized := make([]WorkspaceAgentSkillAccess, 0, len(merged))
		for _, inst := range normalizedInstances {
			if entry, exists := merged[inst.ID]; exists {
				normalized = append(normalized, entry)
			}
		}
		if len(normalized) != len(w.AgentSkillAccess) {
			changed = true
		}
		w.AgentSkillAccess = normalized
	}

	if changed {
		w.AgentInstances = normalizedInstances
		w.Agents = normalizedAgents
		w.UpdatedAt = time.Now()
	}

	return changed
}
