package workspace

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
)

type TaskPromptNoteSummary struct {
	Name    string
	Preview string
}

type TaskPromptSessionSummary struct {
	Title     string
	AgentName string
	UpdatedAt time.Time
}

type taskPromptContextStore interface {
	ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]TaskPromptNoteSummary, error)
	ListSessionsByWorkspace(ctx context.Context, workspaceID string, limit int) ([]TaskPromptSessionSummary, int, error)
}

const (
	taskPromptMaxTasks       = 6
	taskPromptMaxNotes       = 5
	taskPromptMaxFiles       = 5
	taskPromptMaxDirectories = 5
	taskPromptMaxSessions    = 5
	taskPromptTextLimit      = 120
	taskPromptPreviewLimit   = 160
	taskPromptPathLimit      = 180
)

func (h *LLMTaskHandler) buildTaskSystemPrompt() string {
	var prompt strings.Builder
	prompt.WriteString("You are a helpful AI assistant completing a task in a collaborative workspace. ")
	prompt.WriteString("You have access to tools, but only use them when they are clearly necessary to complete the specific task. ")
	prompt.WriteString("For simple questions, greetings, or informational requests, respond naturally without calling tools. ")
	prompt.WriteString("When a task refers to the workspace, interpret workspace as the collaborative workspace data provided in the prompt ")
	prompt.WriteString("(tasks, notes, files, directories, sessions), not the server's current working directory, git checkout, or repository state, ")
	prompt.WriteString("unless the task explicitly asks about code, the repository, or the filesystem. ")
	prompt.WriteString("Use the workspace snapshot in the prompt as the source of truth for workspace-summary requests. ")
	prompt.WriteString("If the available workspace data is limited, say that directly instead of substituting repository/worktree context. ")
	prompt.WriteString("If you use tools, continue reasoning from the tool results until you can either complete the requested step or explain exactly what is still blocked. ")
	prompt.WriteString("If a task asks for file or folder contents, directory listings, or filesystem state, you must verify the answer with filesystem tools before responding. ")
	prompt.WriteString("Do not answer filesystem listing tasks from the workspace snapshot, prior attempt summaries, or assumptions alone. ")
	prompt.WriteString("If the task explicitly asks for a file list or folder contents and you can answer, return the list directly instead of asking whether the user wants to see it. ")
	prompt.WriteString("If the task names a specific file or folder, inspect that exact target after locating it instead of stopping at the parent directory. ")
	prompt.WriteString("Do not treat a discovery-only tool call as completion for a task that requires making changes. ")
	prompt.WriteString("Only ask for user confirmation if the task is truly blocked or risky without explicit user input. ")
	prompt.WriteString("Be thoughtful and precise in your responses.")
	return prompt.String()
}

