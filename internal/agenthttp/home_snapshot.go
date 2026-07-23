package agenthttp

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Home snapshot caps (PRD 4.2 / Resolved Decisions). These bound prompt size so a
// large account cannot blow the context window; detail beyond the caps is fetched
// on demand via the read-only home tools.
const (
	homeSnapshotMaxWorkspaces    = 25
	homeSnapshotMaxTasks         = 40
	homeSnapshotMaxSessions      = 20
	homeSnapshotMaxOpportunities = 20
	homeSnapshotMaxAgents        = 30
	homeSnapshotPreviewLimit     = 180
	homeSnapshotTextLimit        = 120
)

// HomeDateWindow is the activity window a snapshot is scoped to.
type HomeDateWindow string

const (
	HomeWindowToday     HomeDateWindow = "today"
	HomeWindowThisWeek  HomeDateWindow = "this_week"
	HomeWindowThisMonth HomeDateWindow = "this_month"
)

// HomeSessionSummary is a workspace-agnostic recent-session record. Sessions in
// this app are agent/folder scoped, so workspace attribution is omitted (FR #5
// "when available").
type HomeSessionSummary struct {
	ID           string
	Title        string
	AgentName    string
	MessageCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// HomeUsageSummary is a cheap usage/cost roll-up.
type HomeUsageSummary struct {
	TodayCost   float64
	TodayTokens int
	MonthCost   float64
	MonthTokens int
	Currency    string
}

// homeRecentSessionsReader returns recent sessions across the whole app. The
// server wires a session-store adapter so agenthttp stays decoupled from the
// session package.
type homeRecentSessionsReader interface {
	RecentSessions(ctx context.Context, limit int) ([]HomeSessionSummary, error)
}

// homeUsageReader returns a cheap usage summary. The server wires a costTracker
// adapter. A false ok degrades the usage section rather than failing the answer.
type homeUsageReader interface {
	UsageSummary() (HomeUsageSummary, bool)
}

// homeAgentsReader returns the app-wide agent roster. The server wires an
// agent-store adapter so agenthttp stays decoupled from the agent/store
// packages. A false ok degrades the agents section rather than failing the
// answer. Workspace usage is cross-referenced from the workspace list, not the
// reader, so the roster carries only per-agent profile fields.
type homeAgentsReader interface {
	AgentRoster() ([]HomeAgentSummary, bool)
}

// HomeSnapshotSources bundles the data sources the snapshot aggregates. Any may
// be nil; a nil source degrades its section instead of failing the snapshot.
type HomeSnapshotSources struct {
	Workspaces    workspace.Store
	Opportunities workspace.OpportunityStore
	Sessions      homeRecentSessionsReader
	Usage         homeUsageReader
	Agents        homeAgentsReader
	// Now allows tests to pin the clock; defaults to time.Now.
	Now func() time.Time
}

// HomeWorkspaceSummary is a per-workspace roll-up line.
type HomeWorkspaceSummary struct {
	ID         string
	Name       string
	Status     string
	AgentCount int
	OpenTasks  int
	UpdatedAt  time.Time
}

// HomeTaskSummary is a single task active within the snapshot window.
type HomeTaskSummary struct {
	ID            string
	WorkspaceID   string
	WorkspaceName string
	Description   string
	Status        string
	Priority      int
	Assignee      string
	UpdatedAt     time.Time
}

// HomeAgentSummary is a single agent profile in the app-wide roster, plus the
// count and names of workspaces that use it (cross-referenced from the workspace
// list, so the home assistant can answer "which agents do I have and where are
// they used").
type HomeAgentSummary struct {
	Name           string
	Type           string
	Role           string
	Model          string
	Provider       string
	Description    string
	Capabilities   []string
	Status         string
	WorkspaceCount int
	Workspaces     []string
}

// HomeOpportunitySummary is a single open Action Center opportunity.
type HomeOpportunitySummary struct {
	ID            string
	WorkspaceID   string
	WorkspaceName string
	Title         string
	Summary       string
	Priority      string
}

// HomeSnapshotMeta records section sizes, truncation, and which sections degraded
// (a data source failed or was unavailable). Returned to the frontend as
// snapshot_meta and folded into the prompt.
type HomeSnapshotMeta struct {
	Window           HomeDateWindow `json:"window"`
	WindowLabel      string         `json:"window_label"`
	GeneratedAt      time.Time      `json:"generated_at"`
	WorkspaceCount   int            `json:"workspace_count"`
	TaskCount        int            `json:"task_count"`
	SessionCount     int            `json:"session_count"`
	OpportunityCount int            `json:"opportunity_count"`
	AgentCount       int            `json:"agent_count"`
	Truncated        []string       `json:"truncated,omitempty"`
	Degraded         []string       `json:"degraded,omitempty"`
}

// HomeSnapshot is the app-scoped analogue of the workspace task snapshot.
type HomeSnapshot struct {
	Meta          HomeSnapshotMeta
	Workspaces    []HomeWorkspaceSummary
	TaskCounts    map[string]int
	Tasks         []HomeTaskSummary
	Sessions      []HomeSessionSummary
	Opportunities []HomeOpportunitySummary
	Agents        []HomeAgentSummary
	Usage         *HomeUsageSummary
}

// NormalizeHomeDateWindow maps free-text/explicit window inputs to a canonical
// window. An empty/unknown value resolves to defaultWindow (PRD Resolved
// Decision #2: recap asks default this_week, status asks default today — the
// caller picks the default from prompt phrasing).
func NormalizeHomeDateWindow(raw string, defaultWindow HomeDateWindow) HomeDateWindow {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), " ", "_") {
	case string(HomeWindowToday), "day", "1d":
		return HomeWindowToday
	case string(HomeWindowThisWeek), "week", "7d":
		return HomeWindowThisWeek
	case string(HomeWindowThisMonth), "month", "30d":
		return HomeWindowThisMonth
	default:
		if defaultWindow == "" {
			return HomeWindowThisWeek
		}
		return defaultWindow
	}
}

