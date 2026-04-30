package chathttp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type workspaceSnapshotWorkspaceStore interface {
	Get(id string) (*workspace.Workspace, error)
}

type workspaceSnapshotSessionStore interface {
	ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]session.WorkspaceNoteListItem, error)
	ListSessions(ctx context.Context, filter *session.SessionFilter, opts *session.ListOptions) (*session.ListResult, error)
}

const (
	workspaceSnapshotMaxTasks       = 5
	workspaceSnapshotMaxNotes       = 5
	workspaceSnapshotMaxFiles       = 5
	workspaceSnapshotMaxDirectories = 5
	workspaceSnapshotMaxSessions    = 5
	workspaceSnapshotTextLimit      = 120
	workspaceSnapshotPreviewLimit   = 160
	workspaceSnapshotPathLimit      = 180
)

func (h *Handler) buildRuntimeSystemPrompt(ctx context.Context, routeCtx normalizedChatRouteContext) string {
	return h.buildRuntimeSystemPromptForToolCapability(ctx, routeCtx, true)
}

func (h *Handler) buildRuntimeSystemPromptForToolCapability(ctx context.Context, routeCtx normalizedChatRouteContext, toolCallable bool) string {
	return buildWorkspaceRuntimeSystemPromptForToolCapability(ctx, routeCtx, h.workspaceStore, h.sessionStore, toolCallable)
}

func buildWorkspaceRuntimeSystemPrompt(
	ctx context.Context,
	routeCtx normalizedChatRouteContext,
	workspaceStore workspaceSnapshotWorkspaceStore,
	sessionStore workspaceSnapshotSessionStore,
) string {
	return buildWorkspaceRuntimeSystemPromptForToolCapability(ctx, routeCtx, workspaceStore, sessionStore, true)
}

func buildWorkspaceRuntimeSystemPromptForToolCapability(
	ctx context.Context,
	routeCtx normalizedChatRouteContext,
	workspaceStore workspaceSnapshotWorkspaceStore,
	sessionStore workspaceSnapshotSessionStore,
	toolCallable bool,
) string {
	routePrompt := buildRouteContextSystemPrompt(routeCtx)
	workspacePrompt := buildWorkspaceSnapshotPromptForToolCapability(ctx, routeCtx, workspaceStore, sessionStore, toolCallable)
	if workspacePrompt == "" {
		return routePrompt
	}
	if strings.TrimSpace(routePrompt) == "" {
		return workspacePrompt
	}
	return routePrompt + "\n\n---\n" + workspacePrompt
}

func buildWorkspaceSnapshotPrompt(
	ctx context.Context,
	routeCtx normalizedChatRouteContext,
	workspaceStore workspaceSnapshotWorkspaceStore,
	sessionStore workspaceSnapshotSessionStore,
) string {
	return buildWorkspaceSnapshotPromptForToolCapability(ctx, routeCtx, workspaceStore, sessionStore, true)
}