func (h *LLMTaskHandler) buildTaskWorkspaceSnapshot(ctx context.Context, task Task) string {
	if h.workspaceStore == nil || strings.TrimSpace(task.WorkspaceID) == "" {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ws, err := h.workspaceStore.Get(task.WorkspaceID)
	if err != nil || ws == nil {
		logger.Debug("Skipping workspace snapshot for task prompt", logger.Fields{
			"workspace_id": task.WorkspaceID,
			"error":        err,
		})
		return ""
	}

	var notes []TaskPromptNoteSummary
	var sessions []TaskPromptSessionSummary
	sessionCount := 0
	if h.contextStore != nil {
		notes, err = h.contextStore.ListNotesByWorkspace(ctx, task.WorkspaceID)
		if err != nil {
			logger.Debug("Failed to load workspace notes for task prompt", logger.Fields{
				"workspace_id": task.WorkspaceID,
				"error":        err,
			})
		}

		sessions, sessionCount, err = h.contextStore.ListSessionsByWorkspace(ctx, task.WorkspaceID, taskPromptMaxSessions)
		if err != nil {
			logger.Debug("Failed to load workspace sessions for task prompt", logger.Fields{
				"workspace_id": task.WorkspaceID,
				"error":        err,
			})
			sessions = nil
			sessionCount = 0
		}
	}

	lines := []string{
		"## Workspace Snapshot",
		"",
		"This is the current collaborative workspace state for this task.",
		fmt.Sprintf("- Workspace ID: %q", sanitizeTaskPromptText(ws.ID, taskPromptTextLimit)),
		fmt.Sprintf("- Workspace Name: %q", sanitizeTaskPromptText(ws.Name, taskPromptTextLimit)),
	}

	if description := sanitizeTaskPromptText(ws.Description, taskPromptPreviewLimit); description != "" {
		lines = append(lines, fmt.Sprintf("- Workspace Description: %q", description))
	}

	lines = append(lines,
		fmt.Sprintf("- Workspace Status: %q", sanitizeTaskPromptText(string(ws.Status), taskPromptTextLimit)),
	)
	if !ws.UpdatedAt.IsZero() {
		lines = append(lines, fmt.Sprintf("- Workspace Updated At: %q", ws.UpdatedAt.UTC().Format(time.RFC3339)))
	}

	agentSummary := buildTaskPromptAgentSummary(ws)
	lines = append(lines, fmt.Sprintf("- Agents (%d): %s", agentSummary.Count, agentSummary.Label))

	stats := ws.GetTaskStats()
	lines = append(lines, "", "### Workspace Tasks", "")
	lines = append(lines, fmt.Sprintf("- Counts: %s", buildTaskPromptTaskCountsSummary(stats)))
	for _, wsTask := range collectTaskPromptTasks(ws.Tasks, taskPromptMaxTasks) {
		lines = append(lines, fmt.Sprintf(
			"- Open task: [%s] %q -> %q (priority %d)",
			sanitizeTaskPromptText(string(wsTask.Status), taskPromptTextLimit),
			sanitizeTaskPromptText(wsTask.Description, taskPromptTextLimit),
			taskPromptAssignee(wsTask),
			wsTask.Priority,
		))
	}

	files := collectTaskPromptFiles(ws.Attachments, taskPromptMaxFiles)
	if len(files) > 0 {
		lines = append(lines, "", fmt.Sprintf("### Workspace Files (%d)", countTaskPromptFiles(ws.Attachments)), "")
		for _, file := range files {
			lines = append(lines, fmt.Sprintf(
				"- File: %q (type=%q, mime=%q)",
				taskPromptAttachmentName(file),
				sanitizeTaskPromptText(string(file.Type), taskPromptTextLimit),
				taskPromptAttachmentMIME(file),
			))
		}
	}

	directories := limitTaskPromptDirectories(ws.DirectoryReferences, taskPromptMaxDirectories)
	if len(directories) > 0 {
		lines = append(lines, "", fmt.Sprintf("### Workspace Directories (%d)", len(ws.DirectoryReferences)), "")
		for _, dir := range directories {
			lines = append(lines, fmt.Sprintf(
				"- Directory: %q path=%q",
				sanitizeTaskPromptText(dir.Name, taskPromptTextLimit),
				sanitizeTaskPromptText(dir.Path, taskPromptPathLimit),
			))
		}
	}

	if len(notes) > 0 {
		lines = append(lines, "", fmt.Sprintf("### Workspace Notes (%d)", len(notes)), "")
		for _, note := range limitTaskPromptNotes(notes, taskPromptMaxNotes) {
			lines = append(lines, fmt.Sprintf(
				"- Note: %q - %q",
				sanitizeTaskPromptText(note.Name, taskPromptTextLimit),
				sanitizeTaskPromptText(note.Preview, taskPromptPreviewLimit),
			))
		}
	}

	if len(sessions) > 0 {
		lines = append(lines, "", fmt.Sprintf("### Recent Workspace Sessions (%d)", sessionCount), "")
		for _, session := range limitTaskPromptSessions(sessions, taskPromptMaxSessions) {
			lines = append(lines, fmt.Sprintf(
				"- Session: %q agent=%q updated_at=%q",
				sanitizeTaskPromptText(session.Title, taskPromptTextLimit),
				sanitizeTaskPromptText(session.AgentName, taskPromptTextLimit),
				session.UpdatedAt.UTC().Format(time.RFC3339),
			))
		}
	}

	lines = append(lines,
		"",
		"Use this workspace snapshot when the task asks about the workspace. Do not replace it with repository or worktree assumptions unless the task explicitly asks for repository context.",
	)

	return strings.Join(lines, "\n")
}

type taskPromptAgentSummary struct {
	Count int
	Label string
}

func buildTaskPromptAgentSummary(ws *Workspace) taskPromptAgentSummary {
	if ws == nil {
		return taskPromptAgentSummary{Label: "none"}
	}

	names := make([]string, 0, len(ws.AgentInstances))
	for _, item := range ws.AgentInstances {
		if name := sanitizeTaskPromptText(item.Name, taskPromptTextLimit); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		for _, item := range ws.Agents {
			if name := sanitizeTaskPromptText(item, taskPromptTextLimit); name != "" {
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return taskPromptAgentSummary{Label: "none"}
	}

	return taskPromptAgentSummary{
		Count: len(names),
		Label: strings.Join(names, ", "),
	}
}

func buildTaskPromptTaskCountsSummary(stats map[string]int) string {
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

func collectTaskPromptTasks(tasks []Task, limit int) []Task {
	if limit <= 0 || len(tasks) == 0 {
		return nil
	}

	openTasks := make([]Task, 0, min(limit, len(tasks)))
	for _, task := range tasks {
		switch task.Status {
		case TaskStatusPending, TaskStatusAssigned, TaskStatusInProgress:
			openTasks = append(openTasks, task)
			if len(openTasks) == limit {
				return openTasks
			}
		}
	}

	return openTasks
}

func taskPromptAssignee(task Task) string {
	assignee := task.To
	if strings.TrimSpace(assignee) == "" {
		assignee = task.AssignedNodeID
	}
	if strings.TrimSpace(assignee) == "" {
		assignee = "unassigned"
	}
	return sanitizeTaskPromptText(assignee, taskPromptTextLimit)
}

func collectTaskPromptFiles(attachments []Attachment, limit int) []Attachment {
	if limit <= 0 || len(attachments) == 0 {
		return nil
	}
	files := make([]Attachment, 0, min(limit, len(attachments)))
	for _, att := range attachments {
		if att.DeletedAt != nil {
			continue
		}
		if att.File == nil && att.Type == AttachmentTypeDoc {
			continue
		}
		files = append(files, att)
		if len(files) == limit {
			return files
		}
	}
	return files
}

func countTaskPromptFiles(attachments []Attachment) int {
	count := 0
	for _, att := range attachments {
		if att.DeletedAt != nil {
			continue
		}
		if att.File == nil && att.Type == AttachmentTypeDoc {
			continue
		}
		count++
	}
	return count
}

func taskPromptAttachmentName(att Attachment) string {
	if att.File != nil && strings.TrimSpace(att.File.Name) != "" {
		return sanitizeTaskPromptText(att.File.Name, taskPromptTextLimit)
	}
	return sanitizeTaskPromptText(att.Title, taskPromptTextLimit)
}

func taskPromptAttachmentMIME(att Attachment) string {
	if att.File != nil && strings.TrimSpace(att.File.Mime) != "" {
		return sanitizeTaskPromptText(att.File.Mime, taskPromptTextLimit)
	}
	return sanitizeTaskPromptText(string(att.Type), taskPromptTextLimit)
}

func limitTaskPromptDirectories(directories []DirectoryReference, limit int) []DirectoryReference {
	if limit <= 0 || len(directories) == 0 {
		return nil
	}
	if len(directories) <= limit {
		return directories
	}
	return directories[:limit]
}

func limitTaskPromptNotes(notes []TaskPromptNoteSummary, limit int) []TaskPromptNoteSummary {
	if limit <= 0 || len(notes) == 0 {
		return nil
	}
	if len(notes) <= limit {
		return notes
	}
	return notes[:limit]
}

func limitTaskPromptSessions(sessions []TaskPromptSessionSummary, limit int) []TaskPromptSessionSummary {
	if limit <= 0 || len(sessions) == 0 {
		return nil
	}
	if len(sessions) <= limit {
		return sessions
	}
	return sessions[:limit]
}

func sanitizeTaskPromptText(value string, maxLen int) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if maxLen > 0 && len(cleaned) > maxLen {
		if maxLen <= 3 {
			return cleaned[:maxLen]
		}
		return cleaned[:maxLen-3] + "..."
	}
	return cleaned
}

// buildTaskPrompt creates a prompt for the task
func (h *LLMTaskHandler) buildTaskPrompt(ctx context.Context, task Task, ag *agent.Agent) string {
	var prompt strings.Builder

	prompt.WriteString("# Task Assignment\n\n")
	prompt.WriteString("You have been assigned a task in a collaborative workspace.\n\n")
	prompt.WriteString(fmt.Sprintf("**Task ID**: %s\n", task.ID))
	prompt.WriteString(fmt.Sprintf("**From**: %s\n", task.From))
	prompt.WriteString(fmt.Sprintf("**Priority**: %d/5\n\n", task.Priority))

	// Process task description with placeholder substitution
	processedDescription := h.substitutePlaceholders(task)
	prompt.WriteString(fmt.Sprintf("## Task Description\n\n%s\n\n", processedDescription))

	if workspaceSnapshot := h.buildTaskWorkspaceSnapshot(ctx, task); workspaceSnapshot != "" {
		prompt.WriteString(workspaceSnapshot)
		prompt.WriteString("\n\n")
	}

	// Include attachments if any are connected to this task
	attachmentContents := h.getAttachedFileContents(task)
	if len(attachmentContents) > 0 {
		prompt.WriteString("## Attached Files\n\n")
		prompt.WriteString("The following files are attached to this task:\n\n")
		for _, att := range attachmentContents {
			prompt.WriteString(fmt.Sprintf("### %s\n\n", att.Title))
			if att.FilePath != "" {
				prompt.WriteString(fmt.Sprintf("**File**: `%s`\n\n", att.FilePath))
			}
			if att.Body != "" {
				prompt.WriteString(fmt.Sprintf("**Note**: %s\n\n", att.Body))
			}
			if att.Content != "" {
				prompt.WriteString("**Content**:\n```\n")
				prompt.WriteString(att.Content)
				prompt.WriteString("\n```\n\n")
			}
		}
	}

	// Handle input task results specially for better formatting
	inputTaskResults, hasInputResults := task.Context["input_task_results"]
	if hasInputResults {
		h.formatInputResults(&prompt, task, inputTaskResults)
	}

	// Include other context fields
	if len(task.Context) > 0 {
		hasOtherContext := false
		for key := range task.Context {
			if key != "input_task_results" {
				hasOtherContext = true
				break
			}
		}

		if hasOtherContext {
			prompt.WriteString("## Additional Context\n\n")
			for key, value := range task.Context {
				if key != "input_task_results" {
					prompt.WriteString(fmt.Sprintf("- **%s**: %v\n", key, value))
				}
			}
			prompt.WriteString("\n")
		}
	}

	if task.Timeout > 0 {
		prompt.WriteString(fmt.Sprintf("**Time Limit**: %v\n\n", task.Timeout))
	}

	prompt.WriteString("Please complete this task to the best of your ability. ")
	prompt.WriteString("**Important**: Only use tools when they are explicitly necessary to complete the task. ")
	prompt.WriteString("For informational requests, meta-commands (like /tools, /help), or simple questions, ")
	prompt.WriteString("respond directly without calling tools. ")
	prompt.WriteString("Provide a clear, concise response with your findings or results.")

	return prompt.String()
}