// DefaultHomeDateWindowForPrompt picks the default window from prompt phrasing:
// status-style asks scope to today, everything else (recap/summary) to this week.
func DefaultHomeDateWindowForPrompt(prompt string) HomeDateWindow {
	p := strings.ToLower(prompt)
	for _, marker := range []string{"right now", "currently", "pending right now", "what's running", "whats running", "in progress now"} {
		if strings.Contains(p, marker) {
			return HomeWindowToday
		}
	}
	return HomeWindowThisWeek
}

func resolveHomeWindowRange(window HomeDateWindow, now time.Time) (start, end time.Time, label string) {
	end = now
	switch window {
	case HomeWindowToday:
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		label = "today (" + start.Format("Jan 2") + ")"
	case HomeWindowThisMonth:
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		label = "this month (" + start.Format("Jan 2") + " – " + now.Format("Jan 2") + ")"
	default: // this_week, Monday-based
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7 // treat Sunday as last day of the week
		}
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		start = dayStart.AddDate(0, 0, -(weekday - 1))
		label = "this week (" + start.Format("Jan 2") + " – " + now.Format("Jan 2") + ")"
	}
	return start, end, label
}

func taskActiveInWindow(t workspace.Task, start, end time.Time) bool {
	// An uncommitted Backlog capture is not "activity" in this window sense —
	// exclude it up front so the CreatedAt check below (which has no status
	// filter of its own) can't sweep it into "what's happening today."
	if t.Status == workspace.TaskStatusBacklog {
		return false
	}
	inRange := func(ts time.Time) bool {
		return !ts.IsZero() && !ts.Before(start) && !ts.After(end)
	}
	if inRange(t.CreatedAt) {
		return true
	}
	if t.StartedAt != nil && inRange(*t.StartedAt) {
		return true
	}
	if t.CompletedAt != nil && inRange(*t.CompletedAt) {
		return true
	}
	// Always surface still-open tasks regardless of timestamps so "what's
	// pending" stays correct even for older items.
	switch t.Status {
	case workspace.TaskStatusPending, workspace.TaskStatusAssigned,
		workspace.TaskStatusInProgress, workspace.TaskStatusWaitingForChoice:
		return true
	}
	return false
}

