package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var errFolderScanSourceUnavailable = errors.New("folder scan source is unavailable")

// folderScanAuthority revalidates a fact learned from a shown folder
// (FR38): the folder must still exist at its canonical path, be a
// directory, pass the root rules, and, for a marker-based project, still
// carry its marker. The path comes from the workspace the offer created —
// its primary linked directory — so the dossier never needs to store one.
type folderScanAuthority struct {
	digest   folderScanDigest
	files    *workspace.FileStore
	validate func(raw string) (string, error)
}

// folderScanDigest is the slice of the offer service the authority reads.
type folderScanDigest interface {
	ResolvedProjectOfferForKey(ctx context.Context, userID, key string) (personalassistant.FolderOffer, bool, error)
}

func (a folderScanAuthority) Revalidate(ctx context.Context, binding personalassistant.KnowledgeBinding, item personalassistant.KnowledgeItem) error {
	if a.digest == nil || a.files == nil || item.SourceKind != personalassistant.FolderScanSourceKind {
		return errFolderScanSourceUnavailable
	}
	var evidence []personalassistant.KnowledgeEvidence
	for _, revision := range item.Revisions {
		if revision.ID == item.CurrentRevisionID {
			evidence = revision.Evidence
			break
		}
	}
	if len(evidence) != 1 || evidence[0].SourceKind != personalassistant.FolderScanSourceKind || strings.TrimSpace(evidence[0].SourceID) == "" {
		return errFolderScanSourceUnavailable
	}
	key := evidence[0].SourceID
	offer, found, err := a.digest.ResolvedProjectOfferForKey(ctx, binding.UserID, key)
	if err != nil || !found {
		return errFolderScanSourceUnavailable
	}
	folderWS, err := a.files.Get(offer.Outcome.WorkspaceID)
	if err != nil || folderWS == nil {
		return errFolderScanSourceUnavailable
	}
	primary, _ := folderWS.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
	ref, err := folderWS.GetDirectoryReference(strings.TrimSpace(primary))
	if err != nil || ref == nil {
		return errFolderScanSourceUnavailable
	}
	path := filepath.Clean(ref.Path)
	if personalassistant.FolderKey(path) != key {
		return errFolderScanSourceUnavailable
	}
	if a.validate != nil {
		canonical, err := a.validate(path)
		if err != nil || personalassistant.FolderKey(canonical) != key {
			return errFolderScanSourceUnavailable
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return errFolderScanSourceUnavailable
	}
	if offer.Subject.MarkerName != "" && !folderdigest.HasMarker(path, offer.Subject.MarkerName) {
		return errFolderScanSourceUnavailable
	}
	return nil
}
