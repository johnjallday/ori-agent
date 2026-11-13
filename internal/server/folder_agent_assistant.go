package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// folderAgentAssistantRun is the Home step's shared-assistant hire as the run
// finds it: the plan's line (empty when the card named no hire), and the song
// team and decision read from the Home as it is now.
type folderAgentAssistantRun struct {
	line   string
	homeID string
	team   projectstaffing.Team
	teamOK bool
	plan   projectstaffing.SharedPlan
}

// creates says the hire would make a new agent, which needs a model.
func (a folderAgentAssistantRun) creates() bool {
	return a.line != "" && a.teamOK && a.plan.State == projectstaffing.SharedHire
}

// folderAgentHomeAssistant reads the Home's song team (from the installed app
// plugin, as the Home opens songs with it) and what a hire would do there.
func (b *ServerBuilder) folderAgentHomeAssistant(homeID, line string) folderAgentAssistantRun {
	run := folderAgentAssistantRun{line: line, homeID: homeID}
	if line == "" || b.folderSetupHost == nil || b.projectStaffing == nil {
		return run
	}
	home, err := b.workspaceStore.Get(homeID)
	if err != nil || home == nil {
		return run
	}
	team, ok := b.folderSetupHost.projectTeam(home)
	if !ok {
		return run
	}
	run.team = projectstaffing.Team{PluginID: team.PluginID, BlueprintID: team.BlueprintID, Digest: team.Digest, Roles: team.Roles}
	if plan, err := b.projectStaffing.PlanShared(homeID, run.team); err == nil {
		run.teamOK, run.plan = true, plan
	}
	return run
}

// hireFolderAgentAssistant hires the Home's shared assistant on its own plan
// line, recording the Home's approval for it with this run as the offer
// (FR7). It returns the name of an agent it created, and the stop the run ends
// with when the hire could not be made (nil when it was, or when the Home
// already had it).
func (b *ServerBuilder) hireFolderAgentAssistant(ctx context.Context, rec folderagent.Record, a folderAgentAssistantRun,
	modelReady bool, setLine func(name, state, progress string)) (string, *homeStageStop) {
	fail := func(reason, words string, cause error) (string, *homeStageStop) {
		setLine(a.line, personalassistant.FolderLineFailed, words)
		return "", &homeStageStop{reason: reason, cause: cause}
	}
	if !a.teamOK {
		// FR12: the song team cannot be read (its plugin is missing or disabled).
		return fail(personalassistant.FolderStopAssistantUnavailable,
			"Its plugin is not installed, so it could not be hired", errors.New("the Home's song team cannot be read"))
	}
	if a.plan.Name != "" {
		setLine(a.line, personalassistant.FolderLineWorking, "Hiring "+a.plan.Name)
	}
	grant := projectstaffing.Grant{Source: workspace.ProjectStaffingConsentFolderOffer, OfferID: "folder-agent-" + rec.WorkspaceID}
	create := func(ctx context.Context, role workspace.AssistantProgramRoleSpec, name string, marker *agent.AssistantSetupProvenance) (bool, error) {
		if b.assistantStaffing == nil {
			return false, errors.New("staffing is unavailable")
		}
		return b.assistantStaffing.CreateSharedProjectAgent(ctx, role, name, "", "", marker)
	}
	var fills []projectstaffing.Fill
	err := b.projectStaffing.WithHome(a.homeID, func(tx *projectstaffing.Tx) error {
		var hireErr error
		fills, hireErr = tx.HireShared(ctx, a.team, grant, modelReady, create)
		return hireErr
	})
	label := a.plan.Role.Label
	switch {
	case err == nil && len(fills) > 0 && fills[0].Mode == projectstaffing.ModeCreate:
		setLine(a.line, personalassistant.FolderLineDone, "Hired "+fills[0].Name)
		return fills[0].Name, nil
	case err == nil:
		// Running Set up again: the Home already has it.
		setLine(a.line, personalassistant.FolderLineDone, "Already in your Home")
		return "", nil
	case errors.Is(err, projectstaffing.ErrNoConsent):
		// The Home's switch was turned off after the card was drawn: Set up
		// leaves it off.
		setLine(a.line, personalassistant.FolderLineDone, "Not added: the Home's \"Add my "+label+" to songs I open\" switch is off")
		return "", nil
	case errors.Is(err, projectstaffing.ErrNoModel):
		return fail(personalassistant.FolderStopNeedsModel, "", err)
	case errors.Is(err, projectstaffing.ErrConsentStale):
		return fail(personalassistant.FolderStopConsentStale, "The Home agreed to it for an older version of its team", err)
	case errors.Is(err, projectstaffing.ErrReservationHeld):
		return fail(personalassistant.FolderStopAssistantUnavailable,
			"An agent with its name already exists, and Ori did not make it for this Home", err)
	}
	return fail(personalassistant.FolderStopFailed, "", err)
}

