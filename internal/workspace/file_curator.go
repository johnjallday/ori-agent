package workspace

import "strings"

// FindUniqueAgentInstance is the fail-closed identity lookup for host context.
// Unlike FindAgentInstance, an explicit node must match the requested agent,
// and neither a missing node nor an ambiguous name selects a fallback instance.
func (w *Workspace) FindUniqueAgentInstance(agentName, nodeID string) (*AgentInstance, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	name, node := strings.TrimSpace(agentName), strings.TrimSpace(nodeID)
	if name == "" {
		return nil, false
	}
	var found *AgentInstance
	for _, instance := range w.AgentInstances {
		if !strings.EqualFold(strings.TrimSpace(instance.Name), name) || (node != "" && !strings.EqualFold(strings.TrimSpace(instance.NodeID), node)) {
			continue
		}
		if found != nil || strings.TrimSpace(instance.ID) == "" {
			return nil, false
		}
		copy := instance
		found = &copy
	}
	return found, found != nil
}

// FileCurator returns the one current instance associated with the built-in
// capability. Names, skill bindings, and entry-agent status are not authority.
func (w *Workspace) FileCurator() (*AgentInstance, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var resources []CapabilityResource
	installs := 0
	for _, record := range w.InstalledCapabilities {
		if NormalizeCapabilityID(record.ID) == CapabilityFileJanitor && record.Active() {
			if record.Owner != nil {
				return nil, false
			}
			installs++
			resources = record.ResourcesOfKind(ResourceCompanionAgent)
		}
	}
	if installs != 1 || len(resources) != 1 {
		return nil, false
	}
	var found *AgentInstance
	for _, instance := range w.AgentInstances {
		if instance.ID != resources[0].ID || instance.ID == "" {
			continue
		}
		if found != nil {
			return nil, false
		}
		copy := instance
		found = &copy
	}
	return found, found != nil
}

// LegacyFileCuratorResource uses only persisted role-filling provenance. A
// blueprint/name alone cannot identify which agent was actually created or
// assigned. Old name-only rosters must use the explicit Add a Curator action.
// Existing (including stale/ambiguous) associations are never overwritten.
func (w *Workspace) LegacyFileCuratorResource() (CapabilityResource, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	p := w.TemplateProvenance
	if p == nil || !p.Builtin || p.PluginOwner != nil || p.UserTemplateOwner != nil || p.TemplateID != "file-janitor" {
		return CapabilityResource{}, false
	}
	installed := false
	for _, record := range w.InstalledCapabilities {
		if NormalizeCapabilityID(record.ID) != CapabilityFileJanitor || !record.Active() {
			continue
		}
		if installed || record.Owner != nil || record.Source != InstallSourceBlueprint || len(record.ResourcesOfKind(ResourceCompanionAgent)) != 0 {
			return CapabilityResource{}, false
		}
		installed = true
	}
	if !installed {
		return CapabilityResource{}, false
	}
	var candidate CapabilityResource
	for _, instance := range w.AgentInstances {
		if instance.RoleID != "file-curator" {
			continue
		}
		if candidate.ID != "" || instance.ID == "" || (instance.RoleSource != RoleSourceCreated && instance.RoleSource != RoleSourceAssigned) {
			return CapabilityResource{}, false
		}
		// Conservative repair never acquires ownership of an existing agent.
		candidate = CapabilityResource{Kind: ResourceCompanionAgent, ID: instance.ID, Shared: true}
	}
	return candidate, candidate.ID != ""
}
