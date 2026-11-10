package server

import (
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// folderHomePlacementStore is what the Home step's placement needs: moving
// the folder workspace into the Home and promoting it in place.
type folderHomePlacementStore interface {
	workspace.PromoteStore
	MoveWorkspaceFolder(string, string) ([]workspace.MovedWorkspace, error)
}

// placeFolderWorkspaceInHome puts the fed folder's workspace inside the Home
// and makes it a group in place, so the songs set up from it later can sit
// inside it (the folder becomes a place). Same id, anchor, agent, tickets and
// goal offer: promotion changes the kind and adds the members' directory, and
// never moves, renames or reads the anchored folder.
//
// Only the move can fail the step. A refused promotion (running work, the
// nesting limit) leaves the workspace a plain workspace inside the Home, as
// before; a later song set-up promotes it then, or falls back to the Home.
func placeFolderWorkspaceInHome(store folderHomePlacementStore, workspaceID, homeID string) error {
	if current, err := store.Get(workspaceID); err == nil && current != nil && current.ParentID != homeID {
		if _, err := store.MoveWorkspaceFolder(workspaceID, homeID); err != nil {
			return err
		}
	}
	if _, err := workspace.PromoteWorkspaceToGroup(store, workspaceID); err != nil {
		logger.Warn("Folder workspace stays a plain workspace inside its Home", logger.Fields{
			"workspace_id": workspaceID, "home_id": homeID, "error": err,
		})
	}
	return nil
}
