package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/hostquests"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fileJanitorBlueprintID is the File Janitor blueprint's template ID, the one
// new workspaces record. The retired Downloads Janitor ID still identifies a
// File Janitor workspace created before the rename.
const fileJanitorBlueprintID = "file-janitor"

// calendarOpsBlueprintID is the Calendar Ops blueprint's template ID.
const calendarOpsBlueprintID = "calendar-ops"

// starterWorkspaceSource is the provenance-hydrated workspace read the starter
// missions use. The SQLite-primary list does not carry TemplateProvenance, so
// every "which blueprint is this" check reads the folder store instead.
//
// The mission context runs on the Quests widget's poll, so candidates come from
// the folder store's metadata cache (no chat history or tasks) and only a
// matching workspace is re-read from disk for its current setup state.
type starterWorkspaceSource interface {
	CachedWorkspaces() map[string]*workspace.Workspace
	Get(id string) (*workspace.Workspace, error)
}

// starterWorkspaces returns the folder store, or nil when it is not wired. The
// explicit nil check keeps a nil *FileStore from becoming a non-nil interface.
func (b *ServerBuilder) starterWorkspaces() starterWorkspaceSource {
	if b.workspaceFileStore == nil {
		return nil
	}
	return b.workspaceFileStore
}

// starterMissionContext supplies the per-user state the starter missions
// resolve their Quests card from (tasks/prd-starter-missions.md FR7).
//
// It runs on every GET /api/progression, which the Home widget polls, so every
// read here is local and cheap. Each source is read at call time rather than
// captured, because several of them are wired after progression is, and a nil
// source leaves its field zero.
func (b *ServerBuilder) starterMissionContext() progression.MissionContext {
	var mission progression.MissionContext
	ctx := context.Background()

	if b.personalAssistantService != nil {
		if state, err := b.personalAssistantService.Get(ctx, userprofile.LocalUserID); err == nil && state != nil {
			for _, focus := range state.FocusAreas {
				mission.FocusAreas = append(mission.FocusAreas, string(focus))
			}
			mission.ModelConfigured = state.Availability.Model.Available
		}
	}

	if slug, ready, ok := findJanitorWorkspace(b.starterWorkspaces()); ok {
		mission.FileJanitor = &progression.MissionWorkspace{Slug: slug, WizardReady: ready}
	}

	// The email branch opens the one-card setup; the guided quest stays
	// reachable for anyone who already started it.
	mission.EmailQuestURL = emailSetupURL
	// Only the email branch shows "In progress", and only while Mission 03 is
	// open, so the setup journey is read just for that case.
	if progression.ChooseConnectSourceBranch(mission.FocusAreas) == progression.BranchEmail &&
		(b.progressionEngine == nil || !b.progressionEngine.HasCompleted(progression.ConnectSourceQuestID)) {
		mission.EmailQuestStarted = b.emailSetupStarted()
	}

	// Read your first Daily Brief opens the station where the brief is read.
	mission.DailyBriefURL = b.dailyBriefStationURL(ctx)

	// See what your assistant found resolves from the latest folder workspace's
	// first look. It is read only while the mission is open: once it is done or
	// skipped the card no longer shows it.
	if b.progressionEngine == nil || b.progressionEngine.MissionUnresolved(progression.FolderFirstLookQuestID) {
		mission.FolderFirstTask = b.missionFirstTask(ctx)
	}

	return mission
}

// missionFirstTask is where the first look of the user's most recent folder
// workspace stands, for Mission 03's card. The folder digest picks the look, so
// the card and the Home receipt always follow the same one. The zero value
// means there is nothing to point at.
func (b *ServerBuilder) missionFirstTask(ctx context.Context) progression.MissionFirstTask {
	if b.personalAssistantFolderDigest == nil {
		return progression.MissionFirstTask{}
	}
	view, ok, err := b.personalAssistantFolderDigest.LatestFirstLook(ctx, userprofile.LocalUserID)
	if err != nil || !ok {
		return progression.MissionFirstTask{}
	}
	return missionFirstTaskOf(view)
}

