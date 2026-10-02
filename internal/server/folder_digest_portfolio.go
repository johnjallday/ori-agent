package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// portfolioHomeName is what the card calls a collection's Home before the
// provider is installed to name it (the step-by-step card says the same).
const portfolioHomeName = "Music Production Home"

// portfolioTarget is what a collection offer resolves to.
type portfolioTarget struct {
	row      folderdigest.CapabilityRow
	provider reviewedintegration.HomeProvider
	// integration is the reviewed integration some projects need; nil when none.
	integration *reviewedintegration.Entry
}

func (h *folderSetupHost) portfolioTarget(offer personalassistant.FolderOffer) (portfolioTarget, error) {
	b := h.builder
	if b == nil || b.setupJourneyService == nil || b.sessionHandler == nil || b.assistantStaffing == nil ||
		b.projectStaffing == nil || offer.Portfolio == nil {
		return portfolioTarget{}, errSetupUnavailable
	}
	row, ok := folderdigest.CapabilityForShape(folderdigest.Shape(offer.Portfolio.Shape))
	if !ok || row.Offer == nil || row.Offer.HomeProviderKey != offer.Portfolio.ProviderKey {
		return portfolioTarget{}, errSetupUnavailable
	}
	target := portfolioTarget{row: row}
	found := false
	for _, candidate := range reviewedintegration.HomeProviders() {
		if candidate.Key == offer.Portfolio.ProviderKey {
			target.provider, found = candidate, true
		}
	}
	if !found {
		return portfolioTarget{}, errSetupUnavailable
	}
	if offer.Portfolio.IntegrationProjects > 0 {
		entry, ok := reviewedintegration.Get(offer.Portfolio.IntegrationKey)
		if !ok {
			return portfolioTarget{}, errSetupUnavailable
		}
		target.integration = &entry
	}
	return target, nil
}

// station is the owner's Home for the provider, or nil when there is none.
func (h *folderSetupHost) station(userID string, provider reviewedintegration.HomeProvider) (*workspace.Workspace, error) {
	b := h.builder
	if b.workspaceFileStore == nil {
		return nil, errSetupUnavailable
	}
	station, err := workspace.NewAssistantProgramStore(b.workspaceFileStore).FindStation(workspace.AssistantProgramKey{
		OwnerUserID: userID, PluginID: provider.PluginID, ProgramID: provider.ProgramID,
	})
	if errors.Is(err, workspace.ErrAssistantStationNotFound) {
		return nil, nil
	}
	if err != nil || station == nil || station.OwnerUserID != userID {
		return nil, errSetupUnavailable
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.HomeProvider == nil || state.HomeProvider.PluginID != provider.PluginID {
		return nil, errSetupUnavailable
	}
	// Read the canonical record (the file store's copy may lag the primary).
	if live, err := b.workspaceStore.Get(station.ID); err == nil && live != nil {
		return live, nil
	}
	return station, nil
}

// homeStaffed says every required Home role is filled.
func homeStaffed(home *workspace.Workspace) bool {
	state := home.GetAssistantProgramState()
	if state == nil || state.Declaration == nil {
		return false
	}
	filled := map[string]bool{}
	for _, binding := range state.HomeBindings.Bindings {
		filled[binding.RoleID] = true
	}
	for _, role := range state.Declaration.Roles {
		if role.Scope == workspace.AssistantRoleScopeHome && role.Required && !filled[role.ID] {
			return false
		}
	}
	return true
}

// projectTeam is the project blueprint the Home's library opens projects with.
func (h *folderSetupHost) projectTeam(home *workspace.Workspace) (projectlibrary.ProjectTeam, bool) {
	b := h.builder
	if home == nil || b.pluginHandler == nil {
		return projectlibrary.ProjectTeam{}, false
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.HomeProvider == nil {
		return projectlibrary.ProjectTeam{}, false
	}
	installed, err := b.pluginHandler.Manager().List()
	if err != nil {
		return projectlibrary.ProjectTeam{}, false
	}
	key := state.Key.Normalize()
	return projectlibrary.CompatibleProjectTeam(installed, state.HomeProvider, projectlibrary.Scope{
		OwnerUserID: key.OwnerUserID, HomeID: home.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID,
	})
}

// sharing is where the Home's consent for the shared assistant stands for the
// blueprint the Home opens projects with: "" (none, or for an older team),
// "on", or "off".
func (h *folderSetupHost) sharing(home *workspace.Workspace, blueprintID string) string {
	consent := home.GetAssistantProgramState().GetProjectStaffingConsent()
	if consent == nil || consent.Validate() != nil || consent.BlueprintID != blueprintID {
		return ""
	}
	if consent.RevokedAt != nil {
		return foldersetup.SharingOff
	}
	if team, ok := h.projectTeam(home); ok && team.Digest != consent.TeamDigest {
		return "" // a plugin update changed the team: Set up asks again
	}
	return foldersetup.SharingOn
}

// portfolioPlan lists every consequence of Set up on a collection.
func (h *folderSetupHost) portfolioPlan(ctx context.Context, req personalassistant.FolderSetupRequest) (personalassistant.FolderSetupPlan, error) {
	target, err := h.portfolioTarget(req.Offer)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, err
	}
	provider, err := h.providerFacts(ctx, target.provider)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, err
	}
	facts := foldersetup.PortfolioFacts{
		FolderName: strings.TrimSpace(req.Offer.Subject.Name), Projects: req.Offer.Portfolio.Projects,
		CollectionNoun: target.row.Offer.DisplayName, HomeName: portfolioHomeName, Provider: provider,
		HomeTemplate: "plugin-home:" + target.provider.PluginID + ":" + target.provider.ProgramID,
		AppName:      target.row.Offer.IntegrationName, ProjectLabel: target.row.Blueprint.Label,
		IntegrationProjects: req.Offer.Portfolio.IntegrationProjects,
	}
	if target.integration != nil {
		integration, err := h.integrationFacts(ctx, req.UserID, *target.integration)
		if err != nil {
			return personalassistant.FolderSetupPlan{}, err
		}
		facts.Integration = &integration
	}
	home, err := h.station(req.UserID, target.provider)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, err
	}
	if home != nil {
		facts.HomeExists, facts.HomeName, facts.HomeStaffed = true, home.Name, homeStaffed(home)
		facts.Sharing = h.sharing(home, target.row.Blueprint.BlueprintID)
		if home.GetAssistantProgramState().GetSongDetailsConsent().Active() {
			facts.SongDetails = foldersetup.SongDetailsOn
		}
		if team, ok := h.projectTeam(home); ok && len(team.Roles) == 1 {
			facts.AssistantName = team.Roles[0].Label
		}
	}
	if facts.FolderName == "" {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	return foldersetup.BuildPortfolioPlan(facts), nil
}

