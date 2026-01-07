// Package session provides an adapter for the orchestration system to access session data.
package session

import (
	"context"
	"time"

	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
)

// OrchestrationSessionStore implements the orchestrationhttp.SessionStore interface
// to provide session and task data for the Studios dashboard.
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

// ListTasksByWorkspace returns session tasks for a workspace.
func (a *OrchestrationSessionStore) ListTasksByWorkspace(ctx context.Context, workspaceID string) ([]orchestrationhttp.SessionTaskItem, error) {
	tasks, err := a.store.TaskStore().ListTasksByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	// Convert to orchestrationhttp.SessionTaskItem
	items := make([]orchestrationhttp.SessionTaskItem, len(tasks))
	for i, t := range tasks {
		var completedAt *time.Time
		if t.CompletedAt != nil {
			completedAt = t.CompletedAt
		}
		items[i] = orchestrationhttp.SessionTaskItem{
			ID:          t.ID,
			Description: t.Description,
			Details:     t.Details,
			Status:      string(t.Status),
			Priority:    t.Priority,
			CreatedAt:   t.CreatedAt,
			UpdatedAt:   t.UpdatedAt,
			CompletedAt: completedAt,
		}
	}

	return items, nil
}
