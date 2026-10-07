package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func folderSetupWorkspaceDestination(ws *workspace.Workspace, userID string) (*personalassistant.FolderSetupDestination, error) {
	if ws == nil || (ws.OwnerUserID != userID && !(ws.OwnerUserID == "" && userID == "local")) || ws.Status == workspace.StatusTrashed || ws.Status == workspace.StatusMissing {
		return nil, errSetupUnavailable
	}
	kind := "project"
	if ws.Kind == "group" {
		kind = "group"
	}
	entry, _ := workspace.GetProjectEntryLocator(ws.SharedData)
	data, err := json.Marshal(struct {
		Primary    any
		Entry      *workspace.ProjectEntryLocator
		Provenance *workspace.TemplateProvenance
		Link       *workspace.AssistantProjectLink
	}{ws.SharedData[projecttemplates.PrimaryDirectoryIDKey], entry, ws.GetTemplateProvenance(), ws.GetAssistantProjectLink()})
	if err != nil {
		return nil, errSetupUnavailable
	}
	sum := sha256.Sum256(data)
	destination := &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: ws.ID, Name: ws.Name, Kind: kind, ParentID: ws.ParentID, OwnerUserID: userID, RecordVersion: ws.Version, CompatibilityHash: hex.EncodeToString(sum[:])}
	if destination.Validate() != nil {
		return nil, errSetupUnavailable
	}
	return destination, nil
}

// folderProjectLinked reports whether the user's active project still holds this
// exact folder as a user-visible linked directory. It reads the canonical store
// only: a failed read, a trashed or foreign workspace, or a capability-owned
// reference is "not linked", so a completed outcome is never reused on a guess.
func folderProjectLinked(store workspace.Store, userID, workspaceID, folderPath string) bool {
	if store == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(folderPath) == "" {
		return false
	}
	ws, err := store.Get(workspaceID)
	if err != nil || ws == nil || ws.Kind == "group" || ws.GetStatus() != workspace.StatusActive ||
		(ws.OwnerUserID != userID && !(ws.OwnerUserID == "" && userID == "local")) {
		return false
	}
	for _, ref := range ws.DirectoryReferences {
		if ref.Purpose == "" && filepath.Clean(ref.Path) == filepath.Clean(folderPath) {
			return true
		}
	}
	return false
}

// Supporting-folder confirmation adds only a purpose-empty directory reference.
// It does not set a primary directory, project entry, blueprint, mode, roster,
// consent, task or source byte. Canonical store Update serializes duplicate clicks.
func (c folderWorkspaceCreator) linkSupportingFolder(ctx context.Context, req personalassistant.FolderCreateRequest) (personalassistant.FolderCreateResult, error) {
	if c.store == nil || req.Destination == nil || req.Destination.Kind != "project" {
		return personalassistant.FolderCreateResult{}, personalassistant.ErrFolderWorkspaceRefused
	}
	route := ""
	err := c.store.Update(req.Destination.WorkspaceID, func(ws *workspace.Workspace) error {
		actual, err := folderSetupWorkspaceDestination(ws, req.UserID)
		if err != nil {
			return personalassistant.ErrFolderWorkspaceRefused
		}
		// A retry after the link landed returns the exact existing reference;
		// no stale version can add another grant or replace a different root.
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(req.OfferID+"\x00supporting")).String()
		if prior, err := ws.GetDirectoryReference(id); err == nil {
			if prior.Purpose != "" || prior.WorkspaceID != ws.ID || filepath.Clean(prior.Path) != filepath.Clean(req.Path) {
				return personalassistant.ErrFolderWorkspaceRefused
			}
			actual.RecordVersion = req.Destination.RecordVersion
			if *actual != *req.Destination {
				return personalassistant.ErrFolderPlanChanged
			}
		} else {
			if *actual != *req.Destination {
				return personalassistant.ErrFolderPlanChanged
			}
			// Same root with a different display name is already a supporting
			// source; name alone never deduplicates unrelated folders.
			for _, prior := range ws.DirectoryReferences {
				if prior.Purpose == "" && prior.WorkspaceID == ws.ID && filepath.Clean(prior.Path) == filepath.Clean(req.Path) {
					route = "/workspaces/" + ws.FolderSlug
					return nil
				}
			}
			if err := ws.AddDirectoryReference(workspace.DirectoryReference{ID: id, Name: strings.TrimSpace(req.FolderName), Path: req.Path, CreatedAt: time.Now().UTC()}); err != nil {
				return personalassistant.ErrFolderOutcomeUnavailable
			}
		}
		route = "/workspaces/" + ws.FolderSlug
		return nil
	})
	if err != nil {
		return personalassistant.FolderCreateResult{}, err
	}
	return personalassistant.FolderCreateResult{WorkspaceID: req.Destination.WorkspaceID, Route: route, Receipt: []personalassistant.FolderReceiptRow{
		{Kind: "directory", Name: "Linked " + req.FolderName + " as a supporting source in " + req.Destination.Name, Detail: "Read access to this folder; primary project entry, blueprint, mode, agents and tasks unchanged"},
	}}, nil
}