// missionFirstTaskOf is the mission card's reading of a first look. A look that
// cannot start, or that paused to ask something, carries the one sentence that
// says why.
func missionFirstTaskOf(view personalassistant.FolderFirstTaskView) progression.MissionFirstTask {
	task := progression.MissionFirstTask{
		WorkspaceName: view.WorkspaceName, WorkspaceRoute: view.WorkspaceRoute,
		FolderName: view.FolderName, State: progression.MissionFirstTaskState(view.State),
		CanStart: view.CanStart, TicketRoute: view.TicketRoute,
	}
	switch {
	case view.State == personalassistant.FolderFirstTaskWaiting:
		task.Blocked = view.Message
	case view.State == personalassistant.FolderFirstTaskSeeded && !view.CanStart:
		task.Blocked = view.Message
		if view.Reason == personalassistant.FolderFirstTaskReasonNoModel {
			// A missing model is fixed in Settings, not in the workspace.
			task.BlockedURL, task.BlockedLabel = systemModelSettingsURL, "Open Settings"
		}
	}
	return task
}

// systemModelSettingsURL is where the user adds the model a first look needs.
const systemModelSettingsURL = "/settings#system-model"

// folderFirstTaskView reads where a workspace's first look stands and adds the
// one fact only the server knows: with no model configured, a start would
// fail, so the view says so instead of offering the button.
func (b *ServerBuilder) folderFirstTaskView(ctx context.Context, workspaceID string, modelConfigured bool) (personalassistant.FolderFirstTaskView, bool) {
	if b == nil || b.sessionHandler == nil {
		return personalassistant.FolderFirstTaskView{}, false
	}
	view, ok := b.sessionHandler.FolderFirstTaskView(ctx, workspaceID)
	if !ok {
		return personalassistant.FolderFirstTaskView{}, false
	}
	if view.State == personalassistant.FolderFirstTaskSeeded && view.CanStart && !modelConfigured {
		view.CanStart = false
		view.Reason = personalassistant.FolderFirstTaskReasonNoModel
		view.Message = personalassistant.FolderFirstTaskMessage(view.Reason, view.WorkspaceName)
	}
	return view, true
}

// assistantModelConfigured reports whether a model is available to the
// assistant, the same reading Read your first Daily Brief uses.
func (b *ServerBuilder) assistantModelConfigured(ctx context.Context) bool {
	if b == nil || b.personalAssistantService == nil {
		return false
	}
	state, err := b.personalAssistantService.Get(ctx, userprofile.LocalUserID)
	return err == nil && state != nil && state.Availability.Model.Available
}

// folderFirstTaskFinished reports whether a task.completed event is a folder's
// first look finishing with a result, which is what See what your assistant
// found pays for. The event names only the workspace and the task, so the
// folder store supplies the task's template context. A start from the workspace
// page counts exactly as a start from Home does: the mission watches the
// result, not the button.
func folderFirstTaskFinished(src starterWorkspaceSource, ev workspace.Event) bool {
	if src == nil || ev.Type != workspace.EventTaskCompleted || strings.TrimSpace(ev.WorkspaceID) == "" {
		return false
	}
	taskID, _ := ev.Data["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return false
	}
	ws, err := src.Get(ev.WorkspaceID)
	if err != nil || ws == nil {
		return false
	}
	task := personalassistant.FindFolderFirstTask(ws)
	if task == nil || task.ID != strings.TrimSpace(taskID) {
		return false
	}
	result, _ := ev.Data["result"].(string)
	if strings.TrimSpace(result) == "" {
		result = task.Result
	}
	return strings.TrimSpace(result) != ""
}

// anyFolderFirstTaskFinished reports whether any active workspace already holds
// a finished first look, the grandfathering evidence for Mission 03. Tasks are
// not in the metadata cache, so each candidate is read from disk; this runs
// only in the one-time startup passes, never on a poll.
func anyFolderFirstTaskFinished(src starterWorkspaceSource) bool {
	if src == nil {
		return false
	}
	for id, lean := range src.CachedWorkspaces() {
		if lean == nil || lean.GetStatus() != workspace.StatusActive || strings.EqualFold(lean.Kind, "group") ||
			!ownedBy(lean, userprofile.LocalUserID) {
			continue
		}
		ws, err := src.Get(id)
		if err != nil || ws == nil || ws.GetStatus() != workspace.StatusActive {
			continue
		}
		if personalassistant.FolderFirstTaskFinishedWithResult(personalassistant.FindFolderFirstTask(ws)) {
			return true
		}
	}
	return false
}

