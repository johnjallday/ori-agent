package agenthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// homeToolRegistry exposes read-only, app-scoped tools the home harness lets the
// model call on demand to read full state beyond the bounded snapshot (PRD 4.3).
// Every tool is read-only; none mutate state. In particular home_opportunities
// uses OpportunityStore.List and never marks opportunities seen.
type homeToolRegistry struct {
	sources HomeSnapshotSources
	now     func() time.Time
}

func newHomeToolRegistry(sources HomeSnapshotSources) *homeToolRegistry {
	now := sources.Now
	if now == nil {
		now = time.Now
	}
	return &homeToolRegistry{sources: sources, now: now}
}

const (
	homeToolDefaultLimit = 50
	homeToolMaxLimit     = 100
)

// Definitions returns the LLM tool definitions for the registered read-only tools.
func (r *homeToolRegistry) Definitions() []llm.Tool {
	return []llm.Tool{
		{
			Name:        "home_workspaces",
			Description: "List the user's workspaces (excludes group containers). Read-only.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{"type": "string", "description": "Optional status filter, e.g. active."},
					"limit":  map[string]any{"type": "integer", "description": "Max results (default 50, max 100)."},
				},
			},
		},
		{
			Name:        "home_tasks",
			Description: "List tasks across all workspaces, optionally filtered by status, workspace, and activity date window. Read-only.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status":       map[string]any{"type": "string", "description": "Optional task status filter (pending, in_progress, completed, failed, etc.)."},
					"workspace_id": map[string]any{"type": "string", "description": "Optional workspace id filter."},
					"date_window":  map[string]any{"type": "string", "enum": []string{"today", "this_week", "this_month"}, "description": "Optional activity window."},
					"limit":        map[string]any{"type": "integer", "description": "Max results (default 50, max 100)."},
				},
			},
		},
		{
			Name:        "home_sessions",
			Description: "List recent chat sessions across the app, most recently updated first. Read-only.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "description": "Max results (default 50, max 100)."},
				},
			},
		},
		{
			Name:        "home_opportunities",
			Description: "List open Action Center opportunities across workspaces. Read-only; does not mark anything as seen.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id": map[string]any{"type": "string", "description": "Optional workspace id filter."},
					"limit":        map[string]any{"type": "integer", "description": "Max results (default 50, max 100)."},
				},
			},
		},
		{
			Name:        "home_usage",
			Description: "Return a cheap usage/cost summary (today and month-to-date). Read-only.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// Has reports whether a tool name belongs to this registry.
func (r *homeToolRegistry) Has(name string) bool {
	switch name {
	case "home_workspaces", "home_tasks", "home_sessions", "home_opportunities", "home_usage":
		return true
	}
	return false
}

// Execute dispatches a tool call and returns a JSON result string.
func (r *homeToolRegistry) Execute(ctx context.Context, name, argsJSON string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	args := parseHomeToolArgs(argsJSON)
	switch name {
	case "home_workspaces":
		return r.workspaces(args)
	case "home_tasks":
		return r.tasks(args)
	case "home_sessions":
		return r.sessions(ctx, args)
	case "home_opportunities":
		return r.opportunities(args)
	case "home_usage":
		return r.usage()
	default:
		return "", fmt.Errorf("unknown home tool %q", name)
	}
}

func parseHomeToolArgs(argsJSON string) map[string]any {
	args := map[string]any{}
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" {
		return args
	}
	_ = json.Unmarshal([]byte(trimmed), &args)
	return args
}

func homeToolString(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func homeToolLimit(args map[string]any) int {
	limit := homeToolDefaultLimit
	if v, ok := args["limit"]; ok {
		switch n := v.(type) {
		case float64:
			limit = int(n)
		case int:
			limit = n
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				limit = parsed
			}
		}
	}
	if limit <= 0 {
		limit = homeToolDefaultLimit
	}
	if limit > homeToolMaxLimit {
		limit = homeToolMaxLimit
	}
	return limit
}

func homeToolJSON(payload any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *homeToolRegistry) workspaces(args map[string]any) (string, error) {
	if r.sources.Workspaces == nil {
		return homeToolJSON(map[string]any{"workspaces": []any{}, "note": "workspace store unavailable"})
	}
	statusFilter := strings.ToLower(homeToolString(args, "status"))
	limit := homeToolLimit(args)
	ids, err := r.sources.Workspaces.List()
	if err != nil {
		return "", err
	}
	type row struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		AgentCount int    `json:"agent_count"`
		OpenTasks  int    `json:"open_tasks"`
		UpdatedAt  string `json:"updated_at"`
	}
	var rows []row
	for _, id := range ids {
		ws, getErr := r.sources.Workspaces.Get(id)
		if getErr != nil || ws == nil || isGroupWorkspace(ws) {
			continue
		}
		if statusFilter != "" && !strings.EqualFold(string(ws.Status), statusFilter) {
			continue
		}
		stats := ws.GetTaskStats()
		rows = append(rows, row{
			ID:         ws.ID,
			Name:       ws.Name,
			Status:     string(ws.Status),
			AgentCount: workspaceAgentCount(ws),
			OpenTasks:  stats["pending"] + stats["assigned"] + stats["in_progress"] + stats["waiting_for_choice"],
			UpdatedAt:  homeSnapshotDate(ws.UpdatedAt),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UpdatedAt > rows[j].UpdatedAt })
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return homeToolJSON(map[string]any{"workspaces": rows, "total": total})
}

