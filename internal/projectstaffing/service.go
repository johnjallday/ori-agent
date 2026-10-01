// Package projectstaffing fills a linked project's roles with the one shared
// agent the Home's standing consent names: hire once, assign many.
//
// It decides and records; it never staffs. The caller performs the actual
// staffing through the existing reviewed seams (the setup journey's
// review_project_staffing → add_project_staffing, or the staffing adapter's
// StaffRoleOnWorkspace), so every check those seams make still applies.
package projectstaffing

import (
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Agents is the slice of the user's agent store this package reads.
type Agents interface {
	ListAgents() []string
	GetAgent(name string) (*agent.Agent, bool)
}

var (
	// ErrNotShared means the project's roles are not covered by a shared
	// assistant: the program has no split project provider, or the Home's
	// consent is for another blueprint. The caller keeps its own behaviour.
	ErrNotShared = errors.New("projectstaffing: this project's roles are not shared")
	// ErrNoConsent means the Home has no standing consent, or it is switched off.
	ErrNoConsent = errors.New("projectstaffing: no standing consent")
	// ErrConsentStale means the installed blueprint's team changed since the
	// user agreed: they are asked again once, on the Home.
	ErrConsentStale = errors.New("projectstaffing: the consent is for an older team")
	// ErrAssistantMissing means the agent the consent created is gone.
	ErrAssistantMissing = errors.New("projectstaffing: the shared assistant is missing")
)

// Modes a fill uses, as the staffing input spells them.
const (
	ModeCreate = "create"
	ModeBind   = "bind"
)

// Fill is how one project role is filled.
type Fill struct {
	RoleID string
	Label  string
	Name   string
	Mode   string
}

// Grant asks Fill to record a fresh consent when the Home has none (or it is
// switched off): the folder card's Set up is that consent.
type Grant struct {
	Source  string
	OfferID string
}

// Service decides and records shared staffing for linked projects.
type Service struct {
	workspaces workspace.Store
	agents     Agents
	consents   *workspace.ProjectStaffingConsents
}

// New binds the service to the workspace and agent stores.
func New(workspaces workspace.Store, agents Agents) *Service {
	return &Service{workspaces: workspaces, agents: agents, consents: workspace.NewProjectStaffingConsents(workspaces)}
}

// Consents exposes the Home consent store the service writes through.
func (s *Service) Consents() *workspace.ProjectStaffingConsents {
	if s == nil {
		return nil
	}
	return s.consents
}

// Song is a linked project with a split project provider, and its Home.
type Song struct {
	Project  *workspace.Workspace
	Home     *workspace.Workspace
	Link     *workspace.AssistantProjectLink
	Provider workspace.AssistantProjectProviderOwner
}

// Song resolves a linked project. A project without a split provider (no team
// digest to scope a consent to) is ErrNotShared.
func (s *Service) Song(projectID string) (Song, error) {
	if s == nil || s.workspaces == nil {
		return Song{}, ErrNotShared
	}
	project, err := s.workspaces.Get(strings.TrimSpace(projectID))
	if err != nil || project == nil {
		return Song{}, ErrNotShared
	}
	link := project.GetAssistantProjectLink()
	if link == nil || link.ProjectProvider == nil || !link.ProjectProvider.Valid() {
		return Song{}, ErrNotShared
	}
	home, err := s.workspaces.Get(link.StationWorkspaceID)
	if err != nil || home == nil || home.GetAssistantProgramState() == nil || home.GetAssistantProjectLink() != nil {
		return Song{}, ErrNotShared
	}
	return Song{Project: project, Home: home, Link: link, Provider: *link.ProjectProvider}, nil
}

// RequiredRoles are the project roles the song's blueprint requires.
func (song Song) RequiredRoles() []workspace.AssistantProgramRoleSpec {
	if song.Link == nil {
		return nil
	}
	var roles []workspace.AssistantProgramRoleSpec
	for _, role := range song.Link.ProjectRoles {
		if role.Required && role.Scope == workspace.AssistantRoleScopeProject {
			roles = append(roles, role)
		}
	}
	return roles
}

// FilledRoles are the project roles already bound in the song, by role ID.
func (song Song) FilledRoles() map[string]workspace.AgentInstance {
	filled := map[string]workspace.AgentInstance{}
	if song.Link == nil || song.Project == nil {
		return filled
	}
	instances := map[string]workspace.AgentInstance{}
	for _, instance := range song.Project.GetAgentInstances() {
		instances[instance.ID] = instance
	}
	for _, binding := range song.Link.ProjectBindings.Bindings {
		if instance, found := instances[binding.AgentInstanceID]; found && instance.Name == binding.AgentName {
			filled[binding.RoleID] = instance
		}
	}
	return filled
}

// Fill says how each of roleIDs (required, unfilled project roles of the song)
// is filled under the Home's consent. With grant set, a Home with no consent, or
// one switched off, first gets a fresh consent. A create reserves the name it
// will use before returning, so Settle can recognise it afterwards.
func (s *Service) Fill(projectID string, roleIDs []string, grant *Grant) ([]Fill, error) {
	song, err := s.Song(projectID)
	if err != nil {
		return nil, err
	}
	consent, err := s.consents.Read(song.Home.ID)
	if err != nil {
		consent = nil // An unreadable record is no consent; a grant replaces it.
	}
	if grant != nil && (consent == nil || consent.RevokedAt != nil) {
		consent, err = s.consents.Grant(song.Home.ID, s.grantFor(song, *grant))
		if err != nil {
			return nil, err
		}
	}
	roles := map[string]workspace.AssistantProgramRoleSpec{}
	for _, role := range song.RequiredRoles() {
		roles[role.ID] = role
	}
	fills := make([]Fill, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		role, declared := roles[roleID]
		if !declared {
			return nil, ErrNotShared
		}
		decision := workspace.DecideProjectStaffing(consent, workspace.ProjectStaffingFacts{
			PluginID: song.Provider.PluginID, BlueprintID: song.Provider.BlueprintID, TeamDigest: song.Provider.ProjectTeamDigest,
			RoleID: role.ID, RoleLabel: role.Label, AgentExists: s.agentExists, NameTaken: s.nameTaken(song),
		})
		switch decision.Action {
		case workspace.ProjectStaffingCreate:
			if err := s.consents.ReserveAgent(song.Home.ID, song.Provider.PluginID, song.Provider.BlueprintID,
				song.Provider.ProjectTeamDigest, role.ID, decision.AgentName); err != nil {
				return nil, err
			}
			fills = append(fills, Fill{RoleID: role.ID, Label: role.Label, Name: decision.AgentName, Mode: ModeCreate})
		case workspace.ProjectStaffingBind:
			fills = append(fills, Fill{RoleID: role.ID, Label: role.Label, Name: decision.AgentName, Mode: ModeBind})
		case workspace.ProjectStaffingStop:
			if decision.Reason == workspace.ProjectStaffingConsentStale {
				return nil, ErrConsentStale
			}
			return nil, ErrAssistantMissing
		default:
			if decision.Reason == workspace.ProjectStaffingNotCovered {
				return nil, ErrNotShared
			}
			return nil, ErrNoConsent
		}
	}
	return fills, nil
}

func (s *Service) grantFor(song Song, grant Grant) workspace.ProjectStaffingConsentGrant {
	request := workspace.ProjectStaffingConsentGrant{
		Source: grant.Source, OfferID: grant.OfferID, PluginID: song.Provider.PluginID,
		BlueprintID: song.Provider.BlueprintID, TeamDigest: song.Provider.ProjectTeamDigest,
	}
	for _, role := range song.RequiredRoles() {
		request.RoleIDs = append(request.RoleIDs, role.ID)
	}
	return request
}

// Settle records, from the song's own staffing, the agent the consent's create
// made: a role bound to an agent this song created, whose name is the one the
// consent reserved. Nothing else is ever recorded (D3). It is safe to repeat.
func (s *Service) Settle(projectID string) error {
	song, err := s.Song(projectID)
	if err != nil {
		return nil
	}
	consent, err := s.consents.Read(song.Home.ID)
	if err != nil || consent == nil || !consent.Covers(song.Provider.PluginID, song.Provider.BlueprintID) ||
		consent.TeamDigest != song.Provider.ProjectTeamDigest {
		return nil
	}
	filled := song.FilledRoles()
	for _, role := range consent.Roles {
		if role.AgentName != "" || role.PendingName == "" {
			continue
		}
		instance, found := filled[role.RoleID]
		if !found || instance.RoleSource != workspace.RoleSourceCreated || instance.Name != role.PendingName {
			continue
		}
		if err := s.consents.RecordAgent(song.Home.ID, song.Provider.PluginID, song.Provider.BlueprintID,
			song.Provider.ProjectTeamDigest, role.RoleID, instance.Name); err != nil {
			return err
		}
	}
	return nil
}

// AgentExists says whether the user has an agent of that name.
func (s *Service) AgentExists(name string) bool { return s != nil && s.agentExists(name) }

// agentOrigins is the agent store's own answer to where a name comes from.
type agentOrigins interface {
	AgentOrigin(name string) (store.AgentOrigin, bool)
}

// agentExists is true only for an agent in the user's own roster. A song keeps
// its own copy of the agents it had, and the store lists such a workspace-only
// copy by name; that copy is not the shared assistant and is never bound (D3).
func (s *Service) agentExists(name string) bool {
	if s.agents == nil {
		return false
	}
	if _, found := s.agents.GetAgent(name); !found {
		return false
	}
	if origins, ok := s.agents.(agentOrigins); ok {
		origin, found := origins.AgentOrigin(name)
		return found && origin.Source != store.SourceWorkspace
	}
	return true
}

// nameTaken is the create's own collision rule: any agent the user has, or any
// agent already in the Home or the project.
func (s *Service) nameTaken(song Song) func(string) bool {
	return func(name string) bool {
		if s.agents != nil {
			for _, existing := range s.agents.ListAgents() {
				if strings.EqualFold(strings.TrimSpace(existing), name) {
					return true
				}
			}
		}
		for _, target := range []*workspace.Workspace{song.Home, song.Project} {
			for _, instance := range target.GetAgentInstances() {
				if strings.EqualFold(strings.TrimSpace(instance.Name), name) {
					return true
				}
			}
		}
		return false
	}
}
