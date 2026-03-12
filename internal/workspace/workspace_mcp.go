package workspace

import (
	"fmt"
	"strings"
	"time"
)

// FindAgentInstance returns the workspace agent instance matching the provided node ID,
// or the first instance matching the agent name when node ID is empty.
func (w *Workspace) FindAgentInstance(agentName, nodeID string) (*AgentInstance, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	normalizedNodeID := strings.TrimSpace(nodeID)
	if normalizedNodeID != "" {
		for i := range w.AgentInstances {
			instance := w.AgentInstances[i]
			if strings.EqualFold(strings.TrimSpace(instance.NodeID), normalizedNodeID) {
				copy := instance
				return &copy, true
			}
		}
	}

	normalizedAgentName := strings.TrimSpace(agentName)
	if normalizedAgentName == "" {
		return nil, false
	}

	for i := range w.AgentInstances {
		instance := w.AgentInstances[i]
		if strings.EqualFold(strings.TrimSpace(instance.Name), normalizedAgentName) {
			copy := instance
			return &copy, true
		}
	}

	return nil, false
}

// GetMCPBindings returns a copy of the workspace MCP bindings.
func (w *Workspace) GetMCPBindings() []WorkspaceMCPBinding {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if len(w.MCPBindings) == 0 {
		return nil
	}

	out := make([]WorkspaceMCPBinding, len(w.MCPBindings))
	copy(out, w.MCPBindings)
	return out
}

// GetMCPBinding returns a copy of the workspace MCP binding by ID.
func (w *Workspace) GetMCPBinding(bindingID string) (*WorkspaceMCPBinding, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	normalizedID := strings.TrimSpace(bindingID)
	if normalizedID == "" {
		return nil, false
	}

	for i := range w.MCPBindings {
		binding := w.MCPBindings[i]
		if strings.EqualFold(strings.TrimSpace(binding.ID), normalizedID) {
			copy := binding
			if len(binding.Scope) > 0 {
				copy.Scope = cloneInterfaceMap(binding.Scope)
			}
			return &copy, true
		}
	}

	return nil, false
}

// UpsertMCPBinding creates or updates a workspace MCP binding.
func (w *Workspace) UpsertMCPBinding(binding WorkspaceMCPBinding) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	binding.ID = strings.TrimSpace(binding.ID)
	binding.ServerName = strings.TrimSpace(binding.ServerName)
	if binding.ID == "" {
		return fmt.Errorf("binding ID is required")
	}
	if binding.ServerName == "" {
		return fmt.Errorf("server name is required")
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = time.Now()
	}
	binding.UpdatedAt = time.Now()

	for i := range w.MCPBindings {
		if strings.EqualFold(strings.TrimSpace(w.MCPBindings[i].ID), binding.ID) {
			binding.CreatedAt = w.MCPBindings[i].CreatedAt
			w.MCPBindings[i] = cloneBinding(binding)
			w.UpdatedAt = binding.UpdatedAt
			return nil
		}
	}

	w.MCPBindings = append(w.MCPBindings, cloneBinding(binding))
	w.UpdatedAt = binding.UpdatedAt
	return nil
}

