package server

import (
	"context"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A single recognized song whose Home lists its projects is a portfolio of one:
// the assistant sets up (or joins) the Home and lists the song there, and the
// Home's Portfolio Manager then opens it through the library's own reviewed
// open, under the same one Set up. Nothing here is a model turn or an agent
// tool; the manager is the named actor, the host does the work.

// portfolioManagerName names the Home's manager before its Home declares one.
const portfolioManagerName = "Portfolio Manager"

// handoffOffer says the offer is one recognized project whose capability names
// a reviewed Home provider, which is what lets a Home list projects (the test a
// collection's offer uses). Only that Home's Portfolio Manager opens it; every
// other single project keeps its own plan and run.
func handoffOffer(offer personalassistant.FolderOffer, target setupTarget) bool {
	return offer.Portfolio == nil && offer.Subject.Kind == personalassistant.FolderChoiceProject &&
		offer.Verdict != string(folderdigest.KindDump) && target.provider != nil && target.row.Offer != nil &&
		target.row.Offer.HomeProviderKey != "" && target.row.Offer.HomeProviderKey == target.provider.Key
}

// managerName is the Home's primary Home role, or the provider's usual name for
// it before the Home exists.
func managerName(home *workspace.Workspace) string {
	if home != nil {
		if state := home.GetAssistantProgramState(); state != nil && state.Declaration != nil {
			for _, role := range state.Declaration.Roles {
				if role.Scope == workspace.AssistantRoleScopeHome && role.Primary && strings.TrimSpace(role.Label) != "" {
					return strings.TrimSpace(role.Label)
				}
			}
		}
	}
	return portfolioManagerName
}

// handoffPlan lists every consequence of Set up on one song its Home's
// Portfolio Manager opens: the collection plan's Home facts for a portfolio of
// one and the single project plan's facts for the song, with its hire.
func (h *folderSetupHost) handoffPlan(ctx context.Context, req personalassistant.FolderSetupRequest, target setupTarget) (personalassistant.FolderSetupPlan, error) {
	song, err := h.facts(ctx, req, target)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, err
	}
	if song.Provider == nil || target.provider == nil {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	provider := *target.provider
	home, err := h.station(req.UserID, provider)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, err
	}
	if (home != nil) != song.HomeExists {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable // the Home changed while it was read
	}
	homeFacts := foldersetup.PortfolioFacts{
		FolderName: song.FolderName, Projects: 1, CollectionNoun: target.row.Offer.DisplayName,
		HomeName: song.HomeName, HomeExists: song.HomeExists, Destination: song.Destination,
		HomeTemplate: "plugin-home:" + provider.PluginID + ":" + provider.ProgramID,
		Provider:     *song.Provider, Integration: &song.Integration, IntegrationProjects: 1,
		AppName: target.row.Offer.IntegrationName, ProjectLabel: target.row.Blueprint.Label, Profile: song.Profile,
	}
	if home != nil {
		homeFacts.HomeStaffed = homeStaffed(home)
		homeFacts.Sharing = h.sharing(home, target.row.Blueprint.BlueprintID)
		if home.GetAssistantProgramState().GetSongDetailsConsent().Active() {
			homeFacts.SongDetails = foldersetup.SongDetailsOn
		}
	}
	if song.FolderName == "" {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	manager := managerName(home)
	if home == nil && h.builder.sessionHandler != nil {
		// Before the Home exists its reviewed template names the manager, so the
		// card says the name the Home will give it (AC9).
		if declared := h.builder.sessionHandler.PortfolioHomeManager(ctx, provider.PluginID); declared != "" {
			manager = declared
		}
	}
	plan := foldersetup.BuildHandoffPlan(foldersetup.HandoffFacts{
		Home: homeFacts, Song: song, SongName: song.FolderName, ManagerName: manager,
	})
	if personalassistant.HomeSuggestionReady(plan) {
		// The Home is ready: its manager suggests the song there, and the
		// assistant's card only leads to it.
		if route, ok := homeSuggestionsRoute(home); ok {
			plan.SuggestedIn = &personalassistant.FolderHomeLink{Name: home.Name, Route: route}
		}
	}
	return h.bindHandoffModel(plan)
}

// homeSuggestionsRoute is a Home's own page, where its manager's suggestions are.
func homeSuggestionsRoute(home *workspace.Workspace) (string, bool) {
	if home == nil || !workspace.IsCanonicalWorkspaceSlug(home.FolderSlug) {
		return "", false
	}
	return "/workspaces/" + url.PathEscape(home.FolderSlug) + "/assistant", true
}

// bindHandoffModel says which model the Home's new agents use, on the Home's
// agents line only, and binds the Default model the run may create agents
// with (the Home's, and the song's shared assistant) into the intent. The
// card's picture keeps the hire's own model.
func (h *folderSetupHost) bindHandoffModel(plan personalassistant.FolderSetupPlan) (personalassistant.FolderSetupPlan, error) {
	model := h.portfolioModel()
	if plan.Intent.StaffsHome {
		for i := range plan.Lines {
			if plan.Lines[i].Kind == personalassistant.FolderPlanHandoff {
				break
			}
			if plan.Lines[i].Kind == personalassistant.FolderPlanAgents {
				plan.Lines[i].Detail += homeAgentsModelSentence(model)
			}
		}
	}
	if model.State == "configured" {
		plan.Intent.Model = &personalassistant.FolderSetupModelIntent{Source: "system", Provider: model.Provider, Model: model.Name}
		if plan.Intent.Model.Validate() != nil {
			return personalassistant.FolderSetupPlan{}, errSetupUnavailable
		}
	}
	return plan.Stamped(), nil
}

// handoffRun drives a single song's setup: the collection run for a portfolio
// of one, then the Portfolio Manager's open (songRun).
func (h *folderSetupHost) handoffRun(ctx context.Context, req personalassistant.FolderSetupRequest, target setupTarget) error {
	return h.songRun(ctx, req, target)
}
