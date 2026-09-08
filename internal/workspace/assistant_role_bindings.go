package workspace

import "strings"

// SetHomeRoleBindings replaces only the Home-scoped role identity map using
// its own compare-and-swap revision. It does not alter project bindings or the
// schema-v1 shared roster compatibility fields.
func (service *AssistantProgramStore) SetHomeRoleBindings(stationID string, expectedRevision int64, bindings []AssistantRoleBinding) error {
	if service == nil || service.store == nil || strings.TrimSpace(stationID) == "" || expectedRevision < 0 {
		return ErrAssistantBindingInvalid
	}
	return service.store.Update(stationID, func(station *Workspace) error {
		state := station.GetAssistantProgramState()
		if state == nil || state.SchemaVersion < AssistantProgramStateSchemaVersion || state.Declaration == nil {
			return ErrAssistantProgramUnavailable
		}
		if state.HomeBindings.StateRevision != expectedRevision {
			return ErrAssistantBindingVersionConflict
		}
		if err := validateAssistantRoleBindings(station, state.Declaration, AssistantRoleScopeHome, bindings); err != nil {
			return err
		}
		state.HomeBindings = AssistantRoleBindingSet{
			StateRevision: expectedRevision + 1,
			Bindings:      append([]AssistantRoleBinding(nil), bindings...),
		}
		station.SetAssistantProgramState(state)
		return nil
	})
}

// SetProjectRoleBindings replaces only one exact linked child's role identity
// map. The authoritative AssistantProjectLink identifies both the child and
// its Home; no mutable display field participates in scope resolution.
func (service *AssistantProgramStore) SetProjectRoleBindings(projectID string, expectedRevision int64, bindings []AssistantRoleBinding) error {
	if service == nil || service.store == nil || strings.TrimSpace(projectID) == "" || expectedRevision < 0 {
		return ErrAssistantBindingInvalid
	}
	project, err := service.store.Get(projectID)
	if err != nil {
		return err
	}
	link := project.GetAssistantProjectLink()
	if link == nil || link.SchemaVersion < AssistantProjectLinkSchemaVersion {
		return ErrAssistantProgramUnavailable
	}
	station, err := service.store.Get(link.StationWorkspaceID)
	if err != nil {
		return ErrAssistantStationNotFound
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.Declaration == nil || state.Key.Normalize() != link.Key.Normalize() {
		return ErrAssistantStationNotFound
	}
	return service.store.Update(projectID, func(current *Workspace) error {
		currentLink := current.GetAssistantProjectLink()
		if currentLink == nil || currentLink.SchemaVersion < AssistantProjectLinkSchemaVersion ||
			currentLink.StationWorkspaceID != station.ID || currentLink.Key.Normalize() != state.Key.Normalize() {
			return ErrAssistantProgramVersionConflict
		}
		if currentLink.ProjectBindings.StateRevision != expectedRevision {
			return ErrAssistantBindingVersionConflict
		}
		if err := validateAssistantRoleBindings(current, state.Declaration, AssistantRoleScopeProject, bindings); err != nil {
			return err
		}
		currentLink.ProjectBindings = AssistantRoleBindingSet{
			StateRevision: expectedRevision + 1,
			Bindings:      append([]AssistantRoleBinding(nil), bindings...),
		}
		current.SetAssistantProjectLink(currentLink)
		return nil
	})
}

func validateAssistantRoleBindings(target *Workspace, declaration *AssistantProgramDeclaration, scope AssistantRoleScope, bindings []AssistantRoleBinding) error {
	if target == nil || declaration == nil || (scope != AssistantRoleScopeHome && scope != AssistantRoleScopeProject) || len(bindings) > AssistantProgramMaxRoles {
		return ErrAssistantBindingInvalid
	}
	roles := make(map[string]AssistantProgramRoleSpec, len(declaration.Roles))
	for _, role := range declaration.Roles {
		if role.Scope == scope {
			roles[role.ID] = role
		}
	}
	instances := make(map[string]AgentInstance, len(target.GetAgentInstances()))
	for _, instance := range target.GetAgentInstances() {
		instances[instance.ID] = instance
	}
	seenRoles := make(map[string]struct{}, len(bindings))
	seenInstances := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		binding.RoleID = strings.TrimSpace(binding.RoleID)
		binding.AgentInstanceID = strings.TrimSpace(binding.AgentInstanceID)
		binding.AgentName = strings.TrimSpace(binding.AgentName)
		if _, ok := roles[binding.RoleID]; !ok || binding.AgentInstanceID == "" || binding.AgentName == "" {
			return ErrAssistantBindingInvalid
		}
		if _, exists := seenRoles[binding.RoleID]; exists {
			return ErrAssistantBindingInvalid
		}
		if _, exists := seenInstances[binding.AgentInstanceID]; exists {
			return ErrAssistantBindingInvalid
		}
		instance, ok := instances[binding.AgentInstanceID]
		if !ok || instance.Name != binding.AgentName {
			return ErrAssistantBindingInvalid
		}
		seenRoles[binding.RoleID] = struct{}{}
		seenInstances[binding.AgentInstanceID] = struct{}{}
	}
	return nil
}