// installFolderAgentHomeTools installs the app plugin the Home's songs get
// their team from, through the same reviewed install quest the tools step
// uses. It connects nothing to the folder's workspace: that is the tools
// step's, and the plan did not choose it.
func (b *ServerBuilder) installFolderAgentHomeTools(ctx context.Context, rec folderagent.Record, key, line string,
	setLine func(name, state, progress string)) error {
	entry, ok := reviewedintegration.Get(key)
	if !ok || b.folderSetupHost == nil {
		return errors.New("unknown integration")
	}
	words := integrationWords{plugin: entry.PluginName(), tools: entry.DisplayName, workspace: rec.WorkspaceName}
	setLine(line, personalassistant.FolderLineWorking, words.phase(foldersetup.IntegrationChecking))
	facts, err := b.folderSetupHost.integrationFacts(ctx, rec.OwnerUserID, entry)
	if err != nil {
		return err
	}
	words.version = facts.Version
	if facts.State == personalassistant.FolderInstallReady {
		setLine(line, personalassistant.FolderLineDone, "")
		return nil
	}
	if b.setupJourneyService == nil {
		return errors.New("no setup journey service")
	}
	install, err := b.setupJourneyService.ForHostQuest(ctx, rec.OwnerUserID, entry.InstallQuestID())
	if err != nil {
		return err
	}
	if err := foldersetup.InstallReviewedIntegration(ctx, scopedJourney{service: install, userID: rec.OwnerUserID}, facts.State,
		facts.PluginID, facts.Version, func(phase foldersetup.IntegrationPhase) {
			setLine(line, personalassistant.FolderLineWorking, words.phase(phase))
		}); err != nil {
		return err
	}
	setLine(line, personalassistant.FolderLineDone, words.installed())
	return nil
}

// The Home's shared assistant on the folder card (assistant-reads-projects).
// Its role comes from the app plugin's song blueprint, named by the reviewed
// integration: host code reads the label and the description the plugin
// declares and names no product.

// teamFromBlueprints is the song team the blueprint blueprintID of pluginID
// declares: its required project roles and the team digest every song opened
// with it is pinned to. A resource team is never a song team.
func teamFromBlueprints(pluginID, blueprintID string, blueprints []plugin.ResolvedBlueprint) (projectstaffing.Team, bool) {
	for i := range blueprints {
		blueprint := &blueprints[i]
		project := blueprint.Template.AssistantProject
		if blueprint.ID != blueprintID || project == nil || project.IsResource() {
			continue
		}
		team := projectstaffing.Team{PluginID: pluginID, BlueprintID: blueprint.ID,
			Digest: projecttemplates.AssistantProjectDigest(project)}
		for _, role := range project.ProgramRoles() {
			if role.Required {
				team.Roles = append(team.Roles, role)
			}
		}
		return team, len(team.Roles) > 0
	}
	return projectstaffing.Team{}, false
}

// folderAgentAppTeam is the song team of a reviewed integration: from the
// installed copy when there is one, else from the reviewed release a Set up
// would install (inspected, never installed). false when neither can be read.
func folderAgentAppTeam(ctx context.Context, b *ServerBuilder, entry reviewedintegration.Entry) (projectstaffing.Team, bool) {
	if b == nil || b.pluginHandler == nil || entry.PluginID == "" || entry.ExpectedBlueprintID == "" {
		return projectstaffing.Team{}, false
	}
	if installed, err := b.pluginHandler.Manager().List(); err == nil {
		for _, candidate := range installed {
			if candidate.Name == entry.PluginID {
				return teamFromBlueprints(entry.PluginID, entry.ExpectedBlueprintID, candidate.ResolvedBlueprints)
			}
		}
	}
	if b.server == nil || b.server.reviewedReleases == nil {
		return projectstaffing.Team{}, false
	}
	release, ok, err := b.server.reviewedReleases.integrationInstall(ctx, entry)
	if err != nil || !ok {
		return projectstaffing.Team{}, false
	}
	return teamFromBlueprints(entry.PluginID, entry.ExpectedBlueprintID, release.blueprints)
}

// sharedAssistantStates maps the staffing decision onto the plan's words.
var sharedAssistantStates = map[string]string{
	projectstaffing.SharedHire:  folderlook.AssistantHire,
	projectstaffing.SharedHas:   folderlook.AssistantHas,
	projectstaffing.SharedOff:   folderlook.AssistantOff,
	projectstaffing.SharedStale: folderlook.AssistantStale,
	projectstaffing.SharedHeld:  folderlook.AssistantHeld,
}

// folderAgentAssistantFacts is where the shared assistant of the integration's
// song team stands for the owner's Home of a provider (or the Home a Set up
// would create): the plan names it as a hire, or says why it hires nobody.
// false when the host cannot tell, and the plan then says nothing about it.
func folderAgentAssistantFacts(ctx context.Context, b *ServerBuilder, userID, providerKey, integrationKey string) (folderlook.HomeAssistant, bool) {
	if b == nil || b.folderSetupHost == nil || b.projectStaffing == nil {
		return folderlook.HomeAssistant{}, false
	}
	var provider reviewedintegration.HomeProvider
	for _, candidate := range reviewedintegration.HomeProviders() {
		if candidate.Key == providerKey {
			provider = candidate
		}
	}
	entry, ok := reviewedintegration.Get(integrationKey)
	if provider.Key == "" || !ok {
		return folderlook.HomeAssistant{}, false
	}
	team, ok := folderAgentAppTeam(ctx, b, entry)
	if !ok {
		return folderlook.HomeAssistant{}, false
	}
	station, err := b.folderSetupHost.station(userID, provider)
	if err != nil {
		return folderlook.HomeAssistant{}, false
	}
	homeID := ""
	if station != nil {
		homeID = station.ID
	}
	shared, err := b.projectStaffing.PlanShared(homeID, team)
	if err != nil {
		return folderlook.HomeAssistant{}, false
	}
	state, known := sharedAssistantStates[shared.State]
	if !known {
		return folderlook.HomeAssistant{}, false
	}
	return folderlook.HomeAssistant{Label: shared.Role.Label, Name: shared.Name, Job: shared.Role.Description,
		Taken: shared.Taken, State: state}, true
}