// BuildHomeSnapshot aggregates app-wide state for the home harness. It fails
// soft: a failure or nil source degrades that section (recorded in Meta.Degraded)
// rather than returning an error.
func BuildHomeSnapshot(ctx context.Context, sources HomeSnapshotSources, window HomeDateWindow) HomeSnapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	nowFn := sources.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	start, end, label := resolveHomeWindowRange(window, now)

	snap := HomeSnapshot{
		Meta: HomeSnapshotMeta{
			Window:      window,
			WindowLabel: label,
			GeneratedAt: now,
		},
		TaskCounts: map[string]int{},
	}

	wsByID := map[string]string{}            // id -> name, for cross-section labeling
	agentWorkspaces := map[string][]string{} // agent name -> workspace names that use it

	// Workspaces + tasks.
	if sources.Workspaces == nil {
		snap.Meta.Degraded = append(snap.Meta.Degraded, "workspaces")
	} else {
		ids, err := sources.Workspaces.List()
		if err != nil {
			logger.Debug("home snapshot: list workspaces failed", logger.Fields{"error": err})
			snap.Meta.Degraded = append(snap.Meta.Degraded, "workspaces")
		} else {
			var workspaces []*workspace.Workspace
			for _, id := range ids {
				ws, getErr := sources.Workspaces.Get(id)
				if getErr != nil || ws == nil {
					continue
				}
				if isGroupWorkspace(ws) {
					continue // groups are containers, not activity sources
				}
				workspaces = append(workspaces, ws)
			}
			// Most recently updated first.
			sort.Slice(workspaces, func(i, j int) bool {
				return workspaces[i].UpdatedAt.After(workspaces[j].UpdatedAt)
			})
			snap.Meta.WorkspaceCount = len(workspaces)

			var windowedTasks []HomeTaskSummary
			for _, ws := range workspaces {
				wsByID[ws.ID] = ws.Name
				for _, agentName := range workspaceAgentNames(ws) {
					agentWorkspaces[agentName] = append(agentWorkspaces[agentName], ws.Name)
				}
				stats := ws.GetTaskStats()
				for k, v := range stats {
					snap.TaskCounts[k] += v
				}
				if len(snap.Workspaces) < homeSnapshotMaxWorkspaces {
					snap.Workspaces = append(snap.Workspaces, HomeWorkspaceSummary{
						ID:         ws.ID,
						Name:       ws.Name,
						Status:     string(ws.Status),
						AgentCount: workspaceAgentCount(ws),
						OpenTasks:  stats["pending"] + stats["assigned"] + stats["in_progress"] + stats["waiting_for_choice"],
						UpdatedAt:  ws.UpdatedAt,
					})
				}
				for _, t := range ws.Tasks {
					if !taskActiveInWindow(t, start, end) {
						continue
					}
					windowedTasks = append(windowedTasks, HomeTaskSummary{
						ID:            t.ID,
						WorkspaceID:   ws.ID,
						WorkspaceName: ws.Name,
						Description:   t.Description,
						Status:        string(t.Status),
						Priority:      t.Priority,
						Assignee:      homeTaskAssignee(t),
						UpdatedAt:     homeTaskActivityTime(t),
					})
				}
			}
			if len(snap.Workspaces) < snap.Meta.WorkspaceCount {
				snap.Meta.Truncated = append(snap.Meta.Truncated, "workspaces")
			}

			sort.Slice(windowedTasks, func(i, j int) bool {
				return windowedTasks[i].UpdatedAt.After(windowedTasks[j].UpdatedAt)
			})
			snap.Meta.TaskCount = len(windowedTasks)
			if len(windowedTasks) > homeSnapshotMaxTasks {
				windowedTasks = windowedTasks[:homeSnapshotMaxTasks]
				snap.Meta.Truncated = append(snap.Meta.Truncated, "tasks")
			}
			snap.Tasks = windowedTasks

			// Opportunities (needs the workspace list to iterate).
			snap.Opportunities, snap.Meta.OpportunityCount = collectHomeOpportunities(sources.Opportunities, workspaces, &snap.Meta)
		}
	}

	// Sessions (global recent).
	if sources.Sessions == nil {
		snap.Meta.Degraded = append(snap.Meta.Degraded, "sessions")
	} else if sessions, err := sources.Sessions.RecentSessions(ctx, homeSnapshotMaxSessions); err != nil {
		logger.Debug("home snapshot: recent sessions failed", logger.Fields{"error": err})
		snap.Meta.Degraded = append(snap.Meta.Degraded, "sessions")
	} else {
		snap.Sessions = sessions
		snap.Meta.SessionCount = len(sessions)
	}

	// Usage.
	if sources.Usage == nil {
		snap.Meta.Degraded = append(snap.Meta.Degraded, "usage")
	} else if usage, ok := sources.Usage.UsageSummary(); ok {
		snap.Usage = &usage
	} else {
		snap.Meta.Degraded = append(snap.Meta.Degraded, "usage")
	}

	// Agents (roster + workspace usage cross-referenced from the workspace loop).
	snap.Agents, snap.Meta.AgentCount = collectHomeAgents(sources.Agents, agentWorkspaces, &snap.Meta)

	return snap
}

