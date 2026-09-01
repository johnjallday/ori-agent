package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// homeRecentSessionsAdapter bridges the session store to the home harness's
// recent-sessions reader, keeping agenthttp decoupled from the session package.
type homeRecentSessionsAdapter struct {
	store session.HybridStore
}

func (a homeRecentSessionsAdapter) RecentSessions(ctx context.Context, limit int) ([]agenthttp.HomeSessionSummary, error) {
	if a.store == nil {
		return nil, nil
	}
	res, err := a.store.ListSessions(ctx, nil, &session.ListOptions{Limit: limit, Sort: session.SortByUpdatedDesc})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	out := make([]agenthttp.HomeSessionSummary, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		out = append(out, agenthttp.HomeSessionSummary{
			ID:           s.ID,
			Title:        s.Title,
			AgentName:    s.AgentName,
			MessageCount: s.MessageCount,
			CreatedAt:    s.CreatedAt,
			UpdatedAt:    s.UpdatedAt,
		})
	}
	return out, nil
}

// homeUsageAdapter bridges the cost tracker to the home harness's usage reader.
type homeUsageAdapter struct {
	tracker *llm.CostTracker
}

func (a homeUsageAdapter) UsageSummary() (agenthttp.HomeUsageSummary, bool) {
	if a.tracker == nil {
		return agenthttp.HomeUsageSummary{}, false
	}
	today := a.tracker.GetTodayStats()
	month := a.tracker.GetThisMonthStats()
	currency := today.Currency
	if currency == "" {
		currency = month.Currency
	}
	return agenthttp.HomeUsageSummary{
		TodayCost:   today.TotalCost,
		TodayTokens: today.TotalTokens,
		MonthCost:   month.TotalCost,
		MonthTokens: month.TotalTokens,
		Currency:    currency,
	}, true
}

// homeAgentsAdapter bridges the agent store to the home harness's agent-roster
// reader, keeping agenthttp decoupled from the agent/store packages. Workspace
// usage is cross-referenced in the snapshot/tool, so the roster carries only
// per-agent profile fields.
type homeAgentsAdapter struct {
	agents store.Store
}

func (a homeAgentsAdapter) AgentRoster() ([]agenthttp.HomeAgentSummary, bool) {
	if a.agents == nil {
		return nil, false
	}
	names := a.agents.ListAgents()
	out := make([]agenthttp.HomeAgentSummary, 0, len(names))
	for _, name := range names {
		ag, ok := a.agents.GetAgent(name)
		if !ok || ag == nil {
			continue
		}
		summary := agenthttp.HomeAgentSummary{
			Name:         name,
			Type:         ag.Type,
			Role:         string(ag.Role),
			Model:        ag.Settings.Model,
			Provider:     ag.Settings.Provider,
			Capabilities: ag.Capabilities,
			Status:       string(ag.Status),
		}
		if ag.Metadata != nil {
			summary.Description = ag.Metadata.Description
		}
		out = append(out, summary)
	}
	return out, true
}

// homeActionMutator executes confirmed home actions (PRD 4.6). CreateWorkspace,
// CreateTask, and StartTask are wired; StartTask runs the task through the same
// orchestrator path the workspace UI uses, so coordinator-driven assignment and
// the delegation loop apply identically.
type homeActionMutator struct {
	workspaces     workspace.Store
	agents         store.Store
	orchestrator   *workspace.Orchestrator
	backlogService *workspace.BacklogService
}

func (m homeActionMutator) workspaceHref(workspaceID string) string {
	if m.workspaces == nil {
		return ""
	}
	ws, err := m.workspaces.Get(strings.TrimSpace(workspaceID))
	if err != nil || ws == nil || strings.TrimSpace(ws.FolderSlug) == "" {
		return ""
	}
	return "/workspaces/" + url.PathEscape(ws.FolderSlug)
}

func (m homeActionMutator) defaultAgentName() string {
	if m.agents == nil {
		return ""
	}
	if _, ok := m.agents.GetAgent("Ori"); ok {
		return "Ori"
	}
	if names := m.agents.ListAgents(); len(names) > 0 {
		return names[0]
	}
	return ""
}

func (m homeActionMutator) CreateWorkspace(ctx context.Context, name, description string) (string, string, error) {
	if m.workspaces == nil {
		return "", "", fmt.Errorf("workspace store unavailable")
	}
	agentName := m.defaultAgentName()
	if agentName == "" {
		return "", "", fmt.Errorf("no agent is available to attach to the workspace")
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:        name,
		Description: description,
		Agents:      []string{agentName},
		InitialData: map[string]any{},
	})
	_ = ws.SetEntryAgentName(agentName)
	if err := m.workspaces.Save(ws); err != nil {
		return "", "", err
	}
	return ws.ID, "/workspaces/" + url.PathEscape(ws.FolderSlug), nil
}

