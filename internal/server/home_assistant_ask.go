package server

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
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

// homeActionMutator executes confirmed home actions (PRD 4.6). CreateWorkspace
// and CreateTask are wired; StartTask is deferred to a follow-up (kept behind the
// same confirmation contract, returns a clear message for now).
type homeActionMutator struct {
	workspaces workspace.Store
	agents     store.Store
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
	return ws.ID, "/workspaces/" + ws.ID, nil
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
	return taskID, "/workspaces/" + workspaceID, nil
}

func (m homeActionMutator) StartTask(ctx context.Context, workspaceID, taskID string) (string, error) {
	return "/workspaces/" + workspaceID, fmt.Errorf("starting a task from here isn't supported yet — open the workspace to run it")
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
	if s.Storage != nil {
		handler.SetMutator(homeActionMutator{
			workspaces: s.Storage.WorkspaceStore,
			agents:     s.Storage.AgentStore,
		})
	}
	return handler
}
