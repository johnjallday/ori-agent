package projectlibrary

import (
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// KeeperAuthority is the executing song agent that asks to suggest fields for
// its own song (PRD #792 FR20). It names the song workspace the call runs in
// and the executing instance; it carries no entry, Home or revision: the host
// resolves all of those, so the agent cannot point at another song.
type KeeperAuthority struct {
	ChildWorkspaceID string
	AgentInstanceID  string
	AgentName        string
}

// keeperGrant is what an authorized keeper may act on: one Home scope and its
// one connected entry for this song.
type keeperGrant struct {
	scope Scope
	child *workspace.Workspace
	entry Entry
}

// authorizeKeeper resolves the keeper's song and refuses anything short of
// FR20: the owner's live, non-resource song whose own link names a Home; link
// mirrors that agree; a connected reciprocal entry in the Home library; a
// writable Home provider; and the executing instance being the song's entry
// agent (its name is EntryAgentName, it is the one instance with that name,
// and its workspace-local agent, if any, is not disabled).
func (s *Store) authorizeKeeper(keeper KeeperAuthority) (keeperGrant, error) {
	if s == nil || s.workspaces == nil || s.providerEvidence == nil ||
		keeper.ChildWorkspaceID == "" || !validText(keeper.ChildWorkspaceID, 160) ||
		keeper.AgentInstanceID == "" || !validText(keeper.AgentInstanceID, 160) ||
		keeper.AgentName == "" || !validText(keeper.AgentName, 160) {
		return keeperGrant{}, ErrUnavailable
	}
	child, err := s.workspaces.Get(keeper.ChildWorkspaceID)
	if err != nil || child == nil || child.ID != keeper.ChildWorkspaceID || child.OwnerUserID == "" ||
		child.Status == workspace.StatusTrashed || child.Status == workspace.StatusMissing {
		return keeperGrant{}, ErrUnavailable
	}
	link := child.GetAssistantProjectLink()
	if link == nil || link.StationWorkspaceID == "" || workspace.AssistantProjectLinkIsResource(link) {
		return keeperGrant{}, ErrUnavailable
	}
	home, err := s.workspaces.Get(link.StationWorkspaceID)
	if err != nil || home == nil || home.ID != link.StationWorkspaceID || home.OwnerUserID != child.OwnerUserID ||
		home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing {
		return keeperGrant{}, ErrUnavailable
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.Key.Normalize().OwnerUserID != child.OwnerUserID {
		return keeperGrant{}, ErrUnavailable
	}
	key := state.Key.Normalize()
	scope := Scope{OwnerUserID: child.OwnerUserID, HomeID: home.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID}
	if !scope.valid() {
		return keeperGrant{}, ErrUnavailable
	}
	if _, err := s.songChild(scope, child.ID); err != nil {
		return keeperGrant{}, ErrUnavailable
	}
	if !workspace.AssistantProjectLinkMirrorsAgree(s.workspaces, child) {
		return keeperGrant{}, ErrUnavailable
	}
	if !keeperInstance(child, keeper) {
		return keeperGrant{}, ErrUnavailable
	}
	if local, found, err := s.workspaces.GetWorkspaceAgent(child.ID, keeper.AgentName); err != nil ||
		(found && local != nil && local.Status == types.AgentStatusDisabled) {
		return keeperGrant{}, ErrUnavailable
	}
	if !s.providerWritable(scope, home) {
		return keeperGrant{}, ErrUnavailable
	}
	doc, snapshotState, err := s.readSnapshot(scope)
	if err != nil {
		return keeperGrant{}, ErrUnavailable
	}
	views := newProjectionMaps(doc, snapshotState)
	factsOn := snapshotState.GetSongDetailsConsent().Active()
	for _, entry := range doc.Entries {
		if entry.Link == nil || entry.Link.WorkspaceID != child.ID {
			continue
		}
		if s.projectSearchRow(scope, entry, views.roots, views.linked, views.inactive, factsOn).Connection != "connected" {
			return keeperGrant{}, ErrUnavailable
		}
		return keeperGrant{scope: scope, child: child, entry: entry}, nil
	}
	return keeperGrant{}, ErrUnavailable
}

// keeperInstance reports whether the executing instance is the song's entry
// agent: the one instance carrying the entry agent's name, with this ID.
func keeperInstance(child *workspace.Workspace, keeper KeeperAuthority) bool {
	entryName := strings.TrimSpace(child.EntryAgentName())
	if entryName == "" || strings.TrimSpace(keeper.AgentName) != entryName {
		return false
	}
	matched := 0
	ok := false
	for _, instance := range child.AgentInstances {
		if strings.TrimSpace(instance.Name) != entryName {
			continue
		}
		matched++
		ok = instance.ID == keeper.AgentInstanceID
	}
	return matched == 1 && ok
}

// KeeperEnabled is the keeper tool's exposure hint for the executing
// instance. It is rechecked in full at every call.
func (s *Store) KeeperEnabled(keeper KeeperAuthority) bool {
	_, err := s.authorizeKeeper(keeper)
	return err == nil
}

// ProposeFieldsAsKeeper saves the keeper's fields suggestion for its own song
// through the same fenced write as the Manager's (FR20, FR22). It returns the
// proposal only to the tool, which passes on its id and expiry, never entry
// data.
func (s *Store) ProposeFieldsAsKeeper(keeper KeeperAuthority, proposed ProposedFields, reason, requestKey string) (ManagerProposal, bool, error) {
	grant, err := s.authorizeKeeper(keeper)
	if err != nil {
		return ManagerProposal{}, false, err
	}
	proposer := fieldsProposer{Kind: ProposerKeeper, WorkspaceID: grant.child.ID,
		AgentInstanceID: keeper.AgentInstanceID, AgentName: keeper.AgentName}
	return s.proposeFields(grant.scope, proposer, ManagerAuthority{}, grant.entry.ID, grant.entry.Fields.Revision,
		proposed, reason, requestKey, keeperLivePolicy(grant.child.ID, grant.child.OwnerUserID))
}

// keeperLivePolicy is the part of the keeper's authority checkable inside the
// Home's fenced write, from the Home record alone: the song is still one of
// the Home's linked projects of the same owner. The full check runs before
// the write and in every status read; reading the child here would take the
// store lock a second time.
func keeperLivePolicy(childID, ownerID string) func(*workspace.AssistantProgramState, *workspace.Workspace) bool {
	return func(state *workspace.AssistantProgramState, home *workspace.Workspace) bool {
		return state != nil && home != nil && home.OwnerUserID == ownerID && slices.Contains(state.LinkedProjectIDs, childID)
	}
}

// keeperProposalAvailable reports whether a keeper's fields suggestion can
// still be authorized (FR23): the same song, through the same Home and entry,
// by the same instance.
func (s *Store) keeperProposalAvailable(scope Scope, proposal ManagerProposal) bool {
	grant, err := s.authorizeKeeper(KeeperAuthority{ChildWorkspaceID: proposal.ProposerWorkspaceID,
		AgentInstanceID: proposal.AgentInstanceID, AgentName: proposal.AgentName})
	return err == nil && grant.scope.HomeID == scope.HomeID && grant.entry.ID == proposal.EntryID
}
