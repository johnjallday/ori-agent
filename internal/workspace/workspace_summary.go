package workspace

import (
	"errors"
	"time"
)

// WorkspaceSummary is an observational listing projection, not a writable
// Workspace. It deliberately has no orchestration payload, capability state,
// or conversion to Workspace. Hydrate ID through Get before making payload or
// access decisions, and use Update for mutations rather than saving a listing.
type WorkspaceSummary struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Kind        string          `json:"kind,omitempty"`
	Description string          `json:"description,omitempty"`
	FolderSlug  string          `json:"folder_slug,omitempty"`
	OwnerUserID string          `json:"owner_user_id,omitempty"`
	ParentID    string          `json:"parent_id,omitempty"`
	OrderIndex  int             `json:"order_index,omitempty"`
	Status      WorkspaceStatus `json:"status"`
	Version     int64           `json:"version,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// SummaryLister supplies metadata-only candidate identities. It is separate
// from Store so existing callers and custom stores can migrate incrementally.
type SummaryLister interface {
	ListActiveSummaries() ([]WorkspaceSummary, error)
}

// ListActiveSummaries prefers a store's summary API, with a projection-only
// compatibility bridge for legacy stores. A summary API error is authoritative:
// falling back after failure could conceal an unavailable or incomplete source.
func ListActiveSummaries(source interface {
	ListActive() ([]*Workspace, error)
}) ([]WorkspaceSummary, error) {
	if source == nil {
		return nil, errors.New("workspace summary source is unavailable")
	}
	if lister, ok := source.(SummaryLister); ok {
		return lister.ListActiveSummaries()
	}
	return listLegacyWorkspaceSummaries(source)
}

func listLegacyWorkspaceSummaries(source interface {
	ListActive() ([]*Workspace, error)
}) ([]WorkspaceSummary, error) {
	listed, err := source.ListActive()
	if err != nil {
		return nil, err
	}
	summaries := make([]WorkspaceSummary, 0, len(listed))
	for _, ws := range listed {
		if ws != nil {
			summaries = append(summaries, summarizeWorkspace(ws))
		}
	}
	return summaries, nil
}

func summarizeWorkspace(ws *Workspace) WorkspaceSummary {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return WorkspaceSummary{
		ID: ws.ID, Name: ws.Name, Kind: ws.Kind, Description: ws.Description,
		FolderSlug: ws.FolderSlug, OwnerUserID: ws.OwnerUserID, ParentID: ws.ParentID,
		OrderIndex: ws.OrderIndex, Status: ws.Status, Version: ws.Version,
		CreatedAt: ws.CreatedAt, UpdatedAt: ws.UpdatedAt,
	}
}

// ListActiveSummaries projects the existing folder listing, retaining its
// behavior for unreadable records without exposing writable workspace payload.
func (s *FileStore) ListActiveSummaries() ([]WorkspaceSummary, error) {
	return listLegacyWorkspaceSummaries(s)
}

// ListActiveSummaries copies only metadata rather than cloning heavy workspace
// payloads. Summary values contain no mutable maps, slices, or workspace pointers.
func (s *InMemoryStore) ListActiveSummaries() ([]WorkspaceSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	summaries := make([]WorkspaceSummary, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
		summary := summarizeWorkspace(ws)
		if summary.Status == StatusActive {
			summaries = append(summaries, summary)
		}
	}
	return summaries, nil
}

// ListActiveSummaries delegates to the primary metadata source. The folder
// mirror is not consulted: callers hydrate chosen identities when needed.
func (s *SyncStore) ListActiveSummaries() ([]WorkspaceSummary, error) {
	return ListActiveSummaries(s.primary)
}
