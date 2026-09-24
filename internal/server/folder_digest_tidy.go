package server

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// tidyCoordinator is the slice of the assistant-led setup coordinator the
// tidy outcome drives: the same run the setup journey's screens drove, with
// the folder step answered from the offer instead of the picker (FR31).
type tidyCoordinator interface {
	Get(ctx context.Context, ownerUserID, selectedWorkspaceID string) (*assistantsetup.Projection, error)
	Accept(ctx context.Context, ownerUserID, proposalRevision, selectedWorkspaceID string) (*assistantsetup.Projection, bool, error)
	BeginFolderIntent(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*assistantsetup.Projection, string, error)
	CommitFolderGrant(ctx context.Context, ownerUserID, workspaceID, token string, commit func(assistantsetup.FolderGrantAuthorization) (assistantsetup.FolderGrantResult, error)) (*assistantsetup.Projection, error)
	PrepareReview(ctx context.Context, ownerUserID, runID string, ifVersion int64, reviewRevision string) (*assistantsetup.Projection, error)
}

// tidyJanitor is the slice of File Janitor the tidy outcome needs.
type tidyJanitor interface {
	FolderOwner(root string) (*filejanitor.RootOwner, bool)
	ConfirmSetup(req filejanitor.SetupRequest) (filejanitor.Status, error)
	Status(workspaceID string) (filejanitor.Status, error)
}

// folderTidyRunner runs the tidy outcome of "show me a folder". Its
// collaborators are resolved at call time because the coordinator is wired
// after the folder digest in the builder's phase order.
type folderTidyRunner struct {
	coordinator func() tidyCoordinator
	janitor     func() tidyJanitor
	route       func(workspaceID string) string
}

func (b *ServerBuilder) newFolderTidyRunner() folderTidyRunner {
	return folderTidyRunner{
		coordinator: func() tidyCoordinator {
			if b == nil || b.assistantSetupService == nil {
				return nil
			}
			return b.assistantSetupService
		},
		janitor: func() tidyJanitor {
			if b == nil || b.fileJanitorService == nil {
				return nil
			}
			return b.fileJanitorService
		},
		route: func(workspaceID string) string {
			if b != nil && b.workspaceFileStore != nil {
				if ws, err := b.workspaceFileStore.Get(workspaceID); err == nil && ws != nil && strings.TrimSpace(ws.FolderSlug) != "" {
					return "/workspaces/" + ws.FolderSlug + "?panel=file-janitor"
				}
			}
			return "/workspaces/" + workspaceID + "?panel=file-janitor"
		},
	}
}

