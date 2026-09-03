package personalassistant

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/session"
)

// RecoverySessionWorkspaceStore is the narrow canonical workspace listing seam
// used to find Personal HQ provenance even when its designation was lost.
type RecoverySessionWorkspaceStore interface {
	ListWorkspaces(ctx context.Context) ([]session.Workspace, error)
	GetWorkspace(ctx context.Context, id string) (*session.Workspace, error)
}

// SessionRecoveryWorkspaceReader reduces full workspace records to the bounded
// identity evidence the recovery coordinator is allowed to inspect.
type SessionRecoveryWorkspaceReader struct {
	store RecoverySessionWorkspaceStore
}

func NewSessionRecoveryWorkspaceReader(store RecoverySessionWorkspaceStore) *SessionRecoveryWorkspaceReader {
	return &SessionRecoveryWorkspaceReader{store: store}
}

var _ RecoveryWorkspaceLister = (*SessionRecoveryWorkspaceReader)(nil)

func (r *SessionRecoveryWorkspaceReader) PersonalAssistantRecoveryWorkspaces(ctx context.Context) ([]RecoveryWorkspace, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("personal assistant: recovery workspace reader is unavailable")
	}
	workspaces, err := r.store.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	evidence := make([]RecoveryWorkspace, 0, 1)
	for i := range workspaces {
		workspace, getErr := r.store.GetWorkspace(ctx, workspaces[i].ID)
		if getErr != nil {
			return nil, getErr
		}
		if workspace == nil || workspace.SharedData == nil {
			continue
		}
		if _, present := workspace.SharedData["personal_assistant_presentation"]; !present {
			continue
		}
		item := RecoveryWorkspace{
			ID: strings.TrimSpace(workspace.ID), OwnerUserID: strings.TrimSpace(workspace.OwnerUserID),
			EntryAgents: make([]RecoveryEntryAgent, 0, 1),
		}
		for _, instance := range workspace.AgentInstances {
			if instance.EntryPoint {
				item.EntryAgents = append(item.EntryAgents, RecoveryEntryAgent{ID: instance.ID, Name: instance.Name})
			}
		}
		if presentation, parseErr := parseRecoveryHQPresentation(workspace); parseErr == nil {
			item.AssistantID = presentation.AssistantID
			item.HQRequestID = presentation.RequestID
			item.PresentationValid = true
		}
		evidence = append(evidence, item)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })
	return evidence, nil
}