// portfolioRun drives a collection's setup on the server.
func (h *folderSetupHost) portfolioRun(ctx context.Context, req personalassistant.FolderSetupRequest) error {
	target, err := h.portfolioTarget(req.Offer)
	if err != nil {
		return err
	}
	b := h.builder
	runner := &foldersetup.PortfolioRunner{
		Homes:    &portfolioHomes{host: h, userID: req.UserID, provider: target.provider},
		Staffing: portfolioHomeStaffing{builder: b},
		Library:  portfolioLibrary{builder: b, userID: req.UserID, offerID: req.Offer.ID},
		Receipts: portfolioReceipts{host: h, folder: strings.TrimSpace(req.Offer.Subject.Name), noun: target.row.Offer.DisplayName, projectLabel: target.row.Blueprint.Label},
		Folder:   req.Path,
		Progress: progressFunc(req.Update),
	}
	if req.Plan.Intent.Provider != "" {
		runner.Providers = homeProviderInstaller{setup: folderHomeProviderSetup{builder: b}, key: target.provider.Key}
	}
	if target.integration != nil && req.Plan.Intent.Integration != "" {
		install, err := b.setupJourneyService.ForHostQuest(ctx, req.UserID, target.integration.InstallQuestID())
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
	config := foldersetup.PortfolioConfig{Plan: req.Plan}
	if req.Offer.Setup != nil {
		config.HomeID = req.Offer.Setup.HomeID
	}
	if req.Offer.DecidedAt != nil {
		config.AcceptedAfter = *req.Offer.DecidedAt
	}
	result, err := runner.Run(ctx, config)
	if result.Cause != nil {
		logger.Warn("One-card collection setup stopped", logger.Fields{
			"offer_id": req.Offer.ID, "reason": result.StopReason, "error": result.Cause.Error(),
		})
	}
	return err
}

// portfolioHomes is the Home step over the reviewed Home template. It keeps the
// full review between the review and its commit.
type portfolioHomes struct {
	host     *folderSetupHost
	userID   string
	provider reviewedintegration.HomeProvider
	reviewed map[string]sessionhttp.PortfolioHomeReview
}

func (p *portfolioHomes) Find(context.Context) (foldersetup.HomeFound, bool, error) {
	home, err := p.host.station(p.userID, p.provider)
	if err != nil {
		return foldersetup.HomeFound{}, false, err
	}
	if home == nil {
		return foldersetup.HomeFound{}, false, nil
	}
	found := foldersetup.HomeFound{ID: home.ID}
	if provenance := home.GetAssistantProgramState().GroupTemplate; provenance != nil {
		found.CreatedAt = provenance.CreatedAt
	}
	return found, true, nil
}

func (p *portfolioHomes) Review(ctx context.Context) (foldersetup.HomeReview, error) {
	review, err := p.host.builder.sessionHandler.ReviewPortfolioHome(ctx, p.provider.PluginID)
	if err != nil {
		return foldersetup.HomeReview{}, err
	}
	if p.reviewed == nil {
		p.reviewed = map[string]sessionhttp.PortfolioHomeReview{}
	}
	p.reviewed[review.Token] = review
	return foldersetup.HomeReview{Token: review.Token, TemplateID: review.TemplateID, Name: review.Name, Reuse: review.Reuse}, nil
}

func (p *portfolioHomes) Commit(ctx context.Context, review foldersetup.HomeReview, key string) (string, error) {
	reviewed, ok := p.reviewed[review.Token]
	if !ok {
		return "", errSetupUnavailable
	}
	delete(p.reviewed, review.Token)
	return p.host.builder.sessionHandler.CommitPortfolioHome(ctx, reviewed, key)
}

// portfolioHomeStaffing fills the Home's required roles through the one
// staffing seam (StaffRoleOnWorkspace), creating new agents only.
type portfolioHomeStaffing struct{ builder *ServerBuilder }

func (s portfolioHomeStaffing) Missing(_ context.Context, homeID string) ([]foldersetup.HomeRole, error) {
	home, err := s.builder.workspaceStore.Get(homeID)
	if err != nil || home == nil {
		return nil, errSetupUnavailable
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.Declaration == nil {
		return nil, errSetupUnavailable
	}
	filled := map[string]bool{}
	for _, binding := range state.HomeBindings.Bindings {
		filled[binding.RoleID] = true
	}
	taken := func(name string) bool {
		if s.builder.st != nil {
			for _, existing := range s.builder.st.ListAgents() {
				if strings.EqualFold(strings.TrimSpace(existing), name) {
					return true
				}
			}
		}
		for _, instance := range home.GetAgentInstances() {
			if strings.EqualFold(strings.TrimSpace(instance.Name), name) {
				return true
			}
		}
		return false
	}
	var roles []foldersetup.HomeRole
	for _, role := range state.Declaration.Roles {
		if role.Scope != workspace.AssistantRoleScopeHome || !role.Required || filled[role.ID] {
			continue
		}
		name := role.Label
		if primary := strings.TrimSpace(state.Declaration.DefaultPrimaryName); role.Primary && primary != "" {
			name = primary
		}
		// Never adopt an agent that happens to share the name (D3).
		roles = append(roles, foldersetup.HomeRole{RoleID: role.ID, Name: workspace.FirstFreeAgentName(name, taken)})
	}
	return roles, nil
}

func (s portfolioHomeStaffing) ModelReady(context.Context) bool {
	return s.builder.systemModelAvailable()
}

func (s portfolioHomeStaffing) Staff(ctx context.Context, homeID string, role foldersetup.HomeRole) error {
	return s.builder.assistantStaffing.StaffRoleOnWorkspace(ctx, homeID, []setupjourney.RoleFill{{
		RoleID: role.RoleID, Mode: setupjourney.StaffingModeCreate, Name: role.Name,
	}})
}

// portfolioLibrary is the Home library's own reviews, driven in process.
type portfolioLibrary struct {
	builder *ServerBuilder
	userID  string
	offerID string
}

func (l portfolioLibrary) handler() error {
	if l.builder == nil || l.builder.sessionHandler == nil {
		return errSetupUnavailable
	}
	return nil
}

func (l portfolioLibrary) State(ctx context.Context, homeID string) (foldersetup.LibraryState, error) {
	if err := l.handler(); err != nil {
		return foldersetup.LibraryState{}, err
	}
	folder := ""
	if digest := l.builder.personalAssistantFolderDigest; digest != nil {
		// The connected root is matched against the folder the offer holds.
		if path, _, err := digest.PortfolioSetupRoot(ctx, l.userID, l.offerID, homeID); err == nil {
			folder = path
		}
	}
	state, err := l.builder.sessionHandler.PortfolioLibraryState(l.userID, homeID, folder)
	if err != nil {
		return foldersetup.LibraryState{}, err
	}
	if folder == "" {
		state.RootID, state.Scanned = "", false
	}
	return foldersetup.LibraryState{Initialized: state.Initialized, Linked: state.Linked, RootID: state.RootID,
		Scanned: state.Scanned, Listed: state.Listed, Partial: state.Partial}, nil
}

func (l portfolioLibrary) ReviewInitialize(_ context.Context, homeID string) (foldersetup.InitReview, error) {
	token, linked, err := l.builder.sessionHandler.ReviewPortfolioLibrary(l.userID, homeID)
	return foldersetup.InitReview{Token: token, Linked: linked}, err
}

func (l portfolioLibrary) CommitInitialize(_ context.Context, homeID string, review foldersetup.InitReview, key string) error {
	return l.builder.sessionHandler.CommitPortfolioLibrary(l.userID, homeID, review.Token, key)
}

func (l portfolioLibrary) ReviewConnect(ctx context.Context, homeID string) (foldersetup.ConnectReview, error) {
	digest := l.builder.personalAssistantFolderDigest
	if digest == nil {
		return foldersetup.ConnectReview{}, errSetupUnavailable
	}
	token, folder, includesScan, err := l.builder.sessionHandler.ReviewPortfolioRoot(ctx, l.userID, homeID, l.offerID, setupRootResolver{digest: digest})
	if errors.Is(err, projectlibrary.ErrUnavailable) {
		// The folder the offer held is gone or changed: pick it again.
		return foldersetup.ConnectReview{}, foldersetup.ErrNeedsPick
	}
	return foldersetup.ConnectReview{Token: token, Folder: folder, IncludesScan: includesScan}, err
}

func (l portfolioLibrary) CommitConnect(_ context.Context, homeID string, review foldersetup.ConnectReview, key string) (string, error) {
	return l.builder.sessionHandler.CommitPortfolioRoot(l.userID, homeID, review.Token, key)
}

func (l portfolioLibrary) ReviewScan(_ context.Context, homeID, rootID string) (foldersetup.LibraryScanReview, error) {
	review, err := l.builder.sessionHandler.ReviewPortfolioScan(l.userID, homeID, rootID)
	return foldersetup.LibraryScanReview{Token: review.Token, RootID: review.RootID, MetadataOnly: review.MetadataOnly,
		ReadsSongDetails: review.ReadsSongDetails}, err
}

func (l portfolioLibrary) CommitScan(ctx context.Context, homeID, rootID string, review foldersetup.LibraryScanReview, key string) (foldersetup.ScanOutcome, error) {
	listed, partial, err := l.builder.sessionHandler.CommitPortfolioScan(ctx, l.userID, homeID, rootID, review.Token, key)
	return foldersetup.ScanOutcome{Listed: listed, Partial: partial}, err
}

// setupRootResolver proves the folder through the run's own awaiting offer.
type setupRootResolver struct {
	digest *personalassistant.FolderDigestService
}

func (r setupRootResolver) PortfolioRoot(ctx context.Context, ownerUserID, offerID, homeID string) (string, string, error) {
	return r.digest.PortfolioSetupRoot(ctx, ownerUserID, offerID, homeID)
}

// portfolioSharing records the Home's standing consent for the shared
// assistant, scoped to the installed project blueprint and its team digest.
type portfolioSharing struct {
	host        *folderSetupHost
	offerID     string
	blueprintID string
}

func (s portfolioSharing) State(_ context.Context, homeID string) (string, error) {
	home, err := s.host.builder.workspaceStore.Get(homeID)
	if err != nil || home == nil {
		return "", errSetupUnavailable
	}
	return s.host.sharing(home, s.blueprintID), nil
}

func (s portfolioSharing) Grant(_ context.Context, homeID string) error {
	home, err := s.host.builder.workspaceStore.Get(homeID)
	if err != nil || home == nil {
		return errSetupUnavailable
	}
	team, ok := s.host.projectTeam(home)
	if !ok || team.BlueprintID != s.blueprintID {
		return fmt.Errorf("%w: no installed project blueprint for this Home", errSetupUnavailable)
	}
	grant := workspace.ProjectStaffingConsentGrant{
		Source: workspace.ProjectStaffingConsentFolderOffer, OfferID: s.offerID,
		PluginID: team.PluginID, BlueprintID: team.BlueprintID, TeamDigest: team.Digest,
	}
	for _, role := range team.Roles {
		grant.RoleIDs = append(grant.RoleIDs, role.ID)
	}
	_, err = s.host.builder.projectStaffing.Consents().Grant(homeID, grant)
	return err
}

// portfolioSongDetails records the song-details consent on the Home the run
// created, citing the card's offer.
type portfolioSongDetails struct {
	store   workspace.Store
	offerID string
}

func (s portfolioSongDetails) Grant(_ context.Context, homeID string) error {
	if s.store == nil {
		return errSetupUnavailable
	}
	return workspace.NewSongDetailsConsents(s.store).Grant(homeID, s.offerID)
}

// portfolioReceipts reads back what the run made, from canonical state.
type portfolioReceipts struct {
	host         *folderSetupHost
	folder       string
	noun         string
	projectLabel string
}

func (r portfolioReceipts) Receipt(_ context.Context, homeID string, facts foldersetup.PortfolioReceiptFacts) ([]personalassistant.FolderReceiptRow, error) {
	b := r.host.builder
	home, err := b.workspaceStore.Get(homeID)
	if err != nil || home == nil || !workspace.IsCanonicalWorkspaceSlug(home.FolderSlug) {
		return nil, errSetupUnavailable
	}
	detail := "your Home"
	if facts.HomeCreated {
		detail = "created"
	}
	rows := []personalassistant.FolderReceiptRow{{
		Kind: "home", Name: home.Name, Detail: detail,
		Route: "/workspaces/" + url.PathEscape(home.FolderSlug) + "/assistant#projectLibraryPanel",
	}}
	added := map[string]bool{}
	for _, name := range facts.AddedAgents {
		added[strings.ToLower(name)] = true
	}
	for _, instance := range home.GetAgentInstances() {
		if facts.HomeCreated || added[strings.ToLower(instance.Name)] {
			rows = append(rows, personalassistant.FolderReceiptRow{Kind: "agent", Name: instance.Name, Detail: receiptAgentDetail(instance)})
		}
	}
	noun := strings.TrimSpace(r.noun)
	if noun == "" {
		noun = "projects"
	}
	listed := personalassistant.FolderReceiptRow{
		Kind: "library", Name: fmt.Sprintf("Listed %d %s in %s", facts.Listed.Listed, noun, r.folder),
		Detail: "names and project files only",
	}
	if home.GetAssistantProgramState().GetSongDetailsConsent().Active() {
		one := strings.TrimSpace(r.projectLabel)
		if one == "" {
			one = "project"
		}
		listed.Detail = "names, project files, and each " + one + "'s tempo, length and track count"
	}
	if facts.Listed.Partial {
		// Never claim the whole folder after a partial listing.
		listed.Name = fmt.Sprintf("Listed %d %s in %s so far", facts.Listed.Listed, noun, r.folder)
		listed.Detail = "the folder holds more than one listing reads; open the library to list the rest"
	}
	rows = append(rows, listed)
	if consent := home.GetAssistantProgramState().GetProjectStaffingConsent(); facts.Shared && consent.Active() {
		if team, ok := r.host.projectTeam(home); ok && team.BlueprintID == consent.BlueprintID {
			for _, role := range team.Roles {
				entry, covered := consent.Role(role.ID)
				if !covered {
					continue
				}
				one := strings.TrimSpace(r.projectLabel)
				row := personalassistant.FolderReceiptRow{Kind: "assistant", Name: role.Label,
					Detail: "added to the first " + one + " you open, then joins each one"}
				if entry.AgentName != "" {
					row.Name, row.Detail = entry.AgentName, "joins each "+one+" you open"
				}
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}

// receiptAgentDetail names an agent's role when its name does not, and says it
// was added.
func receiptAgentDetail(instance workspace.AgentInstance) string {
	role := strings.TrimSpace(instance.Role)
	if role == "" || strings.EqualFold(role, strings.TrimSpace(instance.Name)) {
		return "added"
	}
	return role + ", added"
}