func (m homeActionMutator) CreateTask(ctx context.Context, workspaceID, description string) (string, string, error) {
	if m.workspaces == nil {
		return "", "", fmt.Errorf("workspace store unavailable")
	}
	var taskID string
	err := m.workspaces.Update(workspaceID, func(ws *workspace.Workspace) error {
		task := workspace.Task{
			Description: description,
			From:        "Ori",
			Status:      workspace.TaskStatusPending,
			Priority:    3,
		}
		// Default the unassigned task to the workspace coordinator (entry agent)
		// when one can be resolved, so assistant-created work is owned, not
		// orphaned in the Unassigned column.
		ws.ApplyEntryAgentDefault(&task)
		if addErr := ws.AddTask(task); addErr != nil {
			return addErr
		}
		if len(ws.Tasks) > 0 {
			taskID = ws.Tasks[len(ws.Tasks)-1].ID
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return taskID, m.workspaceHref(workspaceID), nil
}

// CreateBacklogItem adds an uncommitted Backlog item through the canonical
// BacklogService (PRD workspace-backlog FR5, 20-25) rather than
// Workspace.Update+ApplyEntryAgentDefault the way CreateTask does above —
// assistant-created backlog items must not be assigned to an agent or become
// runnable (FR25).
func (m homeActionMutator) CreateBacklogItem(ctx context.Context, workspaceID, description string) (string, string, error) {
	href := m.workspaceHref(workspaceID)
	if m.backlogService == nil {
		return "", href, fmt.Errorf("backlog capture is unavailable right now")
	}
	task, err := m.backlogService.Create(workspace.BacklogCreateInput{
		WorkspaceID: workspaceID,
		Description: description,
		SourceType:  workspace.BacklogSourceAssistant,
	})
	if err != nil {
		return "", href, err
	}
	return task.ID, href, nil
}

func (m homeActionMutator) StartTask(ctx context.Context, workspaceID, taskID string) (string, error) {
	href := m.workspaceHref(workspaceID)
	if m.workspaces == nil {
		return href, fmt.Errorf("workspace store unavailable")
	}
	if m.orchestrator == nil {
		return href, fmt.Errorf("task execution is not available right now")
	}

	ws, err := m.workspaces.Get(workspaceID)
	if err != nil {
		return href, fmt.Errorf("workspace not found: %w", err)
	}

	var target *workspace.Task
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == taskID {
			target = &ws.Tasks[i]
			break
		}
	}
	if target == nil {
		return href, fmt.Errorf("task not found in workspace")
	}
	if target.Status == workspace.TaskStatusInProgress {
		return href, fmt.Errorf("task is already running")
	}
	if target.Status == workspace.TaskStatusCompleted {
		return href, fmt.Errorf("task is already completed")
	}

	// Execute asynchronously. The HTTP request that confirmed this action
	// returns immediately, so derive a context that keeps the request's values
	// (tracing, etc.) but is not cancelled when the request ends — otherwise the
	// task would be torn down the moment we respond.
	execCtx := context.Background()
	if ctx != nil {
		execCtx = context.WithoutCancel(ctx)
	}
	task := *target
	orchestrator := m.orchestrator
	go func() {
		if execErr := orchestrator.ExecuteTask(execCtx, workspaceID, task); execErr != nil {
			logger.Error("home assistant: failed to start task", logger.Fields{"workspace_id": workspaceID, "task_id": taskID, "err": execErr})
		}
	}()

	return href, nil
}

func (m homeActionMutator) AssignAgent(ctx context.Context, workspaceID, agentName string) (string, error) {
	href := m.workspaceHref(workspaceID)
	if m.workspaces == nil {
		return href, fmt.Errorf("workspace store unavailable")
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return href, fmt.Errorf("agent name is required")
	}
	// Defense in depth: never add a phantom agent even if the client supplies one.
	if m.agents != nil {
		if _, ok := m.agents.GetAgent(agentName); !ok {
			return href, fmt.Errorf("agent %q does not exist", agentName)
		}
	}
	err := m.workspaces.Update(workspaceID, func(ws *workspace.Workspace) error {
		return ws.AddAgent(agentName)
	})
	if err != nil {
		return href, err
	}
	return href, nil
}

func (m homeActionMutator) CreateAgent(ctx context.Context, name, description string) (string, error) {
	const href = "/agents"
	if m.agents == nil {
		return href, fmt.Errorf("agent store unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return href, fmt.Errorf("agent name is required")
	}
	if _, exists := m.agents.GetAgent(name); exists {
		return href, fmt.Errorf("an agent named %q already exists", name)
	}
	if err := m.agents.CreateAgent(name, &store.CreateAgentConfig{}); err != nil {
		return href, err
	}
	if description = strings.TrimSpace(description); description != "" {
		// The agent already exists at this point; a failed description write is
		// non-fatal (creation succeeded) but worth a warning for debugging.
		if updErr := m.agents.UpdateAgent(name, func(ag *agent.Agent) error {
			if ag.Metadata == nil {
				ag.Metadata = &types.AgentMetadata{}
			}
			ag.Metadata.Description = description
			return nil
		}); updErr != nil {
			logger.Warn("home assistant: failed to set new agent description", logger.Fields{"agent": name, "err": updErr})
		}
	}
	return href, nil
}

func (m homeActionMutator) RemoveAgent(ctx context.Context, workspaceID, agentName string) (string, error) {
	href := m.workspaceHref(workspaceID)
	if m.workspaces == nil {
		return href, fmt.Errorf("workspace store unavailable")
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return href, fmt.Errorf("agent name is required")
	}
	err := m.workspaces.Update(workspaceID, func(ws *workspace.Workspace) error {
		return ws.RemoveAgent(agentName)
	})
	if err != nil {
		return href, err
	}
	return href, nil
}

// newHomeAssistantAskHandler constructs the home harness handler with its data
// sources, model access, and confirmed-action executor.
func (s *Server) newHomeAssistantAskHandler() *agenthttp.HomeAssistantAskHandler {
	sources := agenthttp.HomeSnapshotSources{}
	var llmFactory *llm.Factory
	var systemModel interface {
		GetSystemModel() (provider, model string)
	}
	if s.Storage != nil {
		sources.Workspaces = s.Storage.WorkspaceStore
		if s.Storage.WorkspaceStore != nil {
			sources.Opportunities = workspace.NewOpportunityStore(s.Storage.WorkspaceStore)
		}
		sources.Sessions = homeRecentSessionsAdapter{store: s.Storage.SessionStore}
		if s.Storage.AgentStore != nil {
			sources.Agents = homeAgentsAdapter{agents: s.Storage.AgentStore}
		}
	}
	if s.Core != nil {
		sources.Usage = homeUsageAdapter{tracker: s.Core.CostTracker}
		llmFactory = s.Core.LLMFactory
		if s.Core.ConfigManager != nil {
			systemModel = s.Core.ConfigManager
		}
	}
	handler := agenthttp.NewHomeAssistantAskHandler(sources, llmFactory, systemModel)
	handler.SetTraceEmitter(agenthttp.NewLoggingHomeAskTraceEmitter())
	if s.Storage != nil && s.Storage.PersonalAssistant != nil {
		handler.SetPersonalAssistantContextProvider(personalAssistantContextAdapter{
			relationship: s.Storage.PersonalAssistant,
			profiles:     s.Storage.UserStore,
			workspaces:   s.Storage.WorkspaceStore,
		}, "local")
		if s.Storage.PersonalAssistantMemory != nil {
			handler.SetPersonalAssistantMemoryWriter(s.Storage.PersonalAssistantMemory)
		}
	}

	// Ori Guide reuses the same system model, but only to reword an answer it
	// has already decided. Wired here because this is where the system-model
	// reader is resolved; the guide keeps working unchanged when it is absent.
	if s.Handlers != nil && s.Handlers.OriGuide != nil && llmFactory != nil && systemModel != nil {
		s.Handlers.OriGuide.SetPhraser(agenthttp.NewLLMGuidePhraser(llmFactory, systemModel))
	}
	if s.Storage != nil {
		mutator := homeActionMutator{
			workspaces: s.Storage.WorkspaceStore,
			agents:     s.Storage.AgentStore,
		}
		if s.Storage.WorkspaceStore != nil {
			// A dedicated instance over the same store/event bus/file
			// synchronizer as orchestrationhttp's — BacklogService is a
			// stateless holder of those references, so every capture surface
			// stays hierarchy-safe and BACKLOG.md-consistent without needing
			// a single shared singleton (mirrors orchestrationhttp.Handler's
			// own construction in initializeSubHandlers).
			backlogService := workspace.NewBacklogService(s.Storage.WorkspaceStore)
			backlogService.SetSynchronizer(workspace.NewFileBacklogSynchronizer(s.Storage.WorkspaceStore))
			if s.Workflow != nil {
				backlogService.SetEventBus(s.Workflow.EventBus)
			}
			mutator.backlogService = backlogService
		}
		if s.Workflow != nil {
			mutator.orchestrator = s.Workflow.WorkspaceOrchestrator
		}
		handler.SetMutator(mutator)
	}
	return handler
}
