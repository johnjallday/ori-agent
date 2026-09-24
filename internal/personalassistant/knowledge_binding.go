package personalassistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/session"
)

// KnowledgeBinding is a snapshot of the server-owned, current relationship to
// the designated Personal HQ. It is not an API request type and never grants
// authority by itself: callers re-resolve it before reads and writes.
type KnowledgeBinding struct {
	UserID               string
	AssistantID          string
	HQWorkspaceID        string
	HQFolderSlug         string
	EntryAgentInstanceID string
	StateVersion         int64
	Paused               bool
}

// KnowledgeResolver verifies all links before locating an HQ knowledge store.
// Unlike the older active-HQ projection, this also checks the global profile's
// durable assistant-ID marker: an agent with the same name is not sufficient.
type KnowledgeResolver struct {
	states   Store
	hq       PersonalHQReader
	profiles ProfileReader
}

func NewKnowledgeResolver(states Store, hq PersonalHQReader, profiles ProfileReader) *KnowledgeResolver {
	return &KnowledgeResolver{states: states, hq: hq, profiles: profiles}
}

func (r *KnowledgeResolver) Resolve(ctx context.Context, userID string) (KnowledgeBinding, error) {
	if r == nil || r.states == nil || r.hq == nil || r.profiles == nil || strings.TrimSpace(userID) == "" {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	userID = strings.TrimSpace(userID)
	state, err := r.states.GetState(ctx, userID)
	if err != nil {
		return KnowledgeBinding{}, err
	}
	if state == nil || state.UserID != userID || state.ValidateStateInvariants() != nil || state.RenameStep != RenameNone {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	if state.Status == StatusAwaitingHQ || state.Status == StatusProvisioningHQ {
		return KnowledgeBinding{}, ErrNeedsHQ
	}
	if state.Status != StatusActive && state.Status != StatusPaused {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	profile, ok := r.profiles.PersonalAssistantProfileProvenance(state.GlobalAgentProfileName)
	if !ok || !profile.OwnedBy(state.AssistantID) || !strings.EqualFold(profile.Name, state.GlobalAgentProfileName) {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	status, err := r.hq.Status(ctx, userID)
	if err != nil {
		return KnowledgeBinding{}, fmt.Errorf("personal assistant: resolve personal hq: %w", err)
	}
	if status == nil || !status.Valid || status.UserID != userID || status.WorkspaceID != state.HQWorkspaceID || status.Workspace == nil {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	ws := status.Workspace
	if ws.ID != state.HQWorkspaceID || strings.TrimSpace(ws.FolderSlug) == "" ||
		(ws.OwnerUserID != "" && ws.OwnerUserID != userID) || ws.IsGroup() ||
		ws.Status == session.WorkspaceStatusMissing || ws.Status == session.WorkspaceStatusTrashed {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	// Status.Valid is the authoritative profile-backed designation check. Its
	// Workspace.Designation field is only a best-effort folder projection and
	// can lag the designation without changing the underlying permission.
	linked := 0
	for _, instance := range ws.AgentInstances {
		if instance.ID != state.HQEntryAgentInstanceID {
			continue
		}
		if !instance.EntryPoint || !strings.EqualFold(strings.TrimSpace(instance.Name), strings.TrimSpace(profile.Name)) {
			return KnowledgeBinding{}, ErrRepairNeeded
		}
		linked++
	}
	if linked != 1 || (status.EntryAgentInstanceID != "" && status.EntryAgentInstanceID != state.HQEntryAgentInstanceID) {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	return KnowledgeBinding{
		UserID: userID, AssistantID: state.AssistantID, HQWorkspaceID: ws.ID,
		HQFolderSlug: ws.FolderSlug, EntryAgentInstanceID: state.HQEntryAgentInstanceID,
		StateVersion: state.StateVersion, Paused: state.Status == StatusPaused,
	}, nil
}
