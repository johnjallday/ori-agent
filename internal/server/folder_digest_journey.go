package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
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

// reviewedExistingHome returns the owner's existing Home for a reviewed provider
// (its workspace ID and route) from the owner-scoped station and the workspace
// row. Unlike VerifiedHome it proves no creation time and resolves no release,
// so it is safe to call on a page load; the library still re-verifies the Home
// and provider before any consequence. Missing, ambiguous, foreign, or
// unreadable state fails closed so the offer falls back to a plain suggestion.
func reviewedExistingHome(ctx context.Context, b *ServerBuilder, userID, providerKey string) (personalassistant.FolderCreateResult, error) {
	refused := personalassistant.ErrFolderWorkspaceRefused
	if b == nil || b.workspaceFileStore == nil || b.sessionStore == nil || userID == "" {
		return personalassistant.FolderCreateResult{}, refused
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
		return personalassistant.FolderCreateResult{}, refused
	}
	programs := workspace.NewAssistantProgramStore(b.workspaceFileStore)
	station, err := programs.FindStation(workspace.AssistantProgramKey{OwnerUserID: userID, PluginID: owner.PluginID, ProgramID: owner.ProgramID})
	if err != nil || station == nil || station.OwnerUserID != userID || station.ID == "" {
		return personalassistant.FolderCreateResult{}, refused
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.HomeProvider == nil || state.HomeProvider.PluginID != owner.PluginID || state.HomeProvider.ProgramID != owner.ProgramID {
		return personalassistant.FolderCreateResult{}, refused
	}
	row, err := b.sessionStore.GetWorkspace(ctx, station.ID)
	if err != nil || row == nil || row.OwnerUserID != userID || !row.IsGroup() || strings.TrimSpace(row.FolderSlug) == "" {
		return personalassistant.FolderCreateResult{}, refused
	}
	return personalassistant.FolderCreateResult{WorkspaceID: station.ID, Route: "/workspaces/" + row.FolderSlug}, nil
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
	// Every refusal is the same answer to the caller; the log says which check
	// refused, with no path or identity in it.
	refuse := func(why string) (personalassistant.FolderCreateResult, error) {
		logger.Info("A setup run could not be verified for its folder", logger.Fields{"reason": why})
		return personalassistant.FolderCreateResult{}, personalassistant.ErrFolderWorkspaceRefused
	}
	b := v.builder
	if b == nil || b.setupJourneyService == nil || b.workspaceFileStore == nil || b.sessionStore == nil || runID == "" {
		return refuse("not wired")
	}
	entry, ok := reviewedintegration.Get(integrationKey)
	if !ok || entry.ExpectedBlueprintID != blueprintID {
		return refuse("unreviewed integration")
	}
	_, questID, found := reviewedProjectQuest(ctx, b, integrationKey, blueprintID)
	if !found {
		return refuse("no single plugin quest")
	}
	scoped, err := b.setupJourneyService.ForQuest(ctx, userID, entry.PluginID, questID)
	if err != nil {
		return refuse("quest unavailable")
	}
	journey, err := scoped.Read(ctx, userID, runID)
	if err != nil {
		return refuse("run unreadable")
	}
	if !freshJourneyProject(journey, runID, acceptedAfter) {
		return refuse("run is not a fresh, finished project run")
	}
	id := journey.Receipts.ProjectWorkspaceID
	project, err := b.workspaceFileStore.Get(id)
	if err != nil || project == nil || project.OwnerUserID != userID {
		return refuse("project workspace unreadable or foreign")
	}
	if !journeyProjectMatchesFolder(project, userID, entry.PluginID, blueprintID, folderPath) {
		return refuse("project is not this folder's")
	}
	row, err := b.sessionStore.GetWorkspace(ctx, id)
	if err != nil || row == nil || row.OwnerUserID != userID || row.IsGroup() || strings.TrimSpace(row.FolderSlug) == "" {
		return refuse("project row unreadable")
	}
	result := personalassistant.FolderCreateResult{WorkspaceID: id, Route: "/workspaces/" + row.FolderSlug,
		HomeRoute: v.verifiedProjectHomeRoute(ctx, userID, project)}
	if result.HomeRoute != "" {
		parent, err := b.workspaceStore.Get(project.ParentID)
		if err != nil {
			return refuse("the resulting Home receipt is unavailable")
		}
		result.Parent, err = folderSetupHomeDestination(parent)
		if err != nil {
			return refuse("the resulting Home receipt is unavailable")
		}
	}
	// The receipt is what the card shows once the setup is proved. It is a
	// best-effort read: without it the card falls back to its plain outcome note.
	if b.sessionHandler != nil {
		if rows, receiptErr := b.sessionHandler.FolderOfferWorkspaceReceipt(id, true); receiptErr == nil {
			result.Receipt = rows
		}
	}
	if result.Parent != nil {
		result.Receipt = append(result.Receipt, personalassistant.FolderReceiptRow{Kind: "home", Name: result.Parent.Name, Detail: "verified parent; this project is a separate child", Route: result.HomeRoute})
	}
	return result, nil
}

// An offer cannot infer a Home from its name or selected folder. Only a
// current, reciprocal child link to the owner's real Home supplies a route to
// the library's explicit association review. This is navigation, not adoption.
func (v folderJourneyVerifier) verifiedProjectHomeRoute(ctx context.Context, owner string, child *workspace.Workspace) string {
	b := v.builder
	if b == nil || child == nil || b.workspaceStore == nil || b.workspaceFileStore == nil || b.sessionStore == nil {
		return ""
	}
	primary, err := b.workspaceStore.Get(child.ID)
	if err != nil || primary == nil || primary.OwnerUserID != owner {
		return ""
	}
	folder, err := b.workspaceFileStore.Get(child.ID)
	if err != nil || folder == nil || folder.OwnerUserID != owner {
		return ""
	}
	link, folderLink := primary.GetAssistantProjectLink(), folder.GetAssistantProjectLink()
	if link == nil || folderLink == nil || link.ID != folderLink.ID || link.StateRevision != folderLink.StateRevision ||
		link.Key.Normalize() != folderLink.Key.Normalize() || link.StationWorkspaceID != folderLink.StationWorkspaceID ||
		link.ID != workspace.AssistantProjectLinkID(link.StationWorkspaceID, child.ID) {
		return ""
	}
	home, err := b.workspaceStore.Get(link.StationWorkspaceID)
	if err != nil || home == nil || home.OwnerUserID != owner || home.Status == workspace.StatusTrashed ||
		home.Status == workspace.StatusMissing {
		return ""
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.Key.Normalize() != link.Key.Normalize() || state.Key.Normalize().OwnerUserID != owner {
		return ""
	}
	listed := false
	for _, id := range state.LinkedProjectIDs {
		listed = listed || id == child.ID
	}
	if !listed {
		return ""
	}
	row, err := b.sessionStore.GetWorkspace(ctx, home.ID)
	if err != nil || row == nil || row.OwnerUserID != owner || !row.IsGroup() || strings.TrimSpace(row.FolderSlug) == "" {
		return ""
	}
	return "/workspaces/" + row.FolderSlug + "/assistant#projectLibraryPanel"
}

// A plugin quest's root run reports its source. A child run (a further project)
// carries none by design: it holds no relationship identity of its own and is
// authorized through its root. The caller reads it through the plugin quest's
// scoped service, which refuses any run whose root is not that quest's, so a
// child with a root is that quest's.
func journeySourceIsPlugin(journey *setupjourney.JourneyProjection) bool {
	if journey.Journey.Source == setupjourney.QuestSourcePlugin {
		return true
	}
	return journey.RunKind == setupjourney.RunKindChild && journey.Journey.Source == "" && journey.RootRunID != ""
}

func freshJourneyProject(journey *setupjourney.JourneyProjection, runID string, acceptedAfter time.Time) bool {
	return journey != nil && journey.RunID == runID &&
		journeySourceIsPlugin(journey) &&
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