func buildWorkspaceSnapshotPromptForToolCapability(
	ctx context.Context,
	routeCtx normalizedChatRouteContext,
	workspaceStore workspaceSnapshotWorkspaceStore,
	sessionStore workspaceSnapshotSessionStore,
	toolCallable bool,
) string {
	if !shouldAttachWorkspaceSnapshot(routeCtx) || workspaceStore == nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ws, err := workspaceStore.Get(routeCtx.WorkspaceID)
	if err != nil || ws == nil {
		logger.Debug("Skipping workspace snapshot for chat runtime prompt", logger.Fields{
			"workspace_id": routeCtx.WorkspaceID,
			"error":        err,
		})
		return ""
	}

	var notes []session.WorkspaceNoteListItem
	if sessionStore != nil {
		notes, err = sessionStore.ListNotesByWorkspace(ctx, routeCtx.WorkspaceID)
		if err != nil {
			logger.Debug("Failed to load workspace notes for runtime prompt", logger.Fields{
				"workspace_id": routeCtx.WorkspaceID,
				"error":        err,
			})
		}
	}

	var sessions []session.SessionListItem
	sessionCount := 0
	if sessionStore != nil {
		filter := &session.SessionFilter{FolderID: &routeCtx.WorkspaceID}
		opts := &session.ListOptions{
			Limit: workspaceSnapshotMaxSessions,
			Sort:  session.SortByUpdatedDesc,
		}
		result, listErr := sessionStore.ListSessions(ctx, filter, opts)
		if listErr != nil {
			logger.Debug("Failed to load workspace sessions for runtime prompt", logger.Fields{
				"workspace_id": routeCtx.WorkspaceID,
				"error":        listErr,
			})
		} else if result != nil {
			sessions = result.Sessions
			sessionCount = result.Total
		}
	}

	lines := []string{
		"# Workspace Context",
		"You are operating within a workspace. Stay focused on this workspace scope: its tasks, notes, files, and agents.",
		"When specialist help might be useful, inspect available workspace agents first and ask the user before delegating.",
		"Do not behave like a generic global assistant — keep responses relevant to this workspace.",
		"",
		"## Workspace Snapshot",
		"Use this compact snapshot as the current workspace fact set.",
		"",
		"Workspace:",
		fmt.Sprintf("- ID: %q", sanitizeWorkspaceSnapshotText(ws.ID, workspaceSnapshotTextLimit)),
		fmt.Sprintf("- Name: %q", sanitizeWorkspaceSnapshotText(ws.Name, workspaceSnapshotTextLimit)),
	}

	if description := sanitizeWorkspaceSnapshotText(ws.Description, workspaceSnapshotPreviewLimit); description != "" {
		lines = append(lines, fmt.Sprintf("- Description: %q", description))
	}

	lines = append(lines,
		fmt.Sprintf("- Status: %q", sanitizeWorkspaceSnapshotText(string(ws.Status), workspaceSnapshotTextLimit)),
		fmt.Sprintf("- Updated at: %q", formatWorkspaceSnapshotTime(ws.UpdatedAt)),
	)

	agentSummary := buildWorkspaceAgentSummary(ws)
	lines = append(lines, fmt.Sprintf("- Agents (%d): %s", agentSummary.Count, agentSummary.Label))

	stats := ws.GetTaskStats()
	lines = append(lines, "", "Tasks:")
	lines = append(lines, fmt.Sprintf("- Counts: %s", buildWorkspaceTaskCountsSummary(stats)))
	for _, task := range collectWorkspaceOpenTasks(ws.Tasks, workspaceSnapshotMaxTasks) {
		lines = append(lines, fmt.Sprintf(
			"- Open task: [%s] %q -> %q (priority %d)",
			sanitizeWorkspaceSnapshotText(string(task.Status), workspaceSnapshotTextLimit),
			sanitizeWorkspaceSnapshotText(task.Description, workspaceSnapshotTextLimit),
			sanitizeWorkspaceTaskAssignee(task),
			task.Priority,
		))
	}

	if len(notes) > 0 {
		lines = append(lines, "", fmt.Sprintf("Notes (%d):", len(notes)))
		for _, note := range limitWorkspaceNotes(notes, workspaceSnapshotMaxNotes) {
			lines = append(lines, fmt.Sprintf(
				"- Note: id=%q name=%q preview=%q",
				sanitizeWorkspaceSnapshotText(note.ID, workspaceSnapshotTextLimit),
				sanitizeWorkspaceSnapshotText(note.Name, workspaceSnapshotTextLimit),
				sanitizeWorkspaceSnapshotText(note.Preview, workspaceSnapshotPreviewLimit),
			))
		}
	}

	files := collectWorkspaceFiles(ws.Attachments, workspaceSnapshotMaxFiles)
	if len(files) > 0 {
		lines = append(lines, "", fmt.Sprintf("Files (%d):", countWorkspaceFiles(ws.Attachments)))
		for _, file := range files {
			lines = append(lines, fmt.Sprintf(
				"- File: %q (type=%q, mime=%q)",
				workspaceSnapshotAttachmentName(file),
				sanitizeWorkspaceSnapshotText(string(file.Type), workspaceSnapshotTextLimit),
				workspaceSnapshotAttachmentMIME(file),
			))
		}
	}

	directories := limitWorkspaceDirectories(ws.DirectoryReferences, workspaceSnapshotMaxDirectories)
	if len(directories) > 0 {
		lines = append(lines, "", fmt.Sprintf("Directories (%d):", len(ws.DirectoryReferences)))
		for _, dir := range directories {
			lines = append(lines, fmt.Sprintf(
				"- Directory: %q path=%q",
				sanitizeWorkspaceSnapshotText(dir.Name, workspaceSnapshotTextLimit),
				sanitizeWorkspaceSnapshotText(dir.Path, workspaceSnapshotPathLimit),
			))
		}
	}

	if len(sessions) > 0 {
		lines = append(lines, "", fmt.Sprintf("Recent sessions (%d):", sessionCount))
		for _, item := range limitWorkspaceSessions(sessions, workspaceSnapshotMaxSessions) {
			lines = append(lines, fmt.Sprintf(
				"- Session: %q agent=%q updated_at=%q",
				sanitizeWorkspaceSnapshotText(item.Title, workspaceSnapshotTextLimit),
				sanitizeWorkspaceSnapshotText(item.AgentName, workspaceSnapshotTextLimit),
				formatWorkspaceSnapshotTime(item.UpdatedAt),
			))
		}
	}

	lines = append(lines, "", "Treat this snapshot as current workspace state.")

	if toolCallable {
		lines = append(lines,
			"",
			"## Workspace Tools",
			"You have workspace tools that you MUST use to interact with this workspace:",
			"",
			"Context tools (use proactively to answer questions better):",
			"- workspace_notes: list notes in this workspace and use the id field from its results when reading one note",
			"- workspace_save_note: create or update a note",
			"- workspace_tasks: list tasks with optional status filter",
			"- workspace_sessions: list sessions in this workspace and use the id field from its results",
			"- workspace_session_detail: read messages from a specific session by exact id (never invent placeholder UUIDs or guess ids; use ids returned by workspace_sessions)",
			"- workspace_files: list attached files",
			"- workspace_directories: list referenced directories",
			"",
			"Management tools:",
			"- workspace_manage_agents: list/add/remove workspace agents",
			"- workspace_manage_mcp: list/attach/detach MCP server bindings",
			"- workspace_manage_skills: list/attach/detach skill bindings",
			"",
			"CRITICAL behavior rules:",
			"- You MUST use function calls (tool_call) to interact with the workspace. NEVER just say you did something — actually call the tool.",
			"- WRONG: Responding with text that says 'I have saved this as a note'. RIGHT: Making a workspace_save_note function call, then confirming after the call succeeds.",
			"- When the user asks to save, store, or remember something, emit a workspace_save_note tool_call immediately.",
			"- When you generate useful content (lists, recommendations, plans, research), emit a workspace_save_note tool_call to save it, then tell the user you did after the call returns.",
			"- When answering questions, check workspace_notes and workspace_sessions first for existing context.",
			"- For travel, itinerary, booking, or trip-planning intake requests, do not get stuck repeatedly listing notes or sessions before replying. Use existing notes only when they materially answer the request; otherwise ask the intake questions or continue the planning flow.",
			"- For short travel preference follow-ups such as 'food', 'pintxos', 'museums', 'budget', 'nightlife', or neighborhood preferences, answer directly from the current plan and only check notes or sessions if the user explicitly asks about something previously saved.",
			"- When the user confirms a suggestion you made (e.g. says 'yes', 'do it', 'go ahead'), execute the action immediately using the appropriate tool_call.",
			"- NEVER claim you saved, created, or modified something without actually making the corresponding function call.",
		)
	} else {
		lines = append(lines,
			"",
			"## Workspace Access",
			"This route includes workspace snapshot context only. Live workspace tool calls are unavailable in this provider path.",
			"- Do not claim you saved, created, updated, or fetched workspace data unless the server explicitly did it for you.",
			"- If the user asks to modify workspace data, explain the limitation briefly unless the server handles the action directly.",
		)
	}

	return strings.Join(lines, "\n")
}

