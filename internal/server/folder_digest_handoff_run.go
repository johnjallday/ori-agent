package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// songLibrary is the Home library for one song's own folder: a root of this
// Home that already covers the song counts as its root (OQ4).
type songLibrary struct{ portfolioLibrary }

func (l songLibrary) State(ctx context.Context, homeID string) (foldersetup.LibraryState, error) {
	if err := l.handler(); err != nil {
		return foldersetup.LibraryState{}, err
	}
	folder := ""
	if digest := l.builder.personalAssistantFolderDigest; digest != nil {
		if path, _, err := digest.PortfolioSetupRoot(ctx, l.userID, l.offerID, homeID); err == nil {
			folder = path
		}
	}
	state, err := l.builder.sessionHandler.SongLibraryState(l.userID, homeID, folder)
	if err != nil {
		return foldersetup.LibraryState{}, err
	}
	if folder == "" {
		state.RootID, state.Scanned = "", false
	}
	return foldersetup.LibraryState{Initialized: state.Initialized, Linked: state.Linked, RootID: state.RootID,
		Scanned: state.Scanned, Listed: state.Listed, Partial: state.Partial}, nil
}

// songRun drives a single song's setup: the collection run for a portfolio
// of one whose library root is the song's own folder (3A), then the Home's
// Portfolio Manager opening it through the library's reviewed open.
func (h *folderSetupHost) songRun(ctx context.Context, req personalassistant.FolderSetupRequest, target setupTarget) error {
	b := h.builder
	if b == nil || b.sessionHandler == nil || b.assistantStaffing == nil || b.projectStaffing == nil || target.provider == nil {
		return errSetupUnavailable
	}
	defer h.forget(req.UserID, target.entry)
	provider := *target.provider
	opener := &handoffOpener{builder: b, userID: req.UserID, offerID: req.Offer.ID, brief: req.Brief,
		workspaceName: setupWorkspaceName(req), shape: folderdigest.Shape(req.Offer.Subject.Shape)}
	library := portfolioLibrary{builder: b, userID: req.UserID, offerID: req.Offer.ID}
	runner := &foldersetup.PortfolioRunner{
		ValidateDestination: func(ctx context.Context, homeID string) error { return h.ValidateSetupDestination(ctx, req) },
		Homes:               &portfolioHomes{host: h, userID: req.UserID, provider: provider},
		Staffing:            portfolioHomeStaffing{builder: b, model: req.Plan.Intent.Model},
		Library:             songLibrary{library},
		Opener:              opener,
		Receipts: handoffReceipts{host: h, song: strings.TrimSpace(req.Offer.Subject.Name), label: target.row.Blueprint.Label,
			opener: opener},
		Folder: req.Path,
		Progress: progressFunc(func(ctx context.Context, update personalassistant.FolderSetupUpdate) error {
			// Stopped at the hand-off, the song waits in its Home: the run names
			// the Home's page, where its manager suggests opening it.
			if update.StopReason == personalassistant.FolderStopHandoffReady && update.HomeID != "" && b.workspaceStore != nil {
				if home, err := b.workspaceStore.Get(update.HomeID); err == nil {
					if route, ok := homeSuggestionsRoute(home); ok {
						update.SuggestedIn = &personalassistant.FolderHomeLink{Name: home.Name, Route: route}
					}
				}
			}
			return req.Update(ctx, update)
		}),
	}
	if req.Plan.Intent.Provider != "" {
		runner.Providers = homeProviderInstaller{setup: folderHomeProviderSetup{builder: b}, key: provider.Key}
	}
	if req.Plan.Intent.Integration != "" {
		install, err := b.setupJourneyService.ForHostQuest(ctx, req.UserID, target.entry.InstallQuestID())
		if err != nil {
			return fmt.Errorf("resolve the install quest: %w", err)
		}
		runner.Install = scopedJourney{service: install, userID: req.UserID}
	}
	if req.Plan.Intent.GrantsConsent {
		runner.Sharing = portfolioSharing{host: h, offerID: req.Offer.ID, blueprintID: target.row.Blueprint.BlueprintID}
	}
	if req.Plan.Intent.GrantsSongDetails {
		runner.SongDetails = portfolioSongDetails{store: b.workspaceStore, offerID: req.Offer.ID}
	}
	if req.Plan.Intent.SetsProfile {
		runner.Profile = folderProfileStep{handler: b.sessionHandler, userID: req.UserID, offerID: req.Offer.ID}
	}
	config := foldersetup.PortfolioConfig{Plan: req.Plan, EntryName: req.EntryName, Open: req.Open}
	if req.Offer.Setup != nil {
		config.HomeID = req.Offer.Setup.HomeID
	}
	if req.Offer.DecidedAt != nil {
		config.AcceptedAfter = *req.Offer.DecidedAt
	}
	result, err := runner.Run(ctx, config)
	if result.Cause != nil {
		logger.Warn("One-card song setup stopped", logger.Fields{
			"offer_id": req.Offer.ID, "reason": result.StopReason, "error": result.Cause.Error(),
		})
	}
	return err
}
