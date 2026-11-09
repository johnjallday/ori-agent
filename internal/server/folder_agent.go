package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderagenthttp"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
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
		Matches:    folderAgentMatches{builder: b},
		Memory:     folderAgentMemory{store: workspace.NewMemoryStore(b.workspaceFileStore)},
		Setups:     folderAgentSetups{builder: b},
		Hiring:     folderAgentHiring{builder: b},
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
		BindExisting: req.BindExisting, Provider: req.Provider, Model: req.Model, Mandate: req.Mandate,
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

// AnchorOwners finds the workspaces (other than exclude) anchored to exactly
// one of folders, from each workspace's own anchor reference.
func (w folderAgentWorkspaces) AnchorOwners(folders []string, exclude string) map[string]folderagent.WorkspaceRef {
	want := make(map[string]bool, len(folders))
	for _, folder := range folders {
		want[filepath.Clean(folder)] = true
	}
	out := map[string]folderagent.WorkspaceRef{}
	ids, err := w.builder.workspaceStore.List()
	if err != nil {
		return out
	}
	for _, id := range ids {
		if id == exclude {
			continue
		}
		ws, err := w.builder.workspaceStore.Get(id)
		if err != nil || ws == nil || ws.GetStatus() == workspace.StatusTrashed {
			continue
		}
		anchorID := ws.AnchorDirectoryReferenceID()
		for _, ref := range ws.DirectoryReferences {
			if anchorID != "" && ref.ID == anchorID && want[filepath.Clean(ref.Path)] {
				out[filepath.Clean(ref.Path)] = folderagent.WorkspaceRef{ID: ws.ID, Name: ws.Name, Route: workspaceRoute(ws.FolderSlug)}
			}
		}
	}
	return out
}

// SetGoal replaces the workspace goal with the suggested goal the owner kept.
func (w folderAgentWorkspaces) SetGoal(workspaceID, goal string) error {
	return w.builder.workspaceStore.Update(workspaceID, func(ws *workspace.Workspace) error {
		ws.Mission = goal
		return nil
	})
}

// folderAgentHiring is the card's Hire section: the models a new agent can
// have and the owner's own agents to reuse. Only tool-capable providers are
// offered (the look calls tools). Cloud and CLI providers are registered only
// with credentials, so they can run; local servers are always registered
// whether or not they run, so a local model is offered only when the system
// model is on that server. The system model comes first.
type folderAgentHiring struct{ builder *ServerBuilder }

const folderAgentHireModelsMax = 20

// datedModelAlias matches a pinned snapshot ("…-20251101"): the undated name
// beside it is the one to offer.
var datedModelAlias = regexp.MustCompile(`-\d{8}$`)

func (h folderAgentHiring) Models(context.Context) []folderagent.HireModel {
	b := h.builder
	if b.llmFactory == nil {
		return nil
	}
	var out []folderagent.HireModel
	seen := map[string]bool{}
	add := func(provider, model string, isDefault bool) {
		key := strings.ToLower(provider + "\x00" + model)
		if provider == "" || model == "" || seen[key] || len(out) >= folderAgentHireModelsMax {
			return
		}
		seen[key] = true
		out = append(out, folderagent.HireModel{Provider: provider, Model: model, Label: model + " (" + provider + ")", Default: isDefault})
	}
	providers := b.llmFactory.ListProviders()
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	toolCapable := map[string]bool{}
	for _, p := range providers {
		toolCapable[strings.ToLower(p.Name)] = p.Capabilities.SupportsTools
	}
	systemProvider := ""
	if b.configManager != nil && b.configManager.IsSystemModelConfigured() {
		provider, model := b.configManager.GetSystemModel()
		systemProvider = strings.ToLower(provider)
		if toolCapable[systemProvider] {
			add(systemProvider, model, true)
		}
	}
	for _, p := range providers {
		if !p.Capabilities.SupportsTools || (p.Type == llm.ProviderTypeLocal && strings.ToLower(p.Name) != systemProvider) {
			continue
		}
		for _, model := range p.Models {
			if datedModelAlias.MatchString(model) {
				continue
			}
			add(strings.ToLower(p.Name), model, false)
		}
	}
	return out
}

