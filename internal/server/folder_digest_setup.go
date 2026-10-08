package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// errSetupUnavailable keeps a card on the step-by-step journey when the
// one-card path cannot be planned or run honestly.
var errSetupUnavailable = errors.New("one-card folder setup is unavailable")

type folderSetupPreviewError string

func (e folderSetupPreviewError) Error() string              { return string(e) }
func (e folderSetupPreviewError) Unwrap() error              { return errSetupUnavailable }
func (e folderSetupPreviewError) SetupPreviewReason() string { return string(e) }

// planFactsTTL keeps a card's plan stable while it is read repeatedly, and keeps
// reading a plugin's release from becoming a request per poll.
const planFactsTTL = 15 * time.Second

// folderSetupHost plans and runs a recognized project's one-card setup over the
// real setup journey. The runner re-reads the journey each pass and the digest
// service stores the run on the offer; the host keeps only a short plan cache.
type folderSetupHost struct {
	builder *ServerBuilder

	mu    sync.Mutex
	cache map[string]cachedPlanFacts
	// apps is the last look for installed applications a plan line was worded
	// from, and when it was made.
	apps   []folderdigest.InstalledApp
	appsAt time.Time
}

type cachedPlanFacts struct {
	at    time.Time
	facts foldersetup.PlanFacts
}

var _ personalassistant.FolderSetupRunner = (*folderSetupHost)(nil)

// setupTarget is what a recognized project offer resolves to.
type setupTarget struct {
	row   folderdigest.CapabilityRow
	entry reviewedintegration.Entry
	// provider is the reviewed plugin that supplies the blueprint's Home, if any.
	provider *reviewedintegration.HomeProvider
}

func (h *folderSetupHost) target(offer personalassistant.FolderOffer) (setupTarget, error) {
	b := h.builder
	if b == nil {
		return setupTarget{}, errSetupUnavailable
	}
	row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension)
	if !ok || row.Offer == nil {
		return setupTarget{}, errSetupUnavailable
	}
	entry, ok := reviewedintegration.Get(row.Offer.IntegrationKey)
	if !ok {
		return setupTarget{}, errSetupUnavailable
	}
	target := setupTarget{row: row, entry: entry}
	// The reviewed registry ties a Home provider to an integration through the
	// program the integration expects, so the plan can name the provider before
	// the integration's own plugin exists to declare it.
	for _, candidate := range reviewedintegration.HomeProviders() {
		if candidate.ProgramID != "" && candidate.ProgramID == entry.ExpectedProgramID {
			provider := candidate
			target.provider = &provider
			break
		}
	}
	return target, nil
}

// integrationFacts uses the same canonical prerequisite reader as the install
// journey, but never its Read API: Read creates/reconciles inert journey rows.
// Opening a card or supplying review context must remain observational.
func (h *folderSetupHost) integrationFacts(ctx context.Context, userID string, entry reviewedintegration.Entry) (foldersetup.Plugin, error) {
	if h.builder == nil || h.builder.reviewedIntegrationReader == nil {
		return foldersetup.Plugin{}, errSetupUnavailable
	}
	read, err := h.builder.reviewedIntegrationReader.Read(ctx, setupjourney.ReadScope{
		OwnerUserID: userID, IntegrationKey: entry.Key, ExpectedBlueprintID: entry.ExpectedBlueprintID,
		ExpectedAssistantProgramID: entry.ExpectedProgramID,
	})
	if err != nil || read.Integration == nil {
		return foldersetup.Plugin{}, errSetupUnavailable
	}
	shown := read.Integration
	plugin := foldersetup.Plugin{Name: entry.DisplayName, PluginID: entry.PluginID, Source: entry.SourceLabel}
	if shown.DevelopmentCopy {
		plugin.Source = "Local development copy — not release-verified"
	}
	hasAction := func(id setupjourney.ActionID) bool {
		for _, action := range read.AvailableActions {
			if action == id {
				return true
			}
		}
		return false
	}
	switch {
	case read.Complete:
		plugin.State, plugin.Version, plugin.InstalledVersion = personalassistant.FolderInstallReady, shown.InstalledVersion, shown.InstalledVersion
	case hasAction(setupjourney.ActionReviewInstall):
		plugin.State, plugin.Version = personalassistant.FolderInstallInstall, shown.ExpectedVersion
	case hasAction(setupjourney.ActionReviewEnable):
		plugin.State, plugin.Version, plugin.InstalledVersion = personalassistant.FolderInstallEnable, shown.InstalledVersion, shown.InstalledVersion
	case hasAction(setupjourney.ActionReviewUpdate):
		plugin.State, plugin.Version, plugin.InstalledVersion = personalassistant.FolderInstallUpdate, shown.ExpectedVersion, shown.InstalledVersion
	default:
		// Unsupported platform, no reviewed release, or another block: the
		// journey explains it, the one-card path does not guess.
		return foldersetup.Plugin{}, errSetupUnavailable
	}
	return plugin, nil
}