func shouldAttachWorkspaceSnapshot(routeCtx normalizedChatRouteContext) bool {
	if strings.TrimSpace(routeCtx.WorkspaceID) == "" {
		return false
	}
	switch routeCtx.Surface {
	case "workspace_detail", "workspace_canvas", "workspace_chat":
		return true
	default:
		return false
	}
}

type workspaceAgentSummary struct {
	Count int
	Label string
}

func buildWorkspaceAgentSummary(ws *workspace.Workspace) workspaceAgentSummary {
	if ws == nil {
		return workspaceAgentSummary{Label: "none"}
	}

	instanceNames := make([]string, 0, len(ws.AgentInstances))
	for _, item := range ws.AgentInstances {
		if name := sanitizeWorkspaceSnapshotText(item.Name, workspaceSnapshotTextLimit); name != "" {
			instanceNames = append(instanceNames, name)
		}
	}
	if len(instanceNames) > 0 {
		sort.Strings(instanceNames)
		return workspaceAgentSummary{
			Count: len(instanceNames),
			Label: strings.Join(instanceNames, ", "),
		}
	}

	names := make([]string, 0, len(ws.Agents))
	for _, name := range ws.Agents {
		if cleaned := sanitizeWorkspaceSnapshotText(name, workspaceSnapshotTextLimit); cleaned != "" {
			names = append(names, cleaned)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return workspaceAgentSummary{Label: "none"}
	}

	return workspaceAgentSummary{
		Count: len(names),
		Label: strings.Join(names, ", "),
	}
}

func buildWorkspaceTaskCountsSummary(stats map[string]int) string {
	if len(stats) == 0 {
		return "total=0"
	}

	parts := []string{fmt.Sprintf("total=%d", stats["total"])}
	for _, key := range []string{"pending", "assigned", "in_progress", "completed", "failed", "cancelled", "timeout", "scheduled"} {
		if count := stats[key]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", key, count))
		}
	}
	return strings.Join(parts, ", ")
}

