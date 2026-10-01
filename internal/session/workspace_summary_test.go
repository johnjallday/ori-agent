package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type summaryProjectionStore struct {
	HybridStore
	rows []Workspace
	err  error
}

func (s *summaryProjectionStore) ListWorkspaces(context.Context) ([]Workspace, error) {
	return s.rows, s.err
}

func TestWorkspaceStoreAdapterSummaries_PreserveMetadataAndLegacyDefaults(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store := &summaryProjectionStore{rows: []Workspace{
		{ID: "active", Name: "Active", FolderSlug: "active", Status: WorkspaceStatusActive,
			Kind: WorkspaceKindGroup, Description: "description", OwnerUserID: "owner", ParentID: "parent", OrderIndex: 4,
			Version: 9, CreatedAt: at, UpdatedAt: at,
			// The summary path has no reason to decode any of these fields, even
			// if a custom HybridStore returns them alongside its listing metadata.
			TasksJSON: []byte("invalid"), InstalledCapabilitiesJSON: []byte("invalid")},
		{ID: "legacy", Name: "Legacy", Status: "", Kind: "", OwnerUserID: ""},
		{ID: "inactive", Status: WorkspaceStatusTrashed},
		{ID: "missing", Status: WorkspaceStatusMissing},
	}}
	listed, err := NewWorkspaceStoreAdapter(store).ListActiveSummaries()
	if err != nil || len(listed) != 2 {
		t.Fatalf("summary listing = %+v, err=%v", listed, err)
	}
	got := listed[0]
	if got.ID != "active" || got.Name != "Active" || got.FolderSlug != "active" || got.Kind != string(WorkspaceKindGroup) ||
		got.Description != "description" || got.OwnerUserID != "owner" || got.ParentID != "parent" || got.OrderIndex != 4 ||
		got.Status != workspace.StatusActive || got.Version != 9 || !got.CreatedAt.Equal(at) || !got.UpdatedAt.Equal(at) {
		t.Fatalf("summary metadata changed: %+v", got)
	}
	legacy := listed[1]
	if legacy.Status != workspace.StatusActive || legacy.Kind != string(WorkspaceKindWorkspace) || legacy.OwnerUserID != "local" {
		t.Fatalf("legacy metadata defaults changed: %+v", legacy)
	}
}

func TestWorkspaceStoreAdapterSummaries_PropagateErrorAndReturnEmptyList(t *testing.T) {
	failure := errors.New("listing failed")
	if _, err := NewWorkspaceStoreAdapter(&summaryProjectionStore{err: failure}).ListActiveSummaries(); !errors.Is(err, failure) {
		t.Fatalf("listing error = %v, want %v", err, failure)
	}
	listed, err := NewWorkspaceStoreAdapter(&summaryProjectionStore{}).ListActiveSummaries()
	if err != nil || listed == nil || len(listed) != 0 {
		t.Fatalf("empty listing = %#v, err=%v", listed, err)
	}
}