// Agents is the owner's own agents: the built-in assistant and agents that
// only a workspace holds are never offered.
func (h folderAgentHiring) Agents(context.Context, string) []folderagent.HireAgent {
	st := h.builder.st
	if st == nil {
		return nil
	}
	root, hasRoot := st.(interface{ RootAgentFolder(string) (string, bool) })
	var out []folderagent.HireAgent
	for _, name := range st.ListAgents() {
		if systemassistant.IsCanonicalName(name) {
			continue
		}
		if hasRoot {
			if _, ok := root.RootAgentFolder(name); !ok {
				continue
			}
		}
		agent, ok := st.GetAgent(name)
		if !ok || agent == nil {
			continue
		}
		// The personal assistant's own profile keeps its job; it is never a
		// folder's agent.
		if agent.Metadata != nil && personalassistant.ProfileProvenanceFromTags(name, agent.Metadata.Tags).AssistantID != "" {
			continue
		}
		out = append(out, folderagent.HireAgent{Name: name, Model: agent.Settings.Model})
	}
	return out
}

func (h folderAgentHiring) AgentExists(name string) bool {
	st := h.builder.st
	if st == nil {
		return false
	}
	if _, ok := st.GetAgent(name); ok {
		return true
	}
	for _, existing := range st.ListAgents() {
		if strings.EqualFold(existing, name) {
			return true
		}
	}
	return false
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

// addFolderAgentFileCurator installs File Janitor in place on the folder's
// workspace (the install writes its record only: no folder access, watcher or
// schedule until its own setup) and adds File Curator as its companion. A
// repeat is success: both steps are idempotent.
func (b *ServerBuilder) addFolderAgentFileCurator(workspaceID string, set func(kind, state, progress string)) error {
	service := b.workspaceCapabilityService
	if service == nil {
		return errors.New("workspace capabilities are not wired")
	}
	set(folderagent.FolderPlanCapability, personalassistant.FolderLineWorking, "")
	if _, err := service.Install(workspacecapability.InstallRequest{
		WorkspaceID: workspaceID, CapabilityID: workspace.CapabilityFileJanitor, Source: workspace.InstallSourceInPlace,
	}); err != nil {
		return err
	}
	set(folderagent.FolderPlanCapability, personalassistant.FolderLineDone, "")
	set(personalassistant.FolderPlanAgents, personalassistant.FolderLineWorking, "")
	if _, err := service.AddCompanion(workspaceID, workspace.CapabilityFileJanitor); err != nil {
		set(personalassistant.FolderPlanAgents, personalassistant.FolderLineFailed, "")
		return err
	}
	set(personalassistant.FolderPlanAgents, personalassistant.FolderLineDone, "")
	return nil
}

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
	// set moves every unfinished line of a kind; setLine one line by name. Each
	// carries the runner's words for the line (empty keeps working words, and
	// clears them on done) and stamps its start and finish times.
	set := func(kind, state, progress string) {
		now := time.Now().UTC()
		for i := range lines {
			if lines[i].Kind == kind && lines[i].State != personalassistant.FolderLineDone {
				lines[i].Mark(state, progress, now)
			}
		}
		update(append([]personalassistant.FolderPlanLine(nil), lines...))
	}
	stop := func(kind, reason string, cause error) (string, string) {
		set(kind, personalassistant.FolderLineFailed, "")
		fields := logger.Fields{"workspace_id": rec.WorkspaceID, "reason": reason}
		if cause != nil {
			fields["error"] = cause.Error()
		}
		logger.Warn("Folder agent setup stopped", fields)
		return personalassistant.FolderSetupStopped, reason
	}
	setLine := func(name, state, progress string) {
		now := time.Now().UTC()
		for i := range lines {
			if lines[i].Name == name {
				lines[i].Mark(state, progress, now)
			}
		}
		update(append([]personalassistant.FolderPlanLine(nil), lines...))
	}
	if plan.Stage == foldermap.StageGroup {
		if reason, err := b.runFolderAgentGroup(ctx, rec, lines, setLine); err != nil {
			logger.Warn("Folder agent group setup stopped", logger.Fields{"workspace_id": rec.WorkspaceID, "reason": reason, "error": err.Error()})
			return personalassistant.FolderSetupStopped, reason
		}
	}
	for _, candidate := range plan.Candidates {
		switch candidate.Kind {
		case folderlook.KindSkill, folderlook.KindMCP, folderlook.KindPlugin:
			line := ""
			for _, l := range lines {
				if l.Kind == folderagent.FolderPlanAddOn && strings.Contains(l.Name, candidate.Name) {
					line = l.Name
					break
				}
			}
			setLine(line, personalassistant.FolderLineWorking, "")
			if err := b.addFolderAgentAddOn(ctx, rec.WorkspaceID, candidate); err != nil {
				setLine(line, personalassistant.FolderLineFailed, "")
				logger.Warn("Folder agent setup stopped", logger.Fields{"workspace_id": rec.WorkspaceID, "add_on": candidate.ID, "error": err.Error()})
				return personalassistant.FolderSetupStopped, personalassistant.FolderStopInstallFailed
			}
			setLine(line, personalassistant.FolderLineDone, "")
			continue
		case folderlook.KindHomeProvider:
			receipt, err := b.runFolderAgentHome(ctx, rec, candidate, lines, setLine)
			var stopped *homeStageStop
			if errors.As(err, &stopped) {
				logger.Warn("Folder agent Home setup stopped", logger.Fields{"workspace_id": rec.WorkspaceID, "reason": stopped.reason, "error": stopped.cause.Error()})
				return personalassistant.FolderSetupStopped, stopped.reason
			}
			if err != nil {
				return personalassistant.FolderSetupStopped, personalassistant.FolderStopFailed
			}
			for i := range lines {
				if lines[i].Kind == personalassistant.FolderPlanSongs {
					lines[i].Detail = folderagent.HomeReceiptLine(receipt.listed, receipt.unreached, rec.Facts)
				}
			}
			update(append([]personalassistant.FolderPlanLine(nil), lines...))
			continue
		case folderlook.KindFileCurator:
			if err := b.addFolderAgentFileCurator(rec.WorkspaceID, set); err != nil {
				return stop(folderagent.FolderPlanCapability, personalassistant.FolderStopFailed, err)
			}
			continue
		case folderlook.KindIntegration:
		default:
			return stop(personalassistant.FolderPlanWorkspace, personalassistant.FolderStopPlanChanged, errors.New("candidate kind not runnable: "+candidate.Kind))
		}
		entry, ok := reviewedintegration.Get(candidate.IntegrationKey)
		if !ok || b.folderSetupHost == nil {
			return stop(personalassistant.FolderPlanIntegration, personalassistant.FolderStopInstallFailed, errors.New("unknown integration"))
		}
		step := folderAgentIntegration{
			words: integrationWords{plugin: entry.PluginName(), tools: entry.DisplayName, workspace: rec.WorkspaceName},
			facts: func(ctx context.Context) (foldersetup.Plugin, error) {
				return b.folderSetupHost.integrationFacts(ctx, rec.OwnerUserID, entry)
			},
			install: func(ctx context.Context) (foldersetup.Journey, error) {
				if b.setupJourneyService == nil {
					return nil, errors.New("no setup journey service")
				}
				install, err := b.setupJourneyService.ForHostQuest(ctx, rec.OwnerUserID, entry.InstallQuestID())
				if err != nil {
					return nil, err
				}
				return scopedJourney{service: install, userID: rec.OwnerUserID}, nil
			},
			connect: func() error {
				if b.pluginHandler == nil {
					return errors.New("no plugin manager")
				}
				result, err := pluginworkspace.New(b.pluginHandler.Manager(), b.workspaceStore).Reconcile(pluginworkspace.Request{
					WorkspaceID: rec.WorkspaceID, Plugins: []string{entry.PluginID},
				})
				if err == nil && result.SaveErr != nil {
					err = result.SaveErr
				}
				return err
			},
		}
		if kind, reason, err := runFolderAgentIntegration(ctx, step, set); err != nil {
			return stop(kind, reason, err)
		}
	}
	set(personalassistant.FolderPlanFolder, personalassistant.FolderLineDone, "")
	return personalassistant.FolderSetupDone, ""
}