// dailyBriefStationURL is the link that opens the Daily Brief station's panel
// in the local user's Personal HQ, or "" when no valid HQ is designated.
func (b *ServerBuilder) dailyBriefStationURL(ctx context.Context) string {
	if b == nil || b.personalHQService == nil {
		return ""
	}
	status, err := b.personalHQService.Status(ctx, userprofile.LocalUserID)
	if err != nil || !status.Valid || status.Workspace == nil {
		return ""
	}
	return dailybrief.StationURL(status.Workspace.FolderSlug)
}

// briefStationURLFor is the Daily Brief station link for the workspace a brief
// belongs to, or "" when that workspace's browser route cannot be read.
func (b *ServerBuilder) briefStationURLFor(workspaceID string) string {
	if b == nil || b.sessionStore == nil {
		return ""
	}
	ws, err := b.sessionStore.GetWorkspace(context.Background(), strings.TrimSpace(workspaceID))
	if err != nil || ws == nil {
		return ""
	}
	return dailybrief.StationURL(ws.FolderSlug)
}

// emailSetupStatus reads the guided Email Ops setup without creating it.
func (b *ServerBuilder) emailSetupStatus() (*setupjourney.JourneyProjection, bool) {
	if b.setupJourneyService == nil {
		return nil, false
	}
	ctx := context.Background()
	scoped, err := b.setupJourneyService.ForHostQuest(ctx, userprofile.LocalUserID, hostquests.EmailOpsSetupQuestID)
	if err != nil {
		return nil, false
	}
	projection, exists, err := scoped.Status(ctx, userprofile.LocalUserID)
	if err != nil || !exists || projection == nil {
		return nil, false
	}
	return projection, true
}

// emailSetupStarted is true when the guided email setup exists and is not ready.
func (b *ServerBuilder) emailSetupStarted() bool {
	projection, exists := b.emailSetupStatus()
	return exists && projection.Lifecycle != setupjourney.LifecycleReady
}

// emailSetupEverReady is true when the guided email setup has been ready once.
func (b *ServerBuilder) emailSetupEverReady() bool {
	projection, exists := b.emailSetupStatus()
	return exists && projection.FirstCompletedAt != nil
}

// onEmailSetupFirstReady completes Mission 03 when the guided Email Ops setup
// first reaches ready. Every other journey is ignored.
func onEmailSetupFirstReady(engine *progression.Engine) func(userID string, key setupjourney.QuestKey) {
	return func(_ string, key setupjourney.QuestKey) {
		if engine == nil || key.Source != setupjourney.QuestSourceHost || key.ID != hostquests.EmailOpsSetupQuestID {
			return
		}
		engine.Complete(progression.ConnectSourceQuestID)
	}
}

// calendarBindingConnected reports whether a workspace.updated event is a new
// MCP binding that leaves a Calendar Ops workspace connected, the calendar
// branch of Mission 03. The event names only the workspace, so the folder store
// supplies its provenance and bindings.
func calendarBindingConnected(src starterWorkspaceSource, ev workspace.Event) bool {
	if src == nil || ev.Type != workspace.EventWorkspaceUpdated || strings.TrimSpace(ev.WorkspaceID) == "" {
		return false
	}
	if action, _ := ev.Data["action"].(string); action != "mcp_binding_created" {
		return false
	}
	ws, err := src.Get(ev.WorkspaceID)
	return err == nil && ws != nil && ws.IsFromTemplate(calendarOpsBlueprintID) &&
		personalassistant.HasReadyCalendarBinding(ws)
}

// scanStarterWorkspaces reads the backfill evidence for Mission 03 from the
// folder store: whether a Calendar Ops workspace is already connected, and how
// many active workspaces are projects (not a group, not HQ, not a starter
// blueprint).
func scanStarterWorkspaces(src starterWorkspaceSource, hqWorkspaceID string) (calendarReady bool, projects int) {
	if src == nil {
		return false, 0
	}
	for id, lean := range src.CachedWorkspaces() {
		if lean == nil || lean.GetStatus() != workspace.StatusActive || !ownedBy(lean, userprofile.LocalUserID) {
			continue
		}
		templateID := ""
		if provenance := lean.GetTemplateProvenance(); provenance != nil {
			templateID = provenance.TemplateID
		}
		if templateID == calendarOpsBlueprintID && !calendarReady {
			if ws, err := src.Get(id); err == nil && personalassistant.HasReadyCalendarBinding(ws) {
				calendarReady = true
			}
		}
		if id == hqWorkspaceID || strings.EqualFold(lean.Kind, "group") || progression.IsStarterTemplateID(templateID) {
			continue
		}
		projects++
	}
	return calendarReady, projects
}