// providerFacts reads where the Home provider stands from the reviewed-provider
// preview the portfolio path already uses.
func (h *folderSetupHost) providerFacts(ctx context.Context, provider reviewedintegration.HomeProvider) (foldersetup.Plugin, error) {
	preview, err := (folderHomeProviderSetup{builder: h.builder}).Preview(ctx, provider.Key)
	if err != nil {
		return foldersetup.Plugin{}, errSetupUnavailable
	}
	plugin := foldersetup.Plugin{
		Name: provider.DisplayName, PluginID: provider.PluginID, Version: preview.Version, Source: provider.SourceLabel,
	}
	if preview.DevelopmentCopy {
		plugin.Source = "Local development copy — not release-verified"
	}
	switch {
	case preview.Ready:
		plugin.State = personalassistant.FolderInstallReady
	case preview.Installed && preview.Update:
		plugin.State, plugin.InstalledVersion = personalassistant.FolderInstallUpdate, preview.InstalledVersion
	case preview.Installed:
		plugin.State = personalassistant.FolderInstallEnable
	default:
		plugin.State = personalassistant.FolderInstallInstall
	}
	return plugin, nil
}

func (h *folderSetupHost) facts(ctx context.Context, req personalassistant.FolderSetupRequest, target setupTarget) (foldersetup.PlanFacts, error) {
	key := req.UserID + "|" + target.entry.Key
	h.mu.Lock()
	cached, ok := h.cache[key]
	h.mu.Unlock()
	if !ok || time.Since(cached.at) > planFactsTTL {
		facts := foldersetup.PlanFacts{AppName: target.entry.DisplayName, BlueprintLabel: target.row.Blueprint.Label}
		integration, err := h.integrationFacts(ctx, req.UserID, target.entry)
		if err != nil {
			return foldersetup.PlanFacts{}, folderSetupPreviewError("integration_unavailable")
		}
		facts.Integration = integration
		if target.provider != nil {
			provider, err := h.providerFacts(ctx, *target.provider)
			if err != nil {
				return foldersetup.PlanFacts{}, folderSetupPreviewError("home_provider_unavailable")
			}
			facts.Provider = &provider
		}
		cached = cachedPlanFacts{at: time.Now(), facts: facts}
		h.mu.Lock()
		if h.cache == nil {
			h.cache = map[string]cachedPlanFacts{}
		}
		h.cache[key] = cached
		h.mu.Unlock()
	}
	facts := cached.facts
	facts.Destination = &personalassistant.FolderSetupDestination{Status: "standalone"}
	if target.provider != nil {
		// Software preview caching is never a destination/ownership lease.
		home, err := h.station(req.UserID, *target.provider)
		if err != nil {
			return foldersetup.PlanFacts{}, err
		}
		facts.Grouped = true
		facts.HomeTemplate = "plugin:" + target.entry.PluginID + ":" + target.entry.ExpectedBlueprintID
		if home != nil {
			destination, err := folderSetupHomeDestination(home)
			if err != nil {
				return foldersetup.PlanFacts{}, err
			}
			facts.HomeExists, facts.HomeName, facts.Destination = true, home.Name, destination
			facts.SharingOff = reviewedHomeSharingOff(h.builder, req.UserID, target.provider.Key)
		} else {
			destination, err := h.ReadSetupDestination(ctx, req)
			if err != nil || destination == nil {
				return foldersetup.PlanFacts{}, errSetupUnavailable
			}
			facts.Destination, facts.HomeName = destination, destination.Name
		}
	}
	facts.WorkspaceName = strings.TrimSpace(req.Offer.Subject.Name)
	facts.AppInstalled = req.AppInstalled
	// Worded per plan from the owner's Home as it is now: what that Home
	// already knows changes the sentence. A Home that cannot be read is treated
	// as none; the run decides for itself and never grants more than the plan.
	if target.provider != nil {
		var home *workspace.Workspace
		if facts.HomeExists {
			home, _ = h.station(req.UserID, *target.provider)
		}
		facts.Profile = h.profileFacts(*target.provider, target.row.Offer.IntegrationKey, home)
	}
	if facts.WorkspaceName == "" {
		return foldersetup.PlanFacts{}, errSetupUnavailable
	}
	return facts, nil
}

