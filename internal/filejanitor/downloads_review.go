package filejanitor

import (
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// DownloadsRoot resolves the current host-verified inbox anchor. It is not a
// path from the browser, a stored report label, or a Filing grant.
type DownloadsRoot func() (string, error)

const (
	downloadsNeedsInstall = "install"
	downloadsNeedsAccess  = "access"
	downloadsReady        = "ready"
)

func resolveDownloadsRoot(verify DownloadsRoot) (string, error) {
	if verify != nil {
		if path, err := verify(); err == nil && filepath.IsAbs(path) {
			if root, err := resolveSetupRoot(path); err == nil {
				return root, nil
			}
		}
	}
	return "", setupErr(CodeFolderChanged, "Ori could not verify this workspace’s Downloads folder.", "", nil)
}

func requireDownloadsSettings(settings JanitorSettings, root string) error {
	if settings.RootPath != "" && filepath.Clean(settings.RootPath) != root {
		return setupErr(CodeFolderChanged, "Filing in this workspace uses a different folder. Downloads was not opened or relinked.", "", nil)
	}
	return nil
}

// DownloadsReviewStatus never evaluates readiness or returns batch context for
// a different root. Opening the review neither grants permission nor scans.
func (s *Service) DownloadsReviewStatus(owner, workspaceID string, verify DownloadsRoot) (Status, error) {
	root, err := resolveDownloadsRoot(verify)
	if err != nil {
		return Status{}, err
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return Status{}, err
	}
	if err := requireDownloadsSettings(settings, root); err != nil {
		return Status{}, err
	}
	ws, err := s.readWorkspace(workspaceID)
	if err != nil || ws == nil {
		return Status{}, setupErr(CodeWorkspaceMissing, "The Downloads workspace is unavailable.", "", err)
	}
	if !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		return Status{DownloadsReview: downloadsNeedsInstall}, nil
	}
	access, err := s.StoredManagedRoot(owner, workspaceID)
	if err != nil {
		return Status{Applies: true, DownloadsReview: downloadsNeedsAccess}, nil
	}
	if access.Path != root || access.Generation != settings.RootID || access.DirectoryReferenceID != settings.DirectoryReferenceID {
		return Status{}, setupErr(CodeFolderChanged, "The Downloads permission changed. Open the review again.", "", nil)
	}
	if s.checkDirectoryAccess(settings).Status == ComponentFailed {
		return Status{Applies: true, DownloadsReview: downloadsNeedsAccess}, nil
	}
	status := s.statusWithSettings(workspaceID, settings)
	status.DownloadsReview = downloadsReady
	return status, nil
}

// ConfirmDownloadsAccess reuses normal setup but cannot relink a different
// managed folder or start monitoring. Only the explicit access control calls it.
func (s *Service) ConfirmDownloadsAccess(owner, workspaceID string, verify DownloadsRoot) (Status, error) {
	paused := true
	return s.confirmSetup(SetupRequest{WorkspaceID: workspaceID, Paused: &paused}, owner, verify)
}
