package session

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceTaskContextAdapter exposes note/session summaries for workspace task prompts.
type WorkspaceTaskContextAdapter struct {
	store HybridStore
}

// NewWorkspaceTaskContextAdapter creates a task prompt adapter backed by the session store.
func NewWorkspaceTaskContextAdapter(store HybridStore) *WorkspaceTaskContextAdapter {
	return &WorkspaceTaskContextAdapter{store: store}
}

// ListNotesByWorkspace returns note summaries for the workspace task prompt.
func (a *WorkspaceTaskContextAdapter) ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]workspace.TaskPromptNoteSummary, error) {
	notes, err := a.store.ListNotesByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	items := make([]workspace.TaskPromptNoteSummary, len(notes))
	for i, note := range notes {
		items[i] = workspace.TaskPromptNoteSummary{
			ID:      note.ID,
			Name:    note.Name,
			Preview: note.Preview,
		}
	}

	return items, nil
}

// ListSessionsByWorkspace returns recent session summaries plus the total workspace session count.
func (a *WorkspaceTaskContextAdapter) ListSessionsByWorkspace(ctx context.Context, workspaceID string, limit int) ([]workspace.TaskPromptSessionSummary, int, error) {
	filter := &SessionFilter{
		FolderID: &workspaceID,
	}
	opts := &ListOptions{
		Limit: limit,
		Sort:  SortByUpdatedDesc,
	}

	result, err := a.store.ListSessions(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	if result == nil {
		return nil, 0, nil
	}

	items := make([]workspace.TaskPromptSessionSummary, len(result.Sessions))
	for i, sessionItem := range result.Sessions {
		items[i] = workspace.TaskPromptSessionSummary{
			Title:     sessionItem.Title,
			AgentName: sessionItem.AgentName,
			UpdatedAt: sessionItem.UpdatedAt,
		}
	}

	return items, result.Total, nil
}
