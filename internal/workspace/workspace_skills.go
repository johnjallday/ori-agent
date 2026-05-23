package workspace

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// GetSkillBindings returns a copy of the workspace skill bindings.
func (w *Workspace) GetSkillBindings() []WorkspaceSkillBinding {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if len(w.SkillBindings) == 0 {
		return nil
	}

	out := make([]WorkspaceSkillBinding, len(w.SkillBindings))
	for i := range w.SkillBindings {
		out[i] = cloneSkillBinding(w.SkillBindings[i])
	}
	return out
}

// GetSkillBinding returns a copy of the workspace skill binding by ID.
func (w *Workspace) GetSkillBinding(bindingID string) (*WorkspaceSkillBinding, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	normalizedID := strings.TrimSpace(bindingID)
	if normalizedID == "" {
		return nil, false
	}

	for i := range w.SkillBindings {
		binding := w.SkillBindings[i]
		if strings.EqualFold(strings.TrimSpace(binding.ID), normalizedID) {
			cp := cloneSkillBinding(binding)
			return &cp, true
		}
	}

	return nil, false
}

// UpsertSkillBinding creates or updates a workspace skill binding.
func (w *Workspace) UpsertSkillBinding(binding WorkspaceSkillBinding) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	binding.ID = strings.TrimSpace(binding.ID)
	binding.SkillName = strings.TrimSpace(binding.SkillName)
	if binding.ID == "" {
		return fmt.Errorf("binding ID is required")
	}
	if binding.SkillName == "" {
		return fmt.Errorf("skill name is required")
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = time.Now()
	}
	binding.UpdatedAt = time.Now()

	for i := range w.SkillBindings {
		if strings.EqualFold(strings.TrimSpace(w.SkillBindings[i].ID), binding.ID) {
			binding.CreatedAt = w.SkillBindings[i].CreatedAt
			w.SkillBindings[i] = cloneSkillBinding(binding)
			w.UpdatedAt = binding.UpdatedAt
			return nil
		}
	}

	w.SkillBindings = append(w.SkillBindings, cloneSkillBinding(binding))
	w.UpdatedAt = binding.UpdatedAt
	return nil
}

// DeleteSkillBinding removes a workspace skill binding by ID.
func (w *Workspace) DeleteSkillBinding(bindingID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	normalizedID := strings.TrimSpace(bindingID)
	if normalizedID == "" {
		return fmt.Errorf("binding ID is required")
	}

	for i := range w.SkillBindings {
		if strings.EqualFold(strings.TrimSpace(w.SkillBindings[i].ID), normalizedID) {
			w.SkillBindings = append(w.SkillBindings[:i], w.SkillBindings[i+1:]...)
			for j := range w.AgentSkillAccess {
				w.AgentSkillAccess[j].EnabledBindingIDs = removeNormalizedValue(w.AgentSkillAccess[j].EnabledBindingIDs, normalizedID)
			}
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("skill binding %s not found in workspace", bindingID)
}

// GetAgentSkillAccess returns the skill access rule for the given agent instance.
func (w *Workspace) GetAgentSkillAccess(agentInstanceID string) (*WorkspaceAgentSkillAccess, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	normalizedID := strings.TrimSpace(agentInstanceID)
	if normalizedID == "" {
		return nil, false
	}

	for i := range w.AgentSkillAccess {
		entry := w.AgentSkillAccess[i]
		if strings.EqualFold(strings.TrimSpace(entry.AgentInstanceID), normalizedID) {
			cp := cloneSkillAccessEntry(entry)
			return &cp, true
		}
	}

	return nil, false
}

// ListAgentSkillAccess returns a copy of all per-agent-instance skill access rules.
func (w *Workspace) ListAgentSkillAccess() []WorkspaceAgentSkillAccess {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if len(w.AgentSkillAccess) == 0 {
		return nil
	}

	out := make([]WorkspaceAgentSkillAccess, len(w.AgentSkillAccess))
	for i := range w.AgentSkillAccess {
		out[i] = cloneSkillAccessEntry(w.AgentSkillAccess[i])
	}
	return out
}

// SetAgentSkillAccess creates or updates the skill access rule for an agent instance.
func (w *Workspace) SetAgentSkillAccess(entry WorkspaceAgentSkillAccess) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entry.AgentInstanceID = strings.TrimSpace(entry.AgentInstanceID)
	if entry.AgentInstanceID == "" {
		return fmt.Errorf("agent instance ID is required")
	}
	entry.EnabledBindingIDs = dedupeNormalizedValues(entry.EnabledBindingIDs)
	entry.UpdatedAt = time.Now()

	for i := range w.AgentSkillAccess {
		if strings.EqualFold(strings.TrimSpace(w.AgentSkillAccess[i].AgentInstanceID), entry.AgentInstanceID) {
			w.AgentSkillAccess[i] = cloneSkillAccessEntry(entry)
			w.UpdatedAt = entry.UpdatedAt
			return nil
		}
	}

	w.AgentSkillAccess = append(w.AgentSkillAccess, cloneSkillAccessEntry(entry))
	w.UpdatedAt = entry.UpdatedAt
	return nil
}

// DeleteAgentSkillAccess removes the skill access rule for an agent instance.
func (w *Workspace) DeleteAgentSkillAccess(agentInstanceID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	normalizedID := strings.TrimSpace(agentInstanceID)
	if normalizedID == "" {
		return fmt.Errorf("agent instance ID is required")
	}

	for i := range w.AgentSkillAccess {
		if strings.EqualFold(strings.TrimSpace(w.AgentSkillAccess[i].AgentInstanceID), normalizedID) {
			w.AgentSkillAccess = append(w.AgentSkillAccess[:i], w.AgentSkillAccess[i+1:]...)
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("agent skill access %s not found in workspace", agentInstanceID)
}

func cloneSkillBinding(binding WorkspaceSkillBinding) WorkspaceSkillBinding {
	cp := binding
	if len(binding.Config) > 0 {
		data, err := json.Marshal(binding.Config)
		if err != nil {
			cp.Config = make(map[string]any, len(binding.Config))
			for k, v := range binding.Config {
				cp.Config[k] = v
			}
			return cp
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err == nil {
			cp.Config = out
		}
	}
	if len(binding.ToolOverrides) > 0 {
		cp.ToolOverrides = cloneSideEffectMap(binding.ToolOverrides)
	}
	return cp
}

func cloneSkillAccessEntry(entry WorkspaceAgentSkillAccess) WorkspaceAgentSkillAccess {
	cp := entry
	if len(entry.EnabledBindingIDs) > 0 {
		cp.EnabledBindingIDs = append([]string{}, entry.EnabledBindingIDs...)
	}
	return cp
}
