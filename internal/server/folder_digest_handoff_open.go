package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// folderHandoffVerifier proves a single song's setup from canonical state
// only: the Home the run recorded, its setup-open receipt for this offer, the
// child that receipt names, the child's linked folder, the reciprocal link and
// the child's page. The run's own report is never evidence.
type folderHandoffVerifier struct{ builder *ServerBuilder }

var _ personalassistant.FolderHandoffVerifier = folderHandoffVerifier{}

func (v folderHandoffVerifier) VerifiedHandoff(ctx context.Context, userID, offerID, homeID, folderPath string, acceptedAfter time.Time) (personalassistant.FolderCreateResult, error) {
	refuse := func(why string) (personalassistant.FolderCreateResult, error) {
		logger.Info("A single-song setup could not be verified", logger.Fields{"reason": why})
		return personalassistant.FolderCreateResult{}, personalassistant.ErrFolderWorkspaceRefused
	}
	b := v.builder
	if b == nil || b.workspaceStore == nil || b.workspaceFileStore == nil || b.sessionStore == nil || homeID == "" || offerID == "" {
		return refuse("not wired")
	}
	home, err := b.workspaceStore.Get(homeID)
	if err != nil || home == nil || home.OwnerUserID != userID || home.Kind != "group" ||
		home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing {
		return refuse("the Home is not the owner's")
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.HomeProvider == nil || state.Key.Normalize().OwnerUserID != userID {
		return refuse("the Home has no provider")
	}
	if _, reviewed := reviewedintegration.HomeProviderFor(state.HomeProvider.PluginID); !reviewed {
		return refuse("the Home's provider is not reviewed")
	}
	receipt, ok := state.SetupOpenReceipt(offerID)
	if !ok || receipt.Validate() != nil || receipt.RecordedAt.Before(acceptedAfter) {
		return refuse("the Home keeps no setup-open receipt for this offer")
	}
	project, err := b.workspaceFileStore.Get(receipt.ProjectWorkspaceID)
	if err != nil || project == nil || project.OwnerUserID != userID || project.ParentID != homeID {
		return refuse("the opened song is not a child of the Home")
	}
	if !projectFolderIs(project, folderPath) {
		return refuse("the opened song is not this folder's")
	}
	link := project.GetAssistantProjectLink()
	if link == nil || link.ID != receipt.LinkID || link.StationWorkspaceID != homeID {
		return refuse("the song's link is not the receipt's")
	}
	homeRoute := (folderJourneyVerifier{builder: b}).verifiedProjectHomeRoute(ctx, userID, project)
	if homeRoute == "" {
		return refuse("the Home and the song do not hold their reciprocal link")
	}
	row, err := b.sessionStore.GetWorkspace(ctx, project.ID)
	if err != nil || row == nil || row.OwnerUserID != userID || row.IsGroup() || !workspace.IsCanonicalWorkspaceSlug(row.FolderSlug) {
		return refuse("the song's page does not resolve")
	}
	parent, err := folderSetupHomeDestination(home)
	if err != nil {
		return refuse("the Home receipt is unavailable")
	}
	result := personalassistant.FolderCreateResult{WorkspaceID: project.ID, Route: "/workspaces/" + row.FolderSlug,
		HomeRoute: homeRoute, Parent: parent, OpenedBy: managerName(home)}
	if b.sessionHandler != nil {
		if rows, receiptErr := b.sessionHandler.FolderOfferWorkspaceReceipt(project.ID, true); receiptErr == nil {
			result.Receipt = rows
		}
	}
	result.Receipt = append(result.Receipt, personalassistant.FolderReceiptRow{Kind: "home", Name: parent.Name,
		Detail: "verified parent; this project is a separate child", Route: homeRoute})
	return result, nil
}

// projectFolderIs says the project's entry is the folder it links where it is.
func projectFolderIs(project *workspace.Workspace, folderPath string) bool {
	locator, err := workspace.GetProjectEntryLocator(project.SharedData)
	if err != nil || locator == nil || locator.Kind != workspace.ProjectEntryDirectoryReference {
		return false
	}
	dir, err := project.GetDirectoryReference(locator.DirectoryReferenceID)
	return err == nil && dir != nil && strings.TrimSpace(dir.Path) != "" && filepath.Clean(dir.Path) == filepath.Clean(folderPath)
}

// handoffOpener is the Home's Portfolio Manager opening the song the run just
// listed, through the library's own reviewed open (the Open row's service). It
// adds what only this setup knows: the key that makes a retry replay, the
// brief as the song's goal (one write, when there is one), the name the review
// gave the workspace, the first task that carries the brief, and the Home's
// setup-open receipt.
type handoffOpener struct {
	builder       *ServerBuilder
	userID        string
	offerID       string
	brief         string
	workspaceName string
	shape         folderdigest.Shape
	// task is the first-task row the seeder read back, for the receipt.
	task *personalassistant.FolderReceiptRow
}

var _ foldersetup.Opener = (*handoffOpener)(nil)

// handoffOpenKey is the activation commit's key for one song of one offer.
func handoffOpenKey(offerID, entryID string) string { return "open:" + offerID + ":" + entryID }

func (o *handoffOpener) ready() bool {
	return o != nil && o.builder != nil && o.builder.sessionHandler != nil && o.builder.personalAssistantFolderDigest != nil
}

// Find is the catalog entry for the song's own folder, which the run proved
// through its awaiting offer; never a folder the browser named.
func (o *handoffOpener) Find(ctx context.Context, homeID, folderName string) (string, error) {
	if !o.ready() {
		return "", errSetupUnavailable
	}
	folder, _, err := o.builder.personalAssistantFolderDigest.PortfolioSetupRoot(ctx, o.userID, o.offerID, homeID)
	if err != nil {
		return "", err
	}
	if filepath.Base(folder) != folderName {
		return "", foldersetup.ErrOpenNotListed
	}
	entryID, err := o.builder.sessionHandler.FindPortfolioSong(o.userID, homeID, folder)
	if errors.Is(err, sessionhttp.ErrLibrarySongNotListed) {
		return "", foldersetup.ErrOpenNotListed
	}
	return entryID, err
}

func (o *handoffOpener) Open(ctx context.Context, in foldersetup.OpenInput) (foldersetup.OpenOutcome, error) {
	if !o.ready() {
		return foldersetup.OpenOutcome{}, errSetupUnavailable
	}
	goal := ""
	if o.brief != "" {
		normalized, err := personalassistant.NormalizeFolderBrief(o.brief)
		if err != nil {
			return foldersetup.OpenOutcome{}, &foldersetup.OpenStop{Reason: personalassistant.FolderStopPlanChanged, Detail: "the brief is not a goal"}
		}
		goal = normalized
	}
	seeder := folderFirstTaskSeeder{builder: o.builder}
	result, err := o.builder.sessionHandler.OpenPortfolioProject(ctx, o.userID, in.HomeID, sessionhttp.LibraryOpenInput{
		EntryID: in.EntryID, SelectedFile: in.SelectedFile, Key: handoffOpenKey(o.offerID, in.EntryID),
		Goal: goal, WorkspaceName: o.workspaceName, SetupOfferID: o.offerID,
		SeedFirstTask: func(songID, _ string) bool {
			row, seedErr := seeder.SeedFirstTask(ctx, personalassistant.FolderFirstTaskRequest{
				UserID: o.userID, WorkspaceID: songID, Shape: o.shape, Brief: o.brief,
			})
			if seedErr != nil {
				logger.Warn("Opened song: its first task could not be seeded", logger.Fields{"workspace_id": songID, "error": seedErr.Error()})
				return false
			}
			o.task = &row
			return true
		},
	})
	var asked *sessionhttp.LibraryOpenStop
	switch {
	case errors.As(err, &asked):
		reason := asked.Reason
		switch reason {
		case sessionhttp.LibraryOpenNeedsChoice:
			reason = personalassistant.FolderStopNeedsChoice
		case sessionhttp.LibraryOpenNeedsModel:
			reason = personalassistant.FolderStopNeedsModel
		case sessionhttp.LibraryOpenConsentStale:
			reason = personalassistant.FolderStopConsentStale
		}
		return foldersetup.OpenOutcome{}, &foldersetup.OpenStop{Reason: reason, Candidates: asked.ProjectFiles, Detail: asked.Message}
	case errors.Is(err, sessionhttp.ErrLibraryFileNotOffered):
		// The file chosen on the card is no longer one the song offers.
		return foldersetup.OpenOutcome{}, &foldersetup.OpenStop{Reason: personalassistant.FolderStopPlanChanged, Detail: "the chosen project file is gone"}
	case err != nil:
		return foldersetup.OpenOutcome{}, err
	}
	return foldersetup.OpenOutcome{
		EntryID: result.EntryID, WorkspaceID: result.WorkspaceID, LinkID: result.LinkID, Route: result.Route,
		Created: result.Created, Staffing: result.Staffing, AgentName: result.AgentName, FirstTask: result.FirstTask,
	}, nil
}
