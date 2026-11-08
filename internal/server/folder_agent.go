package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderagenthttp"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// wireFolderAgent builds "Start an agent on <folder>" over the folder
// digest's chooser (picker, chips, typed-path tokens), the ordinary anchored
// creation pipeline, and the workspace store. Every dependency that a later
// phase wires is looked up at call time.
func (b *ServerBuilder) wireFolderAgent() {
	if b == nil || b.workspaceStore == nil || b.workspaceFileStore == nil || b.personalAssistantFolderDigest == nil {
		return
	}
	store := folderagent.NewStore(b.workspaceFileStore.GetFolderPath)
	service := folderagent.NewService(folderagent.Deps{
		Store:      store,
		Folders:    b.personalAssistantFolderDigest,
		Workspaces: folderAgentWorkspaces{builder: b},
		Tickets:    folderAgentTickets{store: b.workspaceStore},
		Pins:       folderAgentPins{store: b.workspaceStore},
		Turns:      folderAgentTurns{builder: b},
		Memory:     folderAgentMemory{store: workspace.NewMemoryStore(b.workspaceFileStore)},
		Setups:     folderAgentSetups{builder: b},
	})
	b.folderAgentService = service
	b.folderAgentHandler = folderagenthttp.New(service, func(ctx context.Context) (string, error) {
		if b.userProvider == nil {
			return "", errors.New("no user provider")
		}
		return b.userProvider.CurrentUserID(ctx)
	})
	if closed := service.SweepInterrupted(); closed > 0 {
		logger.Info("Closed interrupted folder agent looks", logger.Fields{"count": closed})
	}
}

// folderAgentTodayItems turns folder records into Today items: a look under
// Working on, a report (or a failed look) under Needs you, a finished setup
// under the folder results.
func (b *ServerBuilder) folderAgentTodayItems(ctx context.Context, userID string) (needs, working, done []personalassistant.TodayItem) {
	if b == nil || b.folderAgentService == nil {
		return nil, nil, nil
	}
	for _, view := range b.folderAgentService.List(ctx, userID) {
		item := personalassistant.TodayItem{
			ID: "folder-agent-" + view.WorkspaceID, Route: view.Route, Attribution: view.AgentName,
			Workspace: view.WorkspaceName, SourceAt: view.UpdatedAt, FolderAgent: view,
		}
		item.Ref.WorkspaceID, item.Ref.EntityType, item.Ref.EntityID = view.WorkspaceID, "workspace", view.WorkspaceID
		switch view.State {
		case folderagent.StateLooking, folderagent.StateSettingUp:
			item.Kind, item.State, item.Title = "folder_agent_look", "working", view.AgentName+" is looking around "+view.FolderName
			item.Detail = view.ProgressLine
			if view.State == folderagent.StateSettingUp {
				item.Title, item.Detail = "Setting up "+view.WorkspaceName, "Working through the plan you confirmed"
			}
			working = append(working, item)
		case folderagent.StateReported:
			item.Kind, item.State, item.Title = "folder_agent_report", "finished", view.AgentName+" looked around "+view.FolderName
			item.Detail, item.Actions = view.FactsLine, view.Actions
			needs = append(needs, item)
		case folderagent.StateFailed:
			item.Kind, item.State, item.Title = "folder_agent_report", "failed", view.AgentName+" could not finish looking around "+view.FolderName
			item.Detail, item.Actions = view.Failure, view.Actions
			needs = append(needs, item)
		case folderagent.StateSetUp:
			if view.Setup != nil && view.Setup.Status == personalassistant.FolderSetupStopped {
				item.Kind, item.State, item.Title = "folder_agent_report", "stopped", "Setting up "+view.WorkspaceName+" stopped"
				item.Detail, item.Actions = "A step did not finish. Retry carries on from there.", view.Actions
				needs = append(needs, item)
				continue
			}
			if view.Setup != nil && time.Since(view.Setup.FinishedAt) < 7*24*time.Hour {
				item.Kind, item.State, item.Title = "folder_agent_report", "done", view.WorkspaceName+" is set up"
				item.Detail = view.StageLine
				done = append(done, item)
			}
		}
	}
	return needs, working, done
}

type folderAgentWorkspaces struct{ builder *ServerBuilder }

