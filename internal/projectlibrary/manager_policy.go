package projectlibrary

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ManagerAuthority is never constructed from an agent-supplied Home ID. The
// host runtime passes the exact attached instance it resolved; the policy
// rereads it and the Home binding on every invocation. A global agent with the
// same name, an unfilled optional role, or a local unbound attachment cannot
// acquire portfolio visibility by choosing a Home URL or invoking the service.
type ManagerAuthority struct {
	HomeID          string
	AgentInstanceID string
	AgentName       string
}

func (s *Store) authorizeManager(authority ManagerAuthority) (Scope, error) {
	if s == nil || s.workspaces == nil || s.providerEvidence == nil ||
		!validText(authority.HomeID, 160) || authority.HomeID == "" ||
		!validText(authority.AgentInstanceID, 160) || authority.AgentInstanceID == "" ||
		!validText(authority.AgentName, 160) || authority.AgentName == "" {
		return Scope{}, ErrUnavailable
	}
	home, err := s.workspaces.Get(authority.HomeID)
	if err != nil || home == nil || home.ID != authority.HomeID || home.OwnerUserID == "" ||
		home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing {
		return Scope{}, ErrUnavailable
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.Declaration == nil || state.Key.Normalize().OwnerUserID != home.OwnerUserID ||
		state.HomeBindings.StateRevision < 1 {
		return Scope{}, ErrUnavailable
	}
	scope := Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID, ProviderID: state.Key.PluginID,
		ProgramID: state.Key.ProgramID}
	if !scope.valid() || !s.providerWritable(scope, home) {
		return Scope{}, ErrUnavailable
	}
	var instance *workspace.AgentInstance
	for _, current := range home.GetAgentInstances() {
		if current.ID != authority.AgentInstanceID {
			continue
		}
		if instance != nil || strings.TrimSpace(current.Name) != authority.AgentName {
			return Scope{}, ErrUnavailable
		}
		copy := current
		instance = &copy
	}
	if instance == nil || instance.RoleID == "" {
		return Scope{}, ErrUnavailable
	}
	var matched int
	for _, role := range state.Declaration.Roles {
		if role.ID == instance.RoleID && role.Scope == workspace.AssistantRoleScopeHome && role.Primary && role.Required {
			for _, binding := range state.HomeBindings.Bindings {
				if binding.RoleID == role.ID && binding.AgentInstanceID == instance.ID && binding.AgentName == authority.AgentName {
					matched++
				}
			}
		}
	}
	if matched != 1 {
		return Scope{}, ErrUnavailable
	}
	// An instance entry can remain after its workspace-local definition is
	// removed. Never fall back to an unrelated global agent with the same name.
	local, found, err := s.workspaces.GetWorkspaceAgent(home.ID, authority.AgentName)
	if err != nil || !found || local == nil || local.Status == types.AgentStatusDisabled {
		return Scope{}, ErrUnavailable
	}
	if _, _, err := s.readSnapshot(scope); err != nil {
		return Scope{}, err
	}
	return scope, nil
}

// CanReadAsManager is only a conservative tool-exposure hint. Every tool
// invocation must repeat this authorization before reading any records.
func (s *Store) CanReadAsManager(authority ManagerAuthority) bool {
	_, err := s.authorizeManager(authority)
	return err == nil
}

// SearchForManager returns at most five Home-authored catalog rows per call.
// The service fixes the page size even when a tool input requests more, and
// does not accept a root ID as a filesystem or project-access capability.
func (s *Store) SearchForManager(authority ManagerAuthority, query Search) (SearchPage, error) {
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return SearchPage{}, err
	}
	query.PageSize = 5
	query.RootID = ""
	page, err := s.Query(scope, query)
	if err != nil {
		return SearchPage{}, err
	}
	if _, err := s.authorizeManager(authority); err != nil {
		return SearchPage{}, err
	}
	return page, nil
}

// DetailForManager excludes directory sources and project file alternatives;
// even a root ID from a prior authorized row is not a grant to inspect files.
func (s *Store) DetailForManager(authority ManagerAuthority, entryID string) (Detail, error) {
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return Detail{}, err
	}
	detail, err := s.Detail(scope, entryID)
	if err != nil {
		return Detail{}, err
	}
	if _, err := s.authorizeManager(authority); err != nil {
		return Detail{}, err
	}
	detail.Sources = nil
	return detail, nil
}