// collectHomeAgents reads the agent roster, fills in each agent's workspace usage
// from the cross-reference map, and applies the snapshot cap. A nil/failed reader
// degrades the agents section.
func collectHomeAgents(reader homeAgentsReader, agentWorkspaces map[string][]string, meta *HomeSnapshotMeta) ([]HomeAgentSummary, int) {
	if reader == nil {
		meta.Degraded = append(meta.Degraded, "agents")
		return nil, 0
	}
	roster, ok := reader.AgentRoster()
	if !ok {
		meta.Degraded = append(meta.Degraded, "agents")
		return nil, 0
	}
	for i := range roster {
		names := dedupeSortedStrings(agentWorkspaces[roster[i].Name])
		roster[i].Workspaces = names
		roster[i].WorkspaceCount = len(names)
	}
	// Most-used agents first, then alphabetical for stability.
	sort.Slice(roster, func(i, j int) bool {
		if roster[i].WorkspaceCount != roster[j].WorkspaceCount {
			return roster[i].WorkspaceCount > roster[j].WorkspaceCount
		}
		return roster[i].Name < roster[j].Name
	})
	total := len(roster)
	if len(roster) > homeSnapshotMaxAgents {
		roster = roster[:homeSnapshotMaxAgents]
		meta.Truncated = append(meta.Truncated, "agents")
	}
	return roster, total
}