// TidyFolder creates (or adopts) the File Janitor workspace through the
// coordinator, grants it the shown folder, and runs the first review — the
// journey the setup screen used to walk, minus the screen. A folder another
// File Janitor already manages opens that workspace instead (FR32).
func (r folderTidyRunner) TidyFolder(ctx context.Context, req personalassistant.FolderTidyRequest) (personalassistant.FolderTidyResult, error) {
	coordinator, janitor := r.coordinator(), r.janitor()
	if coordinator == nil || janitor == nil {
		return personalassistant.FolderTidyResult{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	root := strings.TrimSpace(req.Path)
	if root == "" {
		return personalassistant.FolderTidyResult{}, personalassistant.ErrFolderPathLost
	}
	if owner, ok := janitor.FolderOwner(root); ok && owner != nil {
		return r.existing(owner.WorkspaceID, "Another File Janitor already tidies this folder, so I opened it."), nil
	}

	projection, err := coordinator.Get(ctx, req.UserID, "")
	if err != nil {
		return personalassistant.FolderTidyResult{}, err
	}
	if projection.Run != nil {
		// A setup is already in flight; it finishes from its own workspace
		// rather than being restarted here.
		return r.existing(projection.Run.TargetWorkspaceID, "A File Janitor setup is already in progress, so I opened it."), nil
	}
	if projection.Proposal == nil {
		if projection.Target != nil && projection.Target.WorkspaceID != "" {
			return r.existing(projection.Target.WorkspaceID, "File Janitor is already set up for another folder. Change its folder there to tidy this one."), nil
		}
		return personalassistant.FolderTidyResult{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	if projection.Proposal.Mode == assistantsetup.TargetChoose {
		return personalassistant.FolderTidyResult{}, personalassistant.ErrFolderOutcomeUnavailable
	}

	accepted, _, err := coordinator.Accept(ctx, req.UserID, projection.Proposal.Revision, projection.Proposal.SelectedWorkspaceID)
	if err != nil {
		return personalassistant.FolderTidyResult{}, err
	}
	if accepted == nil || accepted.Run == nil {
		return personalassistant.FolderTidyResult{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	run := accepted.Run
	workspaceID := run.TargetWorkspaceID
	current := accepted

	if run.CurrentStep == assistantsetup.StepFolder {
		granted, token, err := coordinator.BeginFolderIntent(ctx, req.UserID, run.ID, run.Revision)
		if err != nil {
			return personalassistant.FolderTidyResult{}, err
		}
		if granted != nil && granted.Run != nil {
			run = granted.Run
		}
		paused := true
		var conflict *filejanitor.SetupError
		current, err = coordinator.CommitFolderGrant(ctx, req.UserID, workspaceID, token,
			func(authorization assistantsetup.FolderGrantAuthorization) (assistantsetup.FolderGrantResult, error) {
				status, grantErr := janitor.ConfirmSetup(filejanitor.SetupRequest{
					WorkspaceID: workspaceID, Path: root, Paused: &paused,
					Operation: &filejanitor.FolderGrantOperation{RunID: authorization.RunID, OperationID: authorization.OperationID},
				})
				if grantErr != nil {
					safeCode := "folder_grant_failed"
					var setupErr *filejanitor.SetupError
					if errors.As(grantErr, &setupErr) {
						if setupErr.Code == filejanitor.CodeFolderConflict && setupErr.ConflictWorkspaceID != "" {
							conflict = setupErr
						}
						if strings.TrimSpace(setupErr.Code) != "" {
							safeCode = setupErr.Code
						}
					}
					return assistantsetup.FolderGrantResult{}, &assistantsetup.FolderGrantCommitError{SafeCode: safeCode, Err: grantErr}
				}
				return assistantsetup.FolderGrantResult{
					RootGenerationID: status.Settings.RootID, DirectoryReference: status.Settings.DirectoryReferenceID,
				}, nil
			})
		if err != nil {
			if conflict != nil {
				return r.existing(conflict.ConflictWorkspaceID, "Another File Janitor already tidies this folder, so I opened it."), nil
			}
			return personalassistant.FolderTidyResult{}, err
		}
		if current != nil && current.Run != nil {
			run = current.Run
		}
	} else {
		// An adopted workspace may already hold a folder; only the shown one
		// gets a first review here.
		status, statusErr := janitor.Status(workspaceID)
		if statusErr != nil || !filejanitor.RootsOverlap(status.Settings.RootPath, root) {
			return r.existing(workspaceID, "File Janitor is already set up for another folder. Change its folder there to tidy this one."), nil
		}
	}

	if current == nil || current.Monitoring == nil || run == nil {
		return r.existing(workspaceID, "The folder is linked; open File Janitor to review its first scan."), nil
	}
	reviewed, err := coordinator.PrepareReview(ctx, req.UserID, run.ID, run.Revision, current.Monitoring.Revision)
	if err != nil {
		logger.Warn("First File Janitor review did not complete for a shown folder", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
		return r.existing(workspaceID, "The folder is linked; open File Janitor to review its first scan."), nil
	}
	result := personalassistant.FolderTidyResult{WorkspaceID: workspaceID, Route: r.route(workspaceID)}
	if reviewed != nil && reviewed.FirstResult != nil {
		result.BatchID = reviewed.FirstResult.BatchID
		if route := strings.TrimSpace(reviewed.FirstResult.ReviewRoute); route != "" {
			result.Route = route
		}
	}
	logger.Debug("Tidied shown folder through File Janitor", logger.Fields{"workspace_id": workspaceID, "batch_id": result.BatchID})
	return result, nil
}

func (r folderTidyRunner) existing(workspaceID, note string) personalassistant.FolderTidyResult {
	return personalassistant.FolderTidyResult{WorkspaceID: workspaceID, Route: r.route(workspaceID), Existing: true, Note: note}
}