// reviewedHomeSharingOff says the owner's Home for a reviewed provider has a
// shared-assistant consent that was switched off. Anything unreadable is false:
// the plan then simply does not mention the switch.
func reviewedHomeSharingOff(b *ServerBuilder, userID, providerKey string) bool {
	if b == nil || b.workspaceFileStore == nil {
		return false
	}
	for _, entry := range reviewedintegration.HomeProviders() {
		if entry.Key != providerKey {
			continue
		}
		station, err := workspace.NewAssistantProgramStore(b.workspaceFileStore).FindStation(workspace.AssistantProgramKey{
			OwnerUserID: userID, PluginID: entry.PluginID, ProgramID: entry.ProgramID,
		})
		if err != nil || station == nil {
			return false
		}
		consent := station.GetAssistantProgramState().GetProjectStaffingConsent()
		return consent != nil && consent.Validate() == nil && consent.RevokedAt != nil
	}
	return false
}

// forget drops cached facts, so the next plan reflects what a run just changed.
func (h *folderSetupHost) forget(userID string, entry reviewedintegration.Entry) {
	h.mu.Lock()
	delete(h.cache, userID+"|"+entry.Key)
	h.mu.Unlock()
}

// Plan lists every consequence of Set up for the offer, in the order the card
// shows them. Every string is plain text and names no path.
func (h *folderSetupHost) Plan(ctx context.Context, req personalassistant.FolderSetupRequest) (personalassistant.FolderSetupPlan, error) {
	if req.Offer.Portfolio != nil {
		return h.portfolioPlan(ctx, req)
	}
	target, err := h.target(req.Offer)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, err
	}
	facts, err := h.facts(ctx, req, target)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, err
	}
	return foldersetup.BuildPlan(facts), nil
}

// Run brings the plugins the plan promised into place, then drives the plugin's
// quest to the end or to the first stop. Progress is reported through req.Update;
// only an unexpected error is returned.
func (h *folderSetupHost) Run(ctx context.Context, req personalassistant.FolderSetupRequest) error {
	if h == nil || h.builder == nil || h.builder.setupJourneyService == nil {
		return errSetupUnavailable
	}
	if err := h.ValidateSetupDestination(ctx, req); err != nil {
		return err
	}
	if req.Offer.Portfolio != nil {
		return h.portfolioRun(ctx, req)
	}
	target, err := h.target(req.Offer)
	if err != nil {
		return err
	}
	defer h.forget(req.UserID, target.entry)
	b := h.builder
	runner := &foldersetup.Runner{
		ValidateDestination: func(ctx context.Context) error { return h.ValidateSetupDestination(ctx, req) },
		Selections:          folderSelections{builder: b, path: req.Path},
		Progress:            progressFunc(req.Update),
		OpenProject: func(ctx context.Context) (foldersetup.Journey, error) {
			// The plugin's own quest exists only once the plugin does: one quest,
			// exactly, or the run cannot be finished.
			pluginID, questID, found := reviewedProjectQuest(ctx, b, target.row.Offer.IntegrationKey, target.row.Blueprint.BlueprintID)
			if !found {
				return nil, errSetupUnavailable
			}
			scoped, err := b.setupJourneyService.ForQuest(ctx, req.UserID, pluginID, questID)
			if err != nil {
				return nil, fmt.Errorf("resolve the setup quest: %w", err)
			}
			return scopedJourney{service: scoped, userID: req.UserID}, nil
		},
	}
	if intent := req.Plan.Intent; intent.Integration != "" {
		install, err := b.setupJourneyService.ForHostQuest(ctx, req.UserID, target.entry.InstallQuestID())
		if err != nil {
			return fmt.Errorf("resolve the install quest: %w", err)
		}
		runner.Install = scopedJourney{service: install, userID: req.UserID}
	}
	if target.provider != nil && req.Plan.Intent.Provider != "" {
		runner.Providers = homeProviderInstaller{setup: folderHomeProviderSetup{builder: b}, key: target.provider.Key}
	}
	if req.Plan.Intent.SetsProfile {
		runner.Profile = folderProfileStep{handler: b.sessionHandler, userID: req.UserID, offerID: req.Offer.ID}
	}
	if b.projectStaffing != nil {
		// Set up on the card is the standing consent (D1, D9): the song's Home
		// records it and the song gets the Home's one shared assistant.
		runner.Shared = sharedStaffing{service: b.projectStaffing, grant: &projectstaffing.Grant{
			Source: workspace.ProjectStaffingConsentFolderOffer, OfferID: req.Offer.ID,
		}}
	}
	config := foldersetup.Config{
		Plan: req.Plan, WorkspaceName: strings.TrimSpace(req.Offer.Subject.Name), EntryName: req.EntryName,
	}
	if req.Offer.Setup != nil {
		config.RunID, config.HomeID = req.Offer.Setup.RunID, req.Offer.Setup.HomeID
	}
	result, err := runner.Run(ctx, config)
	if result.Cause != nil {
		// The card says only what is needed; the cause belongs in the log.
		logger.Warn("One-card folder setup stopped", logger.Fields{
			"offer_id": req.Offer.ID, "reason": result.StopReason, "error": result.Cause.Error(),
		})
	}
	return err
}