// workspaceAgentNames returns the distinct agent profile names a workspace uses,
// preferring stable AgentInstances and falling back to the deprecated Agents
// slice. Field access mirrors the lock-free reads elsewhere in this snapshot.
func workspaceAgentNames(ws *workspace.Workspace) []string {
	seen := map[string]struct{}{}
	var names []string
	add := func(raw string) {
		name := strings.TrimSpace(raw)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, inst := range ws.AgentInstances {
		add(inst.Name)
	}
	return names
}

// dedupeSortedStrings returns the unique, sorted set of non-empty strings.
func dedupeSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func collectHomeOpportunities(store workspace.OpportunityStore, workspaces []*workspace.Workspace, meta *HomeSnapshotMeta) ([]HomeOpportunitySummary, int) {
	if store == nil {
		meta.Degraded = append(meta.Degraded, "opportunities")
		return nil, 0
	}
	var out []HomeOpportunitySummary
	total := 0
	failed := false
	for _, ws := range workspaces {
		opps, err := store.List(ws.ID)
		if err != nil {
			failed = true
			continue
		}
		for _, o := range opps {
			if !o.IsOpen() {
				continue
			}
			total++
			if len(out) < homeSnapshotMaxOpportunities {
				out = append(out, HomeOpportunitySummary{
					ID:            o.ID,
					WorkspaceID:   ws.ID,
					WorkspaceName: ws.Name,
					Title:         o.Title,
					Summary:       o.Summary,
					Priority:      o.Priority,
				})
			}
		}
	}
	if failed && total == 0 {
		meta.Degraded = append(meta.Degraded, "opportunities")
	}
	if total > len(out) {
		meta.Truncated = append(meta.Truncated, "opportunities")
	}
	return out, total
}

func isGroupWorkspace(ws *workspace.Workspace) bool {
	return strings.EqualFold(strings.TrimSpace(ws.Kind), "group")
}

func workspaceAgentCount(ws *workspace.Workspace) int {
	return len(ws.AgentNames())
}

func homeTaskAssignee(t workspace.Task) string {
	if a := strings.TrimSpace(t.To); a != "" {
		return a
	}
	if a := strings.TrimSpace(t.AssignedNodeID); a != "" {
		return a
	}
	return "unassigned"
}

func homeTaskActivityTime(t workspace.Task) time.Time {
	if t.CompletedAt != nil && !t.CompletedAt.IsZero() {
		return *t.CompletedAt
	}
	if t.StartedAt != nil && !t.StartedAt.IsZero() {
		return *t.StartedAt
	}
	return t.CreatedAt
}

// PromptText renders the snapshot for injection into the model prompt, mirroring
// buildTaskWorkspaceSnapshot's bounded markdown style.
func (s HomeSnapshot) PromptText() string {
	var b strings.Builder
	b.WriteString("## Home Snapshot\n\n")
	b.WriteString("App-wide state for the current user. Window: " + s.Meta.WindowLabel + ".\n")
	b.WriteString("Generated at " + s.Meta.GeneratedAt.UTC().Format(time.RFC3339) + ".\n")
	if len(s.Meta.Degraded) > 0 {
		b.WriteString("Degraded sections (data unavailable): " + strings.Join(s.Meta.Degraded, ", ") + ".\n")
	}
	if len(s.Meta.Truncated) > 0 {
		b.WriteString("Truncated sections (more available via tools): " + strings.Join(s.Meta.Truncated, ", ") + ".\n")
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "### Workspaces (%d)\n\n", s.Meta.WorkspaceCount)
	if len(s.Workspaces) == 0 {
		b.WriteString("- none\n")
	}
	for _, ws := range s.Workspaces {
		fmt.Fprintf(&b, "- %q [%s] agents=%d open_tasks=%d updated=%s (id=%s)\n",
			homeSnapshotClip(ws.Name, homeSnapshotTextLimit), ws.Status, ws.AgentCount, ws.OpenTasks,
			homeSnapshotDate(ws.UpdatedAt), ws.ID)
	}

	fmt.Fprintf(&b, "\n### Agents (%d)\n\n", s.Meta.AgentCount)
	if len(s.Agents) == 0 {
		if slices.Contains(s.Meta.Degraded, "agents") {
			b.WriteString("- unavailable\n")
		} else {
			b.WriteString("- none\n")
		}
	}
	for _, a := range s.Agents {
		fmt.Fprintf(&b, "- %q type=%s role=%s model=%s/%s used_in=%d workspace(s)%s%s\n",
			homeSnapshotClip(a.Name, homeSnapshotTextLimit), emptyTo(a.Type, "n/a"), emptyTo(a.Role, "n/a"),
			emptyTo(a.Provider, "n/a"), emptyTo(a.Model, "n/a"), a.WorkspaceCount,
			homeAgentWorkspacesText(a.Workspaces), homeAgentDescriptionText(a.Description))
	}

	b.WriteString("\n### Task activity\n\n")
	b.WriteString("- Counts (all-time): " + homeTaskCountsLine(s.TaskCounts) + "\n")
	fmt.Fprintf(&b, "- Tasks active in window: %d\n", s.Meta.TaskCount)
	for _, t := range s.Tasks {
		fmt.Fprintf(&b, "- [%s] %q in %q -> %s (priority %d, %s) (id=%s)\n",
			t.Status, homeSnapshotClip(t.Description, homeSnapshotTextLimit),
			homeSnapshotClip(t.WorkspaceName, homeSnapshotTextLimit), t.Assignee, t.Priority,
			homeSnapshotDate(t.UpdatedAt), t.ID)
	}

	fmt.Fprintf(&b, "\n### Recent sessions (%d)\n\n", s.Meta.SessionCount)
	if len(s.Sessions) == 0 {
		b.WriteString("- none\n")
	}
	for _, sess := range s.Sessions {
		fmt.Fprintf(&b, "- %q agent=%q messages=%d updated=%s (id=%s)\n",
			homeSnapshotClip(sess.Title, homeSnapshotTextLimit), homeSnapshotClip(sess.AgentName, homeSnapshotTextLimit),
			sess.MessageCount, homeSnapshotDate(sess.UpdatedAt), sess.ID)
	}

	fmt.Fprintf(&b, "\n### Open Action Center opportunities (%d)\n\n", s.Meta.OpportunityCount)
	if len(s.Opportunities) == 0 {
		b.WriteString("- none\n")
	}
	for _, o := range s.Opportunities {
		fmt.Fprintf(&b, "- [%s] %q in %q: %s (id=%s ws=%s)\n",
			emptyTo(o.Priority, "n/a"), homeSnapshotClip(o.Title, homeSnapshotTextLimit),
			homeSnapshotClip(o.WorkspaceName, homeSnapshotTextLimit),
			homeSnapshotClip(o.Summary, homeSnapshotPreviewLimit), o.ID, o.WorkspaceID)
	}

	b.WriteString("\n### Usage\n\n")
	if s.Usage == nil {
		b.WriteString("- unavailable\n")
	} else {
		cur := emptyTo(s.Usage.Currency, "USD")
		fmt.Fprintf(&b, "- Today: %.4f %s (%d tokens)\n", s.Usage.TodayCost, cur, s.Usage.TodayTokens)
		fmt.Fprintf(&b, "- Month-to-date: %.4f %s (%d tokens)\n", s.Usage.MonthCost, cur, s.Usage.MonthTokens)
	}

	b.WriteString("\nUse this snapshot as the source of truth for app-data questions. Call the home_* tools to read full state when a section is truncated or you need detail. Do not fabricate activity; if a section is empty or degraded, say so.\n")
	return b.String()
}

func homeAgentWorkspacesText(workspaces []string) string {
	if len(workspaces) == 0 {
		return ""
	}
	const maxNamed = 5
	named := workspaces
	suffix := ""
	if len(named) > maxNamed {
		named = named[:maxNamed]
		suffix = ", …"
	}
	clipped := make([]string, 0, len(named))
	for _, name := range named {
		clipped = append(clipped, homeSnapshotClip(name, homeSnapshotTextLimit))
	}
	return " [" + strings.Join(clipped, ", ") + suffix + "]"
}

func homeAgentDescriptionText(description string) string {
	clipped := homeSnapshotClip(description, homeSnapshotPreviewLimit)
	if clipped == "" {
		return ""
	}
	return " — " + clipped
}

func homeTaskCountsLine(stats map[string]int) string {
	if len(stats) == 0 {
		return "total=0"
	}
	parts := []string{fmt.Sprintf("total=%d", stats["total"])}
	for _, key := range []string{"pending", "assigned", "in_progress", "waiting_for_choice", "completed", "failed", "cancelled", "timeout", "scheduled"} {
		if c := stats[key]; c > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", key, c))
		}
	}
	return strings.Join(parts, ", ")
}

func homeSnapshotDate(t time.Time) string {
	if t.IsZero() {
		return "n/a"
	}
	return t.UTC().Format("2006-01-02")
}

func homeSnapshotClip(value string, maxLen int) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if maxLen > 3 && len(cleaned) > maxLen {
		return cleaned[:maxLen-3] + "..."
	}
	return cleaned
}

func emptyTo(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