// DeleteMCPBinding removes a workspace MCP binding by ID.
func (w *Workspace) DeleteMCPBinding(bindingID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	normalizedID := strings.TrimSpace(bindingID)
	if normalizedID == "" {
		return fmt.Errorf("binding ID is required")
	}

	for i := range w.MCPBindings {
		if strings.EqualFold(strings.TrimSpace(w.MCPBindings[i].ID), normalizedID) {
			w.MCPBindings = append(w.MCPBindings[:i], w.MCPBindings[i+1:]...)
			for j := range w.AgentMCPAccess {
				w.AgentMCPAccess[j].EnabledBindingIDs = removeNormalizedValue(w.AgentMCPAccess[j].EnabledBindingIDs, normalizedID)
			}
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("MCP binding %s not found in workspace", bindingID)
}

// GetAgentMCPAccess returns the MCP access rule for the given agent instance.
func (w *Workspace) GetAgentMCPAccess(agentInstanceID string) (*WorkspaceAgentMCPAccess, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	normalizedID := strings.TrimSpace(agentInstanceID)
	if normalizedID == "" {
		return nil, false
	}

	for i := range w.AgentMCPAccess {
		entry := w.AgentMCPAccess[i]
		if strings.EqualFold(strings.TrimSpace(entry.AgentInstanceID), normalizedID) {
			copy := entry
			if len(entry.EnabledBindingIDs) > 0 {
				copy.EnabledBindingIDs = append([]string{}, entry.EnabledBindingIDs...)
			}
			return &copy, true
		}
	}

	return nil, false
}

// ListAgentMCPAccess returns a copy of all per-agent-instance MCP access rules.
func (w *Workspace) ListAgentMCPAccess() []WorkspaceAgentMCPAccess {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if len(w.AgentMCPAccess) == 0 {
		return nil
	}

	out := make([]WorkspaceAgentMCPAccess, len(w.AgentMCPAccess))
	for i := range w.AgentMCPAccess {
		out[i] = w.AgentMCPAccess[i]
		if len(w.AgentMCPAccess[i].EnabledBindingIDs) > 0 {
			out[i].EnabledBindingIDs = append([]string{}, w.AgentMCPAccess[i].EnabledBindingIDs...)
		}
	}
	return out
}

// SetAgentMCPAccess creates or updates the MCP access rule for an agent instance.
func (w *Workspace) SetAgentMCPAccess(entry WorkspaceAgentMCPAccess) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entry.AgentInstanceID = strings.TrimSpace(entry.AgentInstanceID)
	if entry.AgentInstanceID == "" {
		return fmt.Errorf("agent instance ID is required")
	}
	entry.EnabledBindingIDs = dedupeNormalizedValues(entry.EnabledBindingIDs)
	entry.UpdatedAt = time.Now()

	for i := range w.AgentMCPAccess {
		if strings.EqualFold(strings.TrimSpace(w.AgentMCPAccess[i].AgentInstanceID), entry.AgentInstanceID) {
			w.AgentMCPAccess[i] = cloneAccessEntry(entry)
			w.UpdatedAt = entry.UpdatedAt
			return nil
		}
	}

	w.AgentMCPAccess = append(w.AgentMCPAccess, cloneAccessEntry(entry))
	w.UpdatedAt = entry.UpdatedAt
	return nil
}

// DeleteAgentMCPAccess removes the MCP access rule for an agent instance.
func (w *Workspace) DeleteAgentMCPAccess(agentInstanceID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	normalizedID := strings.TrimSpace(agentInstanceID)
	if normalizedID == "" {
		return fmt.Errorf("agent instance ID is required")
	}

	for i := range w.AgentMCPAccess {
		if strings.EqualFold(strings.TrimSpace(w.AgentMCPAccess[i].AgentInstanceID), normalizedID) {
			w.AgentMCPAccess = append(w.AgentMCPAccess[:i], w.AgentMCPAccess[i+1:]...)
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("agent MCP access %s not found in workspace", agentInstanceID)
}

func cloneBinding(binding WorkspaceMCPBinding) WorkspaceMCPBinding {
	copy := binding
	if len(binding.Scope) > 0 {
		copy.Scope = cloneInterfaceMap(binding.Scope)
	}
	return copy
}

func cloneAccessEntry(entry WorkspaceAgentMCPAccess) WorkspaceAgentMCPAccess {
	copy := entry
	if len(entry.EnabledBindingIDs) > 0 {
		copy.EnabledBindingIDs = append([]string{}, entry.EnabledBindingIDs...)
	}
	return copy
}

func cloneInterfaceMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func dedupeNormalizedValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func removeNormalizedValue(values []string, target string) []string {
	if len(values) == 0 {
		return nil
	}
	normalizedTarget := strings.ToLower(strings.TrimSpace(target))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == normalizedTarget {
			continue
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
