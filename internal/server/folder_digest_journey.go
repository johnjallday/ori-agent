package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type folderHomeVerifier struct{ builder *ServerBuilder }

func (v folderHomeVerifier) VerifiedHome(ctx context.Context, userID, homeID, providerKey string, acceptedAfter time.Time) (personalassistant.FolderCreateResult, error) {
	refused := personalassistant.ErrFolderWorkspaceRefused
	b := v.builder
	if b == nil || b.workspaceFileStore == nil || b.sessionStore == nil || homeID == "" {
		return personalassistant.FolderCreateResult{}, refused
	}
	var owner *reviewedintegration.HomeProvider
	for _, provider := range reviewedintegration.HomeProviders() {
		if provider.Key == providerKey {
			copy := provider
			owner = &copy
			break
		}
	}
	if owner == nil {
		return personalassistant.FolderCreateResult{}, refused
	}
	provider, err := (folderHomeProviderSetup{builder: b}).Preview(ctx, providerKey)
	if err != nil || !provider.Ready {
		return personalassistant.FolderCreateResult{}, refused
	}
	programs := workspace.NewAssistantProgramStore(b.workspaceFileStore)
	station, err := programs.FindStation(workspace.AssistantProgramKey{OwnerUserID: userID, PluginID: owner.PluginID, ProgramID: owner.ProgramID})
	if err != nil || station == nil || station.ID != homeID || station.OwnerUserID != userID {
		return personalassistant.FolderCreateResult{}, refused
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.HomeProvider == nil || state.HomeProvider.PluginID != owner.PluginID || state.HomeProvider.ProgramID != owner.ProgramID ||
		state.GroupTemplate == nil || state.GroupTemplate.TemplateID != "plugin-home:"+owner.PluginID+":"+owner.ProgramID ||
		!strings.HasPrefix(state.GroupTemplate.GroupTemplateID, "group-template:") ||
		state.GroupTemplate.ProgramHomeOwner == nil || state.GroupTemplate.ProgramHomeOwner.PluginID != owner.PluginID ||
		state.GroupTemplate.ProgramHomeOwner.ProgramID != owner.ProgramID ||
		state.GroupTemplate.ProgramHomeOwner.PluginVersion != provider.Version ||
		state.HomeProvider.PluginVersion != provider.Version ||
		state.GroupTemplate.CreatedAt.Before(acceptedAfter) || state.GroupTemplate.CreatedAt.IsZero() {
		return personalassistant.FolderCreateResult{}, refused
	}
	row, err := b.sessionStore.GetWorkspace(ctx, homeID)
	if err != nil || row == nil || row.OwnerUserID != userID || !row.IsGroup() || strings.TrimSpace(row.FolderSlug) == "" {
		return personalassistant.FolderCreateResult{}, refused
	}
	return personalassistant.FolderCreateResult{WorkspaceID: homeID, Route: "/workspaces/" + row.FolderSlug}, nil
}

// reviewedHomeExists reads the owner-scoped station key and provenance, not
// a same-named workspace or a browser claim. Unreadable/ambiguous state fails
// closed at the offer site.
func reviewedHomeExists(b *ServerBuilder, userID, providerKey string) (bool, error) {
	if b == nil || b.workspaceFileStore == nil {
		return false, personalassistant.ErrFolderOutcomeUnavailable
	}
	var owner *reviewedintegration.HomeProvider
	for _, entry := range reviewedintegration.HomeProviders() {
		if entry.Key == providerKey {
			copy := entry
			owner = &copy
			break
		}
	}
	if owner == nil {
		return false, personalassistant.ErrFolderOutcomeUnavailable
	}
	programs := workspace.NewAssistantProgramStore(b.workspaceFileStore)
	station, err := programs.FindStation(workspace.AssistantProgramKey{
		OwnerUserID: userID, PluginID: owner.PluginID, ProgramID: owner.ProgramID,
	})
	if errors.Is(err, workspace.ErrAssistantStationNotFound) {
		return false, nil
	}
	if err != nil || station == nil || station.OwnerUserID != userID {
		return false, personalassistant.ErrFolderOutcomeUnavailable
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.HomeProvider == nil || state.HomeProvider.PluginID != owner.PluginID {
		return false, personalassistant.ErrFolderOutcomeUnavailable
	}
	return true, nil
}

// reviewedProjectQuest selects one installed plugin quest that matches the
// host-reviewed integration and blueprint. Ambiguous declarations fail closed.
func reviewedProjectQuest(ctx context.Context, b *ServerBuilder, integrationKey, blueprintID string) (pluginID, questID string, ok bool) {
	if b == nil || b.setupJourneyService == nil {
		return "", "", false
	}
	entry, found := reviewedintegration.Get(integrationKey)
	if !found || entry.ExpectedBlueprintID != blueprintID {
		return "", "", false
	}
	quests, err := b.setupJourneyService.ListQuests(ctx)
	if err != nil {
		return "", "", false
	}
	for _, quest := range quests {
		if quest.Source != setupjourney.QuestSourcePlugin || quest.PluginID != entry.PluginID ||
			quest.TemplateID != "plugin:"+entry.PluginID+":"+blueprintID {
			continue
		}
		if questID != "" {
			return "", "", false
		}
		questID = quest.ID
	}
	return entry.PluginID, questID, questID != ""
}

// folderJourneyVerifier never accepts a claimed workspace from the browser.
// It reads the reviewed plugin's canonical quest run and the resulting project
// workspace, and requires its selected external directory to be the folder
// the user originally showed the assistant.
type folderJourneyVerifier struct{ builder *ServerBuilder }

func (v folderJourneyVerifier) VerifiedProject(ctx context.Context, userID, runID, folderPath, blueprintID, integrationKey string, acceptedAfter time.Time) (personalassistant.FolderCreateResult, error) {
	refused := personalassistant.ErrFolderWorkspaceRefused
	b := v.builder
	if b == nil || b.setupJourneyService == nil || b.workspaceFileStore == nil || b.sessionStore == nil || runID == "" {
		return personalassistant.FolderCreateResult{}, refused
	}
	entry, ok := reviewedintegration.Get(integrationKey)
	if !ok || entry.ExpectedBlueprintID != blueprintID {
		return personalassistant.FolderCreateResult{}, refused
	}
	_, questID, found := reviewedProjectQuest(ctx, b, integrationKey, blueprintID)
	if !found {
		return personalassistant.FolderCreateResult{}, refused
	}
	scoped, err := b.setupJourneyService.ForQuest(ctx, userID, entry.PluginID, questID)
	if err != nil {
		return personalassistant.FolderCreateResult{}, refused
	}
	journey, err := scoped.Read(ctx, userID, runID)
	if err != nil || !freshJourneyProject(journey, runID, acceptedAfter) {
		return personalassistant.FolderCreateResult{}, refused
	}
	id := journey.Receipts.ProjectWorkspaceID
	project, err := b.workspaceFileStore.Get(id)
	if err != nil || project == nil || project.OwnerUserID != userID {
		return personalassistant.FolderCreateResult{}, refused
	}
	if !journeyProjectMatchesFolder(project, userID, entry.PluginID, blueprintID, folderPath) {
		return personalassistant.FolderCreateResult{}, refused
	}
	row, err := b.sessionStore.GetWorkspace(ctx, id)
	if err != nil || row == nil || row.OwnerUserID != userID || row.IsGroup() || strings.TrimSpace(row.FolderSlug) == "" {
		return personalassistant.FolderCreateResult{}, refused
	}
	return personalassistant.FolderCreateResult{WorkspaceID: id, Route: "/workspaces/" + row.FolderSlug}, nil
}

func freshJourneyProject(journey *setupjourney.JourneyProjection, runID string, acceptedAfter time.Time) bool {
	return journey != nil && journey.RunID == runID &&
		journey.Journey.Source == setupjourney.QuestSourcePlugin &&
		journey.Lifecycle == setupjourney.LifecycleReady &&
		journey.FirstCompletedAt != nil && !journey.FirstCompletedAt.Before(acceptedAfter) &&
		journey.Receipts.ProjectWorkspaceID != ""
}

func journeyProjectMatchesFolder(project *workspace.Workspace, owner, pluginID, blueprintID, folderPath string) bool {
	if project == nil || project.OwnerUserID != owner {
		return false
	}
	provenance := project.GetTemplateProvenance()
	if provenance == nil || provenance.TemplateID != "plugin:"+pluginID+":"+blueprintID || provenance.PluginOwner == nil ||
		provenance.PluginOwner.PluginID != pluginID || provenance.PluginOwner.BlueprintID != blueprintID {
		return false
	}
	locator, err := workspace.GetProjectEntryLocator(project.SharedData)
	if err != nil || locator == nil || locator.Kind != workspace.ProjectEntryDirectoryReference {
		return false
	}
	dir, err := project.GetDirectoryReference(locator.DirectoryReferenceID)
	return err == nil && dir != nil && filepath.Clean(dir.Path) == filepath.Clean(folderPath)
}