func (w folderAgentWorkspaces) CreateAnchored(ctx context.Context, req folderagent.CreateRequest) (folderagent.Created, error) {
	b := w.builder
	if b.sessionHandler == nil {
		return folderagent.Created{}, folderagent.ErrUnavailable
	}
	created, err := b.sessionHandler.CreateFolderAgentWorkspace(ctx, sessionhttp.FolderAgentWorkspaceRequest{
		Name: req.Name, AnchorPath: req.AnchorPath, Mission: req.Mission, AgentName: req.AgentName,
	})
	var refusal *sessionhttp.FolderAgentRefusal
	if errors.As(err, &refusal) {
		out := &folderagent.Refusal{Code: refusal.Code, Message: refusal.Message, Status: refusal.Status}
		if refusal.Duplicate != nil {
			out.Duplicate = &folderagent.WorkspaceRef{ID: refusal.Duplicate.WorkspaceID, Name: refusal.Duplicate.Name, Route: workspaceRoute(refusal.Duplicate.Slug)}
		}
		return folderagent.Created{}, out
	}
	if err != nil {
		return folderagent.Created{}, err
	}
	out := folderagent.Created{ID: created.ID, Name: created.Name, Slug: created.Slug, AgentName: created.AgentName}
	if created.Nested != nil {
		out.Nested = &folderagent.WorkspaceRef{ID: created.Nested.WorkspaceID, Name: created.Nested.Name, Route: workspaceRoute(created.Nested.Slug)}
	}
	return out, nil
}

func (w folderAgentWorkspaces) NameTaken(ctx context.Context, name string) bool {
	return w.builder.sessionHandler != nil && w.builder.sessionHandler.WorkspaceNameTaken(ctx, name)
}

// AnchorRoot reads the anchored folder from the workspace's own directory
// reference; the folder record never holds it.
func (w folderAgentWorkspaces) AnchorRoot(workspaceID string) (string, error) {
	ws, err := w.builder.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		return "", fmt.Errorf("workspace %s is unavailable", workspaceID)
	}
	if ws.GetStatus() == workspace.StatusTrashed {
		return "", errors.New("the workspace is in the trash")
	}
	anchorID := ws.AnchorDirectoryReferenceID()
	for _, ref := range ws.DirectoryReferences {
		if ref.ID == anchorID && anchorID != "" {
			return ref.Path, nil
		}
	}
	return "", errors.New("the workspace has no anchored folder")
}

func (w folderAgentWorkspaces) IDs() ([]string, error) { return w.builder.workspaceStore.List() }

func (w folderAgentWorkspaces) Exists(workspaceID string) bool {
	ws, err := w.builder.workspaceStore.Get(workspaceID)
	return err == nil && ws != nil && ws.GetStatus() != workspace.StatusTrashed
}

func (w folderAgentWorkspaces) Route(workspaceID string) string {
	ws, err := w.builder.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		return ""
	}
	return workspaceRoute(ws.FolderSlug)
}

// SetGoal replaces the workspace goal with the suggested goal the owner kept.
func (w folderAgentWorkspaces) SetGoal(workspaceID, goal string) error {
	return w.builder.workspaceStore.Update(workspaceID, func(ws *workspace.Workspace) error {
		ws.Mission = goal
		return nil
	})
}

// folderAgentTurns resolves the folder agent's model the way the Library
// Manager's turn does: the agent's own provider and model in this workspace,
// tool-capable, or no model at all.
type folderAgentTurns struct{ builder *ServerBuilder }

func (t folderAgentTurns) Model(ctx context.Context, workspaceID, agentName string) (folderlook.Chat, string, error) {
	chat, model, err := t.builder.libraryManagerModel(ctx, workspaceID, agentName)
	if err != nil || chat == nil {
		return nil, "", folderlook.ErrNoModel
	}
	return folderlook.Chat(chat), model, nil
}

func (t folderAgentTurns) TokenBudget() int {
	if t.builder.configManager == nil {
		return 0
	}
	return t.builder.configManager.GetFirstLookTokenBudget()
}

// folderAgentMemory keeps one "What this folder is" entry per workspace: a
// later look replaces the earlier entry with a first-look provenance.
type folderAgentMemory struct{ store *workspace.MemoryStore }

