package workspace

import (
	"fmt"
	"strings"
	"time"
)

const sharedDataEntryAgentNameKey = "entry_agent_name"

// EntryAgentName returns the workspace's default entry agent name.
func (w *Workspace) EntryAgentName() string {
	if w == nil {
		return ""
	}
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.entryAgentNameLocked()
}

// SetEntryAgentName stores the workspace's default entry agent name.
func (w *Workspace) SetEntryAgentName(agentName string) error {
	if w == nil {
		return fmt.Errorf("workspace is nil")
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.setEntryAgentNameLocked(agentName)
}

func (w *Workspace) entryAgentNameLocked() string {
	if name := entryAgentNameFromSharedData(w.SharedData); name != "" && w.hasAgent(name) {
		return name
	}

	for _, inst := range w.AgentInstances {
		if inst.EntryPoint && strings.TrimSpace(inst.Name) != "" {
			return strings.TrimSpace(inst.Name)
		}
	}

	if len(w.AgentInstances) > 0 {
		name := strings.TrimSpace(w.AgentInstances[0].Name)
		if name != "" {
			return name
		}
	}

	for _, agentName := range w.Agents {
		name := strings.TrimSpace(agentName)
		if name != "" {
			return name
		}
	}

	return ""
}

func entryAgentNameFromSharedData(sharedData map[string]any) string {
	if len(sharedData) == 0 {
		return ""
	}

	value, ok := sharedData[sharedDataEntryAgentNameKey]
	if !ok {
		return ""
	}

	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func (w *Workspace) setEntryAgentNameLocked(agentName string) error {
	trimmed := strings.TrimSpace(agentName)
	if trimmed == "" {
		if w.SharedData != nil {
			delete(w.SharedData, sharedDataEntryAgentNameKey)
		}
		for i := range w.AgentInstances {
			w.AgentInstances[i].EntryPoint = false
		}
		w.UpdatedAt = time.Now()
		return nil
	}

	if !w.hasAgent(trimmed) {
		return fmt.Errorf("agent %s is not part of workspace", trimmed)
	}

	if w.SharedData == nil {
		w.SharedData = make(map[string]any)
	}
	w.SharedData[sharedDataEntryAgentNameKey] = trimmed
	for i := range w.AgentInstances {
		w.AgentInstances[i].EntryPoint = normalizeAgentNameKey(w.AgentInstances[i].Name) == normalizeAgentNameKey(trimmed)
	}
	w.UpdatedAt = time.Now()
	return nil
}