// isFileJanitorWorkspace reports whether a workspace was created from the File
// Janitor blueprint, under its current or its retired ID.
func isFileJanitorWorkspace(ws *workspace.Workspace) bool {
	return ws != nil && (ws.IsFromTemplate(fileJanitorBlueprintID) || ws.IsFromTemplate(filejanitor.LegacyTemplateID))
}

// ownedBy reports whether a workspace belongs to userID. An empty owner is the
// local single user's, the same rule the Email Ops locator applies.
func ownedBy(ws *workspace.Workspace, userID string) bool {
	owner := strings.TrimSpace(ws.OwnerUserID)
	return owner == "" || strings.EqualFold(owner, strings.TrimSpace(userID))
}

// wizardReady reports whether a workspace's setup wizard has ever passed.
// CompletedAt survives a later regression, which is the right reading for a
// mission: the user did finish setup.
func wizardReady(ws *workspace.Workspace) bool {
	progress := ws.GetSetupWizardProgress()
	return progress != nil && progress.CompletedAt != nil
}

// findJanitorWorkspace returns the local user's File Janitor workspace for
// Mission 02. Progression is per install and single-user, so ownership is
// checked against the local user. A ready one wins, because any ready workspace
// completes the mission; among equals the most recently updated wins. ok is
// false when none exists.
func findJanitorWorkspace(src starterWorkspaceSource) (slug string, ready bool, ok bool) {
	if src == nil {
		return "", false, false
	}
	var best *workspace.Workspace
	bestReady := false
	for id, lean := range src.CachedWorkspaces() {
		// Provenance never changes after creation, so the cached copy is enough
		// to pick candidates.
		if lean == nil || lean.GetStatus() != workspace.StatusActive || !isFileJanitorWorkspace(lean) {
			continue
		}
		ws, err := src.Get(id)
		if err != nil || ws == nil || ws.GetStatus() != workspace.StatusActive ||
			!ownedBy(ws, userprofile.LocalUserID) || !isFileJanitorWorkspace(ws) {
			continue
		}
		candidateReady := wizardReady(ws)
		better := best == nil ||
			(candidateReady && !bestReady) ||
			(candidateReady == bestReady && ws.UpdatedAt.After(best.UpdatedAt)) ||
			// The cache is a map; break exact ties by ID so the card is stable.
			(candidateReady == bestReady && ws.UpdatedAt.Equal(best.UpdatedAt) && ws.ID < best.ID)
		if better {
			best, bestReady = ws, candidateReady
		}
	}
	if best == nil {
		return "", false, false
	}
	return best.FolderSlug, bestReady, true
}

// janitorWorkspaceByID reports whether a workspace ID names an active File
// Janitor workspace, for the wizard completion hook, which receives only an ID.
func janitorWorkspaceByID(src starterWorkspaceSource, workspaceID string) bool {
	if src == nil || strings.TrimSpace(workspaceID) == "" {
		return false
	}
	ws, err := src.Get(workspaceID)
	return err == nil && ws != nil && ws.GetStatus() == workspace.StatusActive && isFileJanitorWorkspace(ws)
}

// composeCompletionHooks runs every consumer, in order, for one completion.
// setupwizard.Service holds a single hook slot, so every consumer is composed
// here rather than replacing the one before it.
func composeCompletionHooks(hooks ...setupwizard.CompletionHook) setupwizard.CompletionHook {
	return func(ctx context.Context, workspaceID string) {
		for _, hook := range hooks {
			if hook != nil {
				hook(ctx, workspaceID)
			}
		}
	}
}

// completeShowFolderOnWizardReady completes Mission 03 when the setup wizard
// that just reached ready belongs to a File Janitor workspace: a tidy is one
// of the two things showing a folder can lead to (FR42).
//
// The engine is read at call time. Progression is built after the setup wizard
// is wired, so capturing it at wiring time would bind nil forever.
func (b *ServerBuilder) completeShowFolderOnWizardReady(_ context.Context, workspaceID string) {
	engine := b.progressionEngine
	if engine == nil || !janitorWorkspaceByID(b.starterWorkspaces(), workspaceID) {
		return
	}
	engine.Complete(progression.ShowFolderQuestID)
}