func (m folderAgentMemory) Remember(workspaceID, text, provenance string) error {
	clean, err := workspace.ValidateMemoryText(text)
	if err != nil {
		return err
	}
	doc, err := m.store.Read(workspaceID)
	if err != nil {
		return err
	}
	for _, entry := range doc.Entries() {
		if strings.HasPrefix(entry.Provenance, folderlook.SourceFirstLookModel) && strings.HasPrefix(entry.Text, folderagent.MemoryTitle) {
			if _, err := m.store.RemoveByProvenance(workspaceID, entry.Provenance); err != nil && !errors.Is(err, workspace.ErrMemoryEntryNotFound) {
				return err
			}
		}
	}
	return m.store.Append(workspaceID, workspace.MemoryEntry{
		Type: workspace.MemoryTypeFact, Date: time.Now().Format("2006-01-02"), Provenance: provenance, Text: clean,
	})
}

func workspaceRoute(slug string) string {
	if strings.TrimSpace(slug) == "" {
		return ""
	}
	return "/workspaces/" + slug
}

// folderAgentLookContextKey marks the host-owned first ticket. The executor
// only runs assigned tickets, so an in-progress host ticket is never picked
// up by the agent's task loop.
const folderAgentLookContextKey = "folder_agent_look"

type folderAgentTickets struct{ store workspace.Store }

func (t folderAgentTickets) Open(workspaceID, title, agentName string) (string, error) {
	var id string
	err := t.store.Update(workspaceID, func(ws *workspace.Workspace) error {
		now := time.Now()
		task := workspace.Task{
			From: "Ori", To: agentName, Description: title, Status: workspace.TaskStatusInProgress,
			Details:   "Ori walks the folder (names, kinds, sizes and dates only) and reports what it found. Nothing is read, moved or installed.",
			Context:   map[string]any{folderAgentLookContextKey: true},
			CreatedAt: now, StartedAt: &now,
		}
		if err := ws.AddTask(task); err != nil {
			return err
		}
		id = ws.Tasks[len(ws.Tasks)-1].ID
		return nil
	})
	return id, err
}

func (t folderAgentTickets) Finish(workspaceID, ticketID, result string, failed bool) error {
	return t.store.Update(workspaceID, func(ws *workspace.Workspace) error {
		task, err := ws.GetTask(ticketID)
		if err != nil || task == nil {
			return err
		}
		updated := *task
		next := workspace.TaskStatusCompleted
		if failed {
			next = workspace.TaskStatusFailed
			updated.Error = result
		} else {
			updated.Result = result
		}
		if updated.Status != workspace.TaskStatusInProgress {
			// A later look re-opens the same ticket's result.
			updated.Status = workspace.TaskStatusInProgress
		}
		if err := updated.SetStatus(next); err != nil {
			return err
		}
		now := time.Now()
		updated.CompletedAt = &now
		return ws.UpdateTask(updated)
	})
}

// CreateBacklog adds the agent's suggested first ticket as a Ready ticket
// assigned to the folder's agent. The executor runs only assigned tickets, so
// it waits for the owner's Start this.
func (t folderAgentTickets) CreateBacklog(workspaceID, title, details, agentName string) (string, error) {
	var id string
	err := t.store.Update(workspaceID, func(ws *workspace.Workspace) error {
		task := workspace.Task{
			From: agentName, To: agentName, Description: title, Details: details, Status: workspace.TaskStatusPending,
			Context: map[string]any{folderAgentLookContextKey + "_next": true}, CreatedAt: time.Now(),
		}
		if err := ws.AddTask(task); err != nil {
			return err
		}
		id = ws.Tasks[len(ws.Tasks)-1].ID
		return nil
	})
	return id, err
}

// Start is the owner's confirmed start: the Ready ticket is assigned and the
// executor picks it up.
func (t folderAgentTickets) Start(workspaceID, ticketID string) error {
	return t.store.Update(workspaceID, func(ws *workspace.Workspace) error {
		task, err := ws.GetTask(ticketID)
		if err != nil || task == nil {
			return fmt.Errorf("the ticket is no longer here")
		}
		updated := *task
		if err := updated.SetStatus(workspace.TaskStatusAssigned); err != nil {
			return err
		}
		return ws.UpdateTask(updated)
	})
}

type folderAgentSetups struct{ builder *ServerBuilder }

