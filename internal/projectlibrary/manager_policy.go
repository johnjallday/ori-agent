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
	if !boundManager(state, home, authority) {
		return Scope{}, ErrUnavailable
	}
	scope := Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID, ProviderID: state.Key.PluginID,
		ProgramID: state.Key.ProgramID}
	if !scope.valid() || !s.providerWritable(scope, home) {
		return Scope{}, ErrUnavailable
	}
	// A workspace-local snapshot is independent of the Home envelope; check
	// it outside the atomic Home callback, never by taking a second store lock.
	local, found, err := s.workspaces.GetWorkspaceAgent(home.ID, authority.AgentName)
	if err != nil || !found || local == nil || local.Status == types.AgentStatusDisabled {
		return Scope{}, ErrUnavailable
	}
	if _, _, err := s.readSnapshot(scope); err != nil {
		return Scope{}, err
	}
	return scope, nil
}

// boundManager checks the identity on the *current Home snapshot*, and can
// safely be used inside a Home update callback without recursive store reads.
func boundManager(state *workspace.AssistantProgramState, home *workspace.Workspace, authority ManagerAuthority) bool {
	if state == nil || home == nil || state.Declaration == nil ||
		state.Key.Normalize().OwnerUserID != home.OwnerUserID || state.HomeBindings.StateRevision < 1 ||
		home.ID != authority.HomeID || home.OwnerUserID == "" {
		return false
	}
	var instance *workspace.AgentInstance
	for _, current := range home.GetAgentInstances() {
		if current.ID != authority.AgentInstanceID {
			continue
		}
		if instance != nil || strings.TrimSpace(current.Name) != authority.AgentName {
			return false
		}
		copy := current
		instance = &copy
	}
	if instance == nil || instance.RoleID == "" {
		return false
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
	return matched == 1
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

// ManagerSessions is the latest three user-authored summaries for one entry,
// never child transcripts, Ticket bodies, private decision/blocker lists or
// DAW activity. There is no agent paging into older Home history.
type ManagerSessions struct {
	Revision int64            `json:"revision"`
	Total    int              `json:"total"`
	Rows     []SessionSummary `json:"rows"`
}

func (s *Store) SessionsForManager(authority ManagerAuthority, entryID string) (ManagerSessions, error) {
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return ManagerSessions{}, err
	}
	page, err := s.ListSessions(scope, entryID, 0, 0)
	if err != nil {
		return ManagerSessions{}, err
	}
	if _, err := s.authorizeManager(authority); err != nil {
		return ManagerSessions{}, err
	}
	if len(page.Rows) > 3 {
		page.Rows = page.Rows[:3]
	}
	return ManagerSessions{Revision: page.Revision, Total: page.Total, Rows: page.Rows}, nil
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