// sharedStaffing adapts the project staffing service to the runner. grant is
// the consent the run records on a Home that has none (nil records nothing).
type sharedStaffing struct {
	service *projectstaffing.Service
	grant   *projectstaffing.Grant
}

func (s sharedStaffing) Fill(_ context.Context, projectWorkspaceID string, roleIDs []string) ([]projectstaffing.Fill, error) {
	return s.service.Fill(projectWorkspaceID, roleIDs, s.grant)
}

func (s sharedStaffing) Settle(_ context.Context, projectWorkspaceID string) error {
	return s.service.Settle(projectWorkspaceID)
}

// scopedJourney binds one user to a quest-scoped journey service.
type scopedJourney struct {
	service *setupjourney.Service
	userID  string
}

func (j scopedJourney) Read(ctx context.Context, runID string) (*setupjourney.JourneyProjection, error) {
	return j.service.Read(ctx, j.userID, runID)
}

func (j scopedJourney) Mutate(ctx context.Context, runID string, action setupjourney.ActionID, request setupjourney.ActionMutation) (*setupjourney.ActionResult, error) {
	return j.service.Mutate(ctx, j.userID, runID, action, request)
}

func (j scopedJourney) Child(ctx context.Context, rootRevision int64, key string) (*setupjourney.JourneyProjection, error) {
	return j.service.CreateOrResumeChild(ctx, j.userID, setupjourney.PresentationMutation{IfRevision: rootRevision, IdempotencyKey: key})
}

// homeProviderInstaller installs the reviewed Home provider through the same
// preview-then-install-that-release seam the portfolio card uses.
type homeProviderInstaller struct {
	setup folderHomeProviderSetup
	key   string
}

func (p homeProviderInstaller) Preview(ctx context.Context) (foldersetup.ProviderPreview, error) {
	preview, err := p.setup.Preview(ctx, p.key)
	if err != nil {
		return foldersetup.ProviderPreview{}, err
	}
	return foldersetup.ProviderPreview{Ready: preview.Ready, PluginID: preview.PluginID, Version: preview.Version}, nil
}

func (p homeProviderInstaller) Install(ctx context.Context, reviewedVersion string) error {
	_, err := p.setup.Install(ctx, p.key, reviewedVersion)
	return err
}

// folderSelections mints the project picker's selection token for the folder
// the server remembered for the offer. The browser never supplies it.
type folderSelections struct {
	builder *ServerBuilder
	path    string
}

func (s folderSelections) Select(context.Context) (string, string, error) {
	if strings.TrimSpace(s.path) == "" || s.builder == nil || s.builder.pathSelectionStore == nil {
		return "", "", foldersetup.ErrNeedsPick
	}
	token, err := s.builder.pathSelectionStore.Issue(s.path)
	if err != nil {
		return "", "", foldersetup.ErrNeedsPick
	}
	return token, s.path, nil
}

type progressFunc func(ctx context.Context, update personalassistant.FolderSetupUpdate) error

func (f progressFunc) Update(ctx context.Context, update personalassistant.FolderSetupUpdate) error {
	return f(ctx, update)
}