func (r *homeToolRegistry) tasks(args map[string]any) (string, error) {
	if r.sources.Workspaces == nil {
		return homeToolJSON(map[string]any{"tasks": []any{}, "note": "workspace store unavailable"})
	}
	statusFilter := strings.ToLower(homeToolString(args, "status"))
	wsFilter := homeToolString(args, "workspace_id")
	limit := homeToolLimit(args)

	var start, end time.Time
	windowed := false
	if w := homeToolString(args, "date_window"); w != "" {
		window := NormalizeHomeDateWindow(w, HomeWindowThisWeek)
		start, end, _ = resolveHomeWindowRange(window, r.now())
		windowed = true
	}

	ids, err := r.sources.Workspaces.List()
	if err != nil {
		return "", err
	}
	type row struct {
		ID            string `json:"id"`
		WorkspaceID   string `json:"workspace_id"`
		WorkspaceName string `json:"workspace_name"`
		Description   string `json:"description"`
		Status        string `json:"status"`
		Priority      int    `json:"priority"`
		Assignee      string `json:"assignee"`
		UpdatedAt     string `json:"updated_at"`
	}
	var rows []row
	for _, id := range ids {
		ws, getErr := r.sources.Workspaces.Get(id)
		if getErr != nil || ws == nil || isGroupWorkspace(ws) {
			continue
		}
		if wsFilter != "" && ws.ID != wsFilter {
			continue
		}
		for _, t := range ws.Tasks {
			if statusFilter != "" && !strings.EqualFold(string(t.Status), statusFilter) {
				continue
			}
			if windowed && !taskActiveInWindow(t, start, end) {
				continue
			}
			rows = append(rows, row{
				ID:            t.ID,
				WorkspaceID:   ws.ID,
				WorkspaceName: ws.Name,
				Description:   homeSnapshotClip(t.Description, homeSnapshotPreviewLimit),
				Status:        string(t.Status),
				Priority:      t.Priority,
				Assignee:      homeTaskAssignee(t),
				UpdatedAt:     homeSnapshotDate(homeTaskActivityTime(t)),
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UpdatedAt > rows[j].UpdatedAt })
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return homeToolJSON(map[string]any{"tasks": rows, "total": total})
}

func (r *homeToolRegistry) sessions(ctx context.Context, args map[string]any) (string, error) {
	if r.sources.Sessions == nil {
		return homeToolJSON(map[string]any{"sessions": []any{}, "note": "session store unavailable"})
	}
	limit := homeToolLimit(args)
	sessions, err := r.sources.Sessions.RecentSessions(ctx, limit)
	if err != nil {
		return "", err
	}
	type row struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		AgentName    string `json:"agent_name"`
		MessageCount int    `json:"message_count"`
		UpdatedAt    string `json:"updated_at"`
	}
	rows := make([]row, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, row{
			ID:           s.ID,
			Title:        s.Title,
			AgentName:    s.AgentName,
			MessageCount: s.MessageCount,
			UpdatedAt:    homeSnapshotDate(s.UpdatedAt),
		})
	}
	return homeToolJSON(map[string]any{"sessions": rows, "total": len(rows)})
}

func (r *homeToolRegistry) opportunities(args map[string]any) (string, error) {
	if r.sources.Workspaces == nil || r.sources.Opportunities == nil {
		return homeToolJSON(map[string]any{"opportunities": []any{}, "note": "opportunity store unavailable"})
	}
	wsFilter := homeToolString(args, "workspace_id")
	limit := homeToolLimit(args)
	ids, err := r.sources.Workspaces.List()
	if err != nil {
		return "", err
	}
	type row struct {
		ID            string `json:"id"`
		WorkspaceID   string `json:"workspace_id"`
		WorkspaceName string `json:"workspace_name"`
		Title         string `json:"title"`
		Summary       string `json:"summary"`
		Priority      string `json:"priority"`
	}
	var rows []row
	for _, id := range ids {
		ws, getErr := r.sources.Workspaces.Get(id)
		if getErr != nil || ws == nil {
			continue
		}
		if wsFilter != "" && ws.ID != wsFilter {
			continue
		}
		// Read-only: List does not mark opportunities seen (unlike Get).
		opps, listErr := r.sources.Opportunities.List(ws.ID)
		if listErr != nil {
			continue
		}
		for _, o := range opps {
			if !o.IsOpen() {
				continue
			}
			rows = append(rows, row{
				ID:            o.ID,
				WorkspaceID:   ws.ID,
				WorkspaceName: ws.Name,
				Title:         o.Title,
				Summary:       homeSnapshotClip(o.Summary, homeSnapshotPreviewLimit),
				Priority:      o.Priority,
			})
		}
	}
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return homeToolJSON(map[string]any{"opportunities": rows, "total": total})
}

func (r *homeToolRegistry) usage() (string, error) {
	if r.sources.Usage == nil {
		return homeToolJSON(map[string]any{"available": false, "note": "usage unavailable"})
	}
	summary, ok := r.sources.Usage.UsageSummary()
	if !ok {
		return homeToolJSON(map[string]any{"available": false})
	}
	return homeToolJSON(map[string]any{
		"available":    true,
		"currency":     emptyTo(summary.Currency, "USD"),
		"today_cost":   summary.TodayCost,
		"today_tokens": summary.TodayTokens,
		"month_cost":   summary.MonthCost,
		"month_tokens": summary.MonthTokens,
	})
}