// folderAgentIntegration is what one reviewed integration's step needs from
// the host: its words, its install facts, its install quest and the connect.
type folderAgentIntegration struct {
	words   integrationWords
	facts   func(context.Context) (foldersetup.Plugin, error)
	install func(context.Context) (foldersetup.Journey, error)
	connect func() error
}

// runFolderAgentIntegration installs the integration when the facts say it
// is not ready, then connects its tools to the workspace. Each boundary moves
// its line with live words; done lines carry the receipt words. A failure
// returns the line kind that failed and the stop reason.
func runFolderAgentIntegration(ctx context.Context, step folderAgentIntegration, set func(kind, state, progress string)) (string, string, error) {
	words := step.words
	set(personalassistant.FolderPlanIntegration, personalassistant.FolderLineWorking, words.phase(foldersetup.IntegrationChecking))
	facts, err := step.facts(ctx)
	if err != nil {
		return personalassistant.FolderPlanIntegration, personalassistant.FolderStopInstallFailed, err
	}
	words.version = facts.Version
	installed := ""
	if facts.State != personalassistant.FolderInstallReady {
		install, err := step.install(ctx)
		if err != nil {
			return personalassistant.FolderPlanIntegration, personalassistant.FolderStopInstallFailed, err
		}
		if err := foldersetup.InstallReviewedIntegration(ctx, install, facts.State, facts.PluginID, facts.Version,
			func(phase foldersetup.IntegrationPhase) {
				set(personalassistant.FolderPlanIntegration, personalassistant.FolderLineWorking, words.phase(phase))
			}); err != nil {
			reason := foldersetup.StopReason(err)
			if reason == "" {
				reason = personalassistant.FolderStopInstallFailed
			}
			return personalassistant.FolderPlanIntegration, reason, err
		}
		installed = words.installed()
	}
	set(personalassistant.FolderPlanIntegration, personalassistant.FolderLineDone, installed)
	set(personalassistant.FolderPlanWorkspace, personalassistant.FolderLineWorking, words.enabling())
	if err := step.connect(); err != nil {
		return personalassistant.FolderPlanWorkspace, personalassistant.FolderStopFailed, err
	}
	set(personalassistant.FolderPlanWorkspace, personalassistant.FolderLineDone, words.connected())
	return "", "", nil
}

// integrationWords are the live and receipt words of one reviewed
// integration's lines (PRD FR6, FR10). Names come from the reviewed entry and
// the version from the host's facts, never from the plugin's own text.
type integrationWords struct {
	plugin, tools, workspace, version string
}

func (w integrationWords) release() string {
	if w.version == "" {
		return w.plugin
	}
	return w.plugin + " " + w.version
}

func (w integrationWords) phase(phase foldersetup.IntegrationPhase) string {
	switch phase {
	case foldersetup.IntegrationChecking:
		return "Checking the reviewed release"
	case foldersetup.IntegrationDownloading:
		return "Downloading " + w.release()
	case foldersetup.IntegrationVerifying:
		return "Verifying " + w.release()
	case foldersetup.IntegrationEnabling:
		return "Enabling " + w.release()
	}
	return ""
}

func (w integrationWords) enabling() string { return "Enabling it in " + w.workspace }

func (w integrationWords) installed() string { return "Installed " + w.release() }

func (w integrationWords) connected() string {
	return "Connected " + w.tools + " tools to " + w.workspace
}
