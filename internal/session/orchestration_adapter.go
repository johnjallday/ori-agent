// Package session provides an adapter for the orchestration system to access session data.
package session

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
)

// OrchestrationSessionStore implements the orchestrationhttp.SessionStore interface
// to provide session and task data for the Workspaces dashboard.
type OrchestrationSessionStore struct {
	store HybridStore
}

// NewOrchestrationSessionStore creates a new adapter for session data access.
func NewOrchestrationSessionStore(store HybridStore) *OrchestrationSessionStore {
	return &OrchestrationSessionStore{store: store}
}

// ListSessionsByWorkspace returns sessions for a workspace.
func (a *OrchestrationSessionStore) ListSessionsByWorkspace(ctx context.Context, workspaceID string) ([]orchestrationhttp.SessionListItem, error) {
	// Create filter for this workspace
	filter := &SessionFilter{
		FolderID: &workspaceID,
	}

	result, err := a.store.ListSessions(ctx, filter, nil)
	if err != nil {
		return nil, err
	}

	// Convert to orchestrationhttp.SessionListItem
	items := make([]orchestrationhttp.SessionListItem, len(result.Sessions))
	for i, s := range result.Sessions {
		items[i] = orchestrationhttp.SessionListItem{
			ID:           s.ID,
			Title:        s.Title,
			AgentName:    s.AgentName,
			MessageCount: s.MessageCount,
			CreatedAt:    s.CreatedAt,
			UpdatedAt:    s.UpdatedAt,
		}
	}

	return items, nil
}

// ListNotesByWorkspace returns workspace notes for a workspace.
func (a *OrchestrationSessionStore) ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]orchestrationhttp.WorkspaceNoteItem, error) {
	notes, err := a.store.ListNotesByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	// Convert to orchestrationhttp.WorkspaceNoteItem
	items := make([]orchestrationhttp.WorkspaceNoteItem, len(notes))
	for i, n := range notes {
		items[i] = orchestrationhttp.WorkspaceNoteItem{
			ID:        n.ID,
			Name:      n.Name,
			Preview:   n.Preview,
			CreatedAt: n.CreatedAt,
			UpdatedAt: n.UpdatedAt,
		}
	}

	return items, nil
}