func collectWorkspaceOpenTasks(tasks []workspace.Task, limit int) []workspace.Task {
	if limit <= 0 || len(tasks) == 0 {
		return nil
	}

	openTasks := make([]workspace.Task, 0, min(limit, len(tasks)))
	for _, task := range tasks {
		switch task.Status {
		case workspace.TaskStatusPending, workspace.TaskStatusAssigned, workspace.TaskStatusInProgress:
			openTasks = append(openTasks, task)
			if len(openTasks) == limit {
				return openTasks
			}
		}
	}

	return openTasks
}

func sanitizeWorkspaceTaskAssignee(task workspace.Task) string {
	assignee := task.To
	if strings.TrimSpace(assignee) == "" {
		assignee = task.AssignedNodeID
	}
	if strings.TrimSpace(assignee) == "" {
		assignee = "unassigned"
	}
	return sanitizeWorkspaceSnapshotText(assignee, workspaceSnapshotTextLimit)
}

func limitWorkspaceNotes(notes []session.WorkspaceNoteListItem, limit int) []session.WorkspaceNoteListItem {
	if limit <= 0 || len(notes) == 0 {
		return nil
	}
	if len(notes) <= limit {
		return notes
	}
	return notes[:limit]
}

func collectWorkspaceFiles(attachments []workspace.Attachment, limit int) []workspace.Attachment {
	if limit <= 0 || len(attachments) == 0 {
		return nil
	}

	files := make([]workspace.Attachment, 0, min(limit, len(attachments)))
	for _, attachment := range attachments {
		if attachment.DeletedAt != nil {
			continue
		}
		if attachment.File == nil && attachment.Type == workspace.AttachmentTypeDoc {
			continue
		}
		files = append(files, attachment)
		if len(files) == limit {
			return files
		}
	}

	return files
}

func countWorkspaceFiles(attachments []workspace.Attachment) int {
	count := 0
	for _, attachment := range attachments {
		if attachment.DeletedAt != nil {
			continue
		}
		if attachment.File == nil && attachment.Type == workspace.AttachmentTypeDoc {
			continue
		}
		count++
	}
	return count
}

func workspaceSnapshotAttachmentName(att workspace.Attachment) string {
	if att.File != nil && strings.TrimSpace(att.File.Name) != "" {
		return sanitizeWorkspaceSnapshotText(att.File.Name, workspaceSnapshotTextLimit)
	}
	return sanitizeWorkspaceSnapshotText(att.Title, workspaceSnapshotTextLimit)
}

func workspaceSnapshotAttachmentMIME(att workspace.Attachment) string {
	if att.File != nil && strings.TrimSpace(att.File.Mime) != "" {
		return sanitizeWorkspaceSnapshotText(att.File.Mime, workspaceSnapshotTextLimit)
	}
	return sanitizeWorkspaceSnapshotText(string(att.Type), workspaceSnapshotTextLimit)
}

func limitWorkspaceDirectories(directories []workspace.DirectoryReference, limit int) []workspace.DirectoryReference {
	if limit <= 0 || len(directories) == 0 {
		return nil
	}
	if len(directories) <= limit {
		return directories
	}
	return directories[:limit]
}

func limitWorkspaceSessions(items []session.SessionListItem, limit int) []session.SessionListItem {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func sanitizeWorkspaceSnapshotText(value string, maxLen int) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if maxLen > 0 && len(cleaned) > maxLen {
		if maxLen <= 3 {
			return cleaned[:maxLen]
		}
		return cleaned[:maxLen-3] + "..."
	}
	return cleaned
}

func formatWorkspaceSnapshotTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339)
}
