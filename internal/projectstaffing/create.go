package projectstaffing

import (
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ModeHeld marks a Home role an agent already holds. Nothing is staffed for
// it; a review only names who has it.
const ModeHeld = "held"

// CreatedFill is how one required role is filled when a reviewed create makes a
// project: the Home's roles on the Home, the project's on the project.
type CreatedFill struct {
	RoleID string
	Label  string
	Scope  workspace.AssistantRoleScope
	Name   string
	Mode   string
}

// CreatePreview is what a reviewed create is about to staff, read before the
// project exists.
type CreatePreview struct {
	// Home is the group the project joins; nil while it does not exist.
	Home *workspace.Workspace
	// Roles are the declared roles of both scopes, in declaration order.
	Roles []workspace.AssistantProgramRoleSpec
	// DefaultPrimaryName names the agent of the Home's primary role.
	DefaultPrimaryName string
	// Provider is the split project blueprint whose roles one agent shares
	// across the Home's projects; nil when every project staffs its own.
	Provider *workspace.AssistantProjectProviderOwner
	// Held is the verified holder of each Home role that has one, by role ID.
	Held map[string]string
	// Explicit are the roles the user filled themselves.
	Explicit map[string]bool
}

// PreviewCreate says how each required role the user left empty would be
// filled by a reviewed create. It reads only: no consent is granted and no name
// is reserved, so the create decides again when it runs.
func (s *Service) PreviewCreate(in CreatePreview) []CreatedFill {
	if s == nil {
		return nil
	}
	free := s.freeNames(in.Home, nil)
	var consent *workspace.ProjectStaffingConsent
	if in.Home != nil {
		consent = in.Home.GetAssistantProgramState().GetProjectStaffingConsent()
		if consent != nil && consent.Validate() != nil {
			consent = nil
		}
	}
	var fills []CreatedFill
	for _, role := range in.Roles {
		if !role.Required || in.Explicit[role.ID] {
			continue
		}
		fill := CreatedFill{RoleID: role.ID, Label: role.Label, Scope: roleScope(role), Mode: ModeCreate}
		switch {
		case fill.Scope == workspace.AssistantRoleScopeHome:
			if holder := strings.TrimSpace(in.Held[role.ID]); holder != "" {
				fill.Mode, fill.Name = ModeHeld, holder
			} else {
				fill.Name = free(homeRoleName(role, in.DefaultPrimaryName))
			}
		case in.Provider != nil && consent.Covers(in.Provider.PluginID, in.Provider.BlueprintID):
			if entry, covered := consent.Role(role.ID); covered && entry.AgentName != "" && s.agentExists(entry.AgentName) {
				fill.Mode, fill.Name = ModeBind, entry.AgentName
			} else {
				fill.Name = free(role.Label)
			}
		default:
			fill.Name = free(role.Label)
		}
		if fill.Name != "" {
			fills = append(fills, fill)
		}
	}
	return fills
}

// FillCreated says how each required role a reviewed create left empty is
// filled, now that the project and its Home link exist. The Home's roles are
// new agents; a project role follows the Home's standing consent (hire once,
// assign many), which grant records when the Home has none. A role the consent
// cannot cover gets its own new agent. With modelReady false nothing new is
// decided: only an agent the Home already has is bound.
//
// Like Fill it decides and records; the caller staffs through the reviewed
// seam and then calls Settle.
func (s *Service) FillCreated(projectID string, explicit map[string]bool, grant *Grant, modelReady bool) ([]CreatedFill, error) {
	if s == nil || s.workspaces == nil {
		return nil, ErrNotShared
	}
	project, err := s.workspaces.Get(strings.TrimSpace(projectID))
	if err != nil || project == nil {
		return nil, ErrNotShared
	}
	link := project.GetAssistantProjectLink()
	if link == nil {
		return nil, ErrNotShared
	}
	home, err := s.workspaces.Get(link.StationWorkspaceID)
	if err != nil || home == nil {
		return nil, ErrNotShared
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.Declaration == nil || state.Declaration.SchemaVersion < 2 {
		// An older program hires its whole team at once; it has no scoped roles.
		return nil, ErrNotShared
	}
	filled := map[string]bool{}
	for _, binding := range state.HomeBindings.Bindings {
		filled[binding.RoleID] = true
	}
	for _, binding := range link.ProjectBindings.Bindings {
		filled[binding.RoleID] = true
	}
	roles := append([]workspace.AssistantProgramRoleSpec(nil), state.Declaration.Roles...)
	roles = append(roles, link.ProjectRoles...)
	free := s.freeNames(home, project)
	shared := link.ProjectProvider != nil && link.ProjectProvider.Valid()

	var fills []CreatedFill
	for _, role := range roles {
		if !role.Required || filled[role.ID] || explicit[role.ID] {
			continue
		}
		fill := CreatedFill{RoleID: role.ID, Label: role.Label, Scope: roleScope(role), Mode: ModeCreate}
		if fill.Scope == workspace.AssistantRoleScopeHome {
			if !modelReady {
				continue
			}
			fill.Name = free(homeRoleName(role, state.Declaration.DefaultPrimaryName))
		} else {
			decided := false
			if shared {
				sharedFill, sharedErr := s.sharedCreatedFill(projectID, role, grant, modelReady)
				switch {
				case sharedErr == nil:
					fill.Name, fill.Mode, decided = sharedFill.Name, sharedFill.Mode, true
				case errors.Is(sharedErr, errNoModel):
					continue
				case !errors.Is(sharedErr, ErrNotShared) && !errors.Is(sharedErr, ErrNoConsent) &&
					!errors.Is(sharedErr, ErrConsentStale) && !errors.Is(sharedErr, ErrAssistantMissing):
					return nil, sharedErr
				}
			}
			if !decided {
				if !modelReady {
					continue
				}
				fill.Name = free(role.Label)
			}
		}
		if fill.Name != "" {
			fills = append(fills, fill)
		}
	}
	return fills, nil
}

var errNoModel = errors.New("projectstaffing: no model for a new agent")

// sharedCreatedFill fills one project role under the Home's consent, treating
// the reviewed create as the user's fresh agreement: it renews a consent given
// for an older team, and replaces a recorded assistant that is gone.
func (s *Service) sharedCreatedFill(projectID string, role workspace.AssistantProgramRoleSpec, grant *Grant, modelReady bool) (Fill, error) {
	song, err := s.Song(projectID)
	if err != nil {
		return Fill{}, err
	}
	provider := song.Provider
	consent, err := s.consents.Read(song.Home.ID)
	if err != nil {
		consent = nil
	}
	covers := consent.Covers(provider.PluginID, provider.BlueprintID)
	if grant != nil && covers && consent.RevokedAt == nil && consent.TeamDigest != provider.ProjectTeamDigest {
		if consent, err = s.consents.Grant(song.Home.ID, s.grantFor(song, *grant)); err != nil {
			return Fill{}, err
		}
	}
	recorded := ""
	if covers {
		if entry, found := consent.Role(role.ID); found {
			recorded = entry.AgentName
		}
	}
	binds := recorded != "" && s.agentExists(recorded)
	if !binds && !modelReady {
		return Fill{}, errNoModel
	}
	if recorded != "" && !binds {
		if _, err := s.consents.ForgetAgent(song.Home.ID, provider.PluginID, provider.BlueprintID, role.ID, recorded); err != nil {
			return Fill{}, err
		}
	}
	fills, err := s.Fill(projectID, []string{role.ID}, grant)
	if err != nil {
		return Fill{}, err
	}
	if len(fills) != 1 {
		return Fill{}, ErrNotShared
	}
	return fills[0], nil
}

func roleScope(role workspace.AssistantProgramRoleSpec) workspace.AssistantRoleScope {
	if role.Scope == workspace.AssistantRoleScopeHome {
		return workspace.AssistantRoleScopeHome
	}
	return workspace.AssistantRoleScopeProject
}

// homeRoleName is the name a Home role's new agent gets: the program's own
// name for its primary, otherwise the role's label.
func homeRoleName(role workspace.AssistantProgramRoleSpec, defaultPrimaryName string) string {
	if primary := strings.TrimSpace(defaultPrimaryName); role.Primary && primary != "" {
		return primary
	}
	return role.Label
}

// freeNames returns the first free name for a label, never one an earlier call
// took: an agent the user has, one already in the Home or the project, or a
// name this pass handed out.
func (s *Service) freeNames(home, project *workspace.Workspace) func(label string) string {
	handed := map[string]bool{}
	taken := func(name string) bool {
		key := strings.ToLower(strings.TrimSpace(name))
		if handed[key] {
			return true
		}
		if s.agents != nil {
			for _, existing := range s.agents.ListAgents() {
				if strings.ToLower(strings.TrimSpace(existing)) == key {
					return true
				}
			}
		}
		for _, target := range []*workspace.Workspace{home, project} {
			if target == nil {
				continue
			}
			for _, instance := range target.GetAgentInstances() {
				if strings.ToLower(strings.TrimSpace(instance.Name)) == key {
					return true
				}
			}
		}
		return false
	}
	return func(label string) string {
		name := workspace.FirstFreeAgentName(label, taken)
		if name != "" {
			handed[strings.ToLower(name)] = true
		}
		return name
	}
}