func (s folderAgentSetups) Readiness(ctx context.Context, userID string) folderlook.Readiness {
	b := s.builder
	home, _ := os.UserHomeDir()
	ready := folderlook.Readiness{InstalledApps: folderdigest.DetectInstalledApps(home)}
	ready.IntegrationReady = func(key string) bool {
		entry, ok := reviewedintegration.Get(key)
		if !ok || b.folderSetupHost == nil {
			return false
		}
		facts, err := b.folderSetupHost.integrationFacts(ctx, userID, entry)
		return err == nil && facts.State == personalassistant.FolderInstallReady
	}
	ready.HomeExists = func(providerKey string) bool {
		exists, err := reviewedHomeExists(b, userID, providerKey)
		return err == nil && exists
	}
	return ready
}

// Run carries out a Tools-stage plan: each selected integration is installed
// (or enabled, or updated) through the host's install quest, then its
// components are attached to this workspace. Nothing else is touched.
func (s folderAgentSetups) Run(ctx context.Context, rec folderagent.Record, plan folderagent.Plan, update func([]personalassistant.FolderPlanLine)) (string, string) {
	b := s.builder
	lines := append([]personalassistant.FolderPlanLine(nil), plan.Lines...)
	set := func(kind, state string) {
		for i := range lines {
			if lines[i].Kind == kind && lines[i].State != personalassistant.FolderLineDone {
				lines[i].State = state
			}
		}
		update(append([]personalassistant.FolderPlanLine(nil), lines...))
	}
	stop := func(kind, reason string, cause error) (string, string) {
		set(kind, personalassistant.FolderLineFailed)
		fields := logger.Fields{"workspace_id": rec.WorkspaceID, "reason": reason}
		if cause != nil {
			fields["error"] = cause.Error()
		}
		logger.Warn("Folder agent setup stopped", fields)
		return personalassistant.FolderSetupStopped, reason
	}
	for _, candidate := range plan.Candidates {
		if candidate.Kind != folderlook.KindIntegration {
			return stop(personalassistant.FolderPlanWorkspace, personalassistant.FolderStopPlanChanged, errors.New("candidate kind not runnable: "+candidate.Kind))
		}
		entry, ok := reviewedintegration.Get(candidate.IntegrationKey)
		if !ok || b.folderSetupHost == nil {
			return stop(personalassistant.FolderPlanIntegration, personalassistant.FolderStopInstallFailed, errors.New("unknown integration"))
		}
		facts, err := b.folderSetupHost.integrationFacts(ctx, rec.OwnerUserID, entry)
		if err != nil {
			return stop(personalassistant.FolderPlanIntegration, personalassistant.FolderStopInstallFailed, err)
		}
		if facts.State != personalassistant.FolderInstallReady {
			set(personalassistant.FolderPlanIntegration, personalassistant.FolderLineWorking)
			if b.setupJourneyService == nil {
				return stop(personalassistant.FolderPlanIntegration, personalassistant.FolderStopInstallFailed, errors.New("no setup journey service"))
			}
			install, err := b.setupJourneyService.ForHostQuest(ctx, rec.OwnerUserID, entry.InstallQuestID())
			if err != nil {
				return stop(personalassistant.FolderPlanIntegration, personalassistant.FolderStopInstallFailed, err)
			}
			if err := foldersetup.InstallReviewedIntegration(ctx, scopedJourney{service: install, userID: rec.OwnerUserID},
				facts.State, facts.PluginID, facts.Version); err != nil {
				reason := foldersetup.StopReason(err)
				if reason == "" {
					reason = personalassistant.FolderStopInstallFailed
				}
				return stop(personalassistant.FolderPlanIntegration, reason, err)
			}
		}
		set(personalassistant.FolderPlanIntegration, personalassistant.FolderLineDone)
		set(personalassistant.FolderPlanWorkspace, personalassistant.FolderLineWorking)
		if b.pluginHandler == nil {
			return stop(personalassistant.FolderPlanWorkspace, personalassistant.FolderStopFailed, errors.New("no plugin manager"))
		}
		result, err := pluginworkspace.New(b.pluginHandler.Manager(), b.workspaceStore).Reconcile(pluginworkspace.Request{
			WorkspaceID: rec.WorkspaceID, Plugins: []string{entry.PluginID},
		})
		if err == nil && result.SaveErr != nil {
			err = result.SaveErr
		}
		if err != nil {
			return stop(personalassistant.FolderPlanWorkspace, personalassistant.FolderStopFailed, err)
		}
		set(personalassistant.FolderPlanWorkspace, personalassistant.FolderLineDone)
	}
	set(personalassistant.FolderPlanFolder, personalassistant.FolderLineDone)
	return personalassistant.FolderSetupDone, ""
}
