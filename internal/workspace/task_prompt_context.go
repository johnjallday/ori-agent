package workspace

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

type TaskPromptNoteSummary struct {
	ID      string
	Name    string
	Preview string
}

type TaskPromptSessionSummary struct {
	Title     string
	AgentName string
	UpdatedAt time.Time
}

type TaskPreparedContext struct {
	Strategy       string
	Summary        string
	Items          []TaskPreparedContextItem
	AvailableTools []string
	PreparedAt     time.Time
}

type TaskPreparedContextItem struct {
	Kind   string
	Ref    string
	Name   string
	Access string
	Detail string
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
	prompt.WriteString("The workspace snapshot below shows note ids and truncated previews (~160 chars). When a task asks to review, summarize, transform, or create tasks from a note, you must call the workspace_notes tool with the note's id to read the full content before answering instead of relying on the preview or asking the user to paste it. ")
	prompt.WriteString("Use workspace_tasks, workspace_sessions, workspace_files, and workspace_directories the same way to read full workspace state on demand. ")
	prompt.WriteString("For fresh public-information tasks such as today's weather, pollen, prices, scores, news, or other current facts, use web_search first when available, then read relevant source pages with web_fetch or browser as needed. Do not guess direct source URLs for location-specific facts; verify that fetched pages match the requested city, region, or ZIP before using them. If search results are empty, broaden the query, remove site-specific filters, and try multiple public sources instead of stopping. Do not return raw Tool Results as the final answer. Do not answer those tasks from prior blocked attempts or workspace task-status summaries unless the user explicitly asks for task status. Include source names or URLs and visible dates when available. ")
	prompt.WriteString("When a task includes a Reference URL, treat it as authoritative source material. Inspect it with available fetch, browser, or web tools before making claims about its contents or implementing changes that depend on it. If you cannot inspect it because tools are unavailable, the host is unreachable, authentication is required, or access is blocked, state that limitation instead of fabricating URL contents. ")
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
	if referenceURL := strings.TrimSpace(task.ReferenceURL); referenceURL != "" {
		lines = append(lines, fmt.Sprintf("- Task Reference URL: %q", sanitizeTaskPromptText(referenceURL, taskPromptPathLimit)))
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
	if taskPromptLooksForFreshPublicInformation(task.Description) {
		lines = append(lines, "- Fresh public-information task: unrelated prior task summaries are omitted unless they are explicit inputs.")
	}
	for _, wsTask := range collectTaskPromptTasksForPrompt(ws.Tasks, task, taskPromptMaxTasks) {
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
				"- Note: id=%q name=%q preview=%q",
				sanitizeTaskPromptText(note.ID, taskPromptTextLimit),
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

// PrepareTaskContext records the same high-level context surfaces that task
// execution already uses so the run harness can expose them before Execute.
func (h *LLMTaskHandler) PrepareTaskContext(ctx context.Context, agentName string, task Task) (*TaskPreparedContext, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prepared := &TaskPreparedContext{
		Strategy:   "task_default",
		PreparedAt: time.Now(),
	}

	if workspaceSnapshot := h.buildTaskWorkspaceSnapshot(ctx, task); workspaceSnapshot != "" {
		prepared.Items = append(prepared.Items, TaskPreparedContextItem{
			Kind:   "workspace_snapshot",
			Name:   "Workspace snapshot",
			Access: "injected",
			Detail: "Workspace metadata plus bounded summaries for tasks, files, directories, notes, and recent sessions are injected into the prompt.",
		})
	}

	var ws *Workspace
	if h.workspaceStore != nil && strings.TrimSpace(task.WorkspaceID) != "" {
		ws, _ = h.workspaceStore.Get(task.WorkspaceID)
	}
	if referenceURL := strings.TrimSpace(task.ReferenceURL); referenceURL != "" {
		prepared.Items = append(prepared.Items, TaskPreparedContextItem{
			Kind:   "reference_url",
			Ref:    referenceURL,
			Name:   "Reference URL",
			Access: "on_demand",
			Detail: "The task includes an authoritative reference URL; inspect it with available web tools before claims or implementation that depends on its contents.",
		})
	}
	if ws != nil {
		if fileCount := countTaskPromptFiles(ws.Attachments); fileCount > 0 {
			prepared.Items = append(prepared.Items, TaskPreparedContextItem{
				Kind:   "workspace_files",
				Name:   "Workspace file summaries",
				Access: "summarized",
				Detail: fmt.Sprintf("%d workspace file(s) are summarized in the snapshot; full content is not injected unless attached to this task.", fileCount),
			})
		}
		if len(ws.DirectoryReferences) > 0 {
			prepared.Items = append(prepared.Items, TaskPreparedContextItem{
				Kind:   "workspace_directories",
				Name:   "Workspace directory references",
				Access: "summarized",
				Detail: fmt.Sprintf("%d directory reference(s) are summarized; contents are available only through tools.", len(ws.DirectoryReferences)),
			})
		}
	}

	if h.contextStore != nil && strings.TrimSpace(task.WorkspaceID) != "" {
		if notes, err := h.contextStore.ListNotesByWorkspace(ctx, task.WorkspaceID); err == nil && len(notes) > 0 {
			prepared.Items = append(prepared.Items, TaskPreparedContextItem{
				Kind:   "workspace_notes",
				Name:   "Workspace note previews",
				Access: "summarized",
				Detail: fmt.Sprintf("%d note(s) have preview text in the snapshot; full bodies are available only through tools.", len(notes)),
			})
		}
		if sessions, count, err := h.contextStore.ListSessionsByWorkspace(ctx, task.WorkspaceID, taskPromptMaxSessions); err == nil && (len(sessions) > 0 || count > 0) {
			prepared.Items = append(prepared.Items, TaskPreparedContextItem{
				Kind:   "workspace_sessions",
				Name:   "Recent workspace sessions",
				Access: "summarized",
				Detail: fmt.Sprintf("%d recent session(s) are summarized in the snapshot.", count),
			})
		}
	}

	prepared.Items = append(prepared.Items, buildAttachedTaskPreparedContextItems(ws, task)...)

	ag, err := h.resolveExecutionAgent(agentName, task)
	if err != nil {
		return nil, err
	}
	tools := h.convertAgentToolsToLLMTools(ag, task)
	prepared.AvailableTools = make([]string, 0, len(tools))
	for _, tool := range tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			prepared.AvailableTools = append(prepared.AvailableTools, name)
		}
	}
	sort.Strings(prepared.AvailableTools)
	if len(prepared.AvailableTools) > 0 {
		prepared.Items = append(prepared.Items, TaskPreparedContextItem{
			Kind:   "workspace_tools",
			Name:   "Workspace and utility tools",
			Access: "on_demand",
			Detail: fmt.Sprintf("%d tool(s) are available for retrieving full workspace state or external information during execution.", len(prepared.AvailableTools)),
		})
	}

	prepared.Summary = buildTaskPreparedContextSummary(prepared.Items)
	return prepared, nil
}

func buildTaskPreparedContextSummary(items []TaskPreparedContextItem) string {
	var injected, summarized, onDemand int
	for _, item := range items {
		switch item.Access {
		case "injected":
			injected++
		case "summarized":
			summarized++
		case "on_demand":
			onDemand++
		}
	}
	return fmt.Sprintf("%d injected, %d summarized, %d on-demand context surface(s)", injected, summarized, onDemand)
}

func buildAttachedTaskPreparedContextItems(ws *Workspace, task Task) []TaskPreparedContextItem {
	if ws == nil || ws.Layout == nil {
		return nil
	}
	attachmentsByID := make(map[string]Attachment, len(ws.Attachments))
	for _, attachment := range ws.Attachments {
		attachmentsByID[attachment.ID] = attachment
	}

	var items []TaskPreparedContextItem
	for _, connection := range ws.Layout.WorkflowConnections {
		if connection.To != task.ID {
			continue
		}
		attachment, ok := attachmentsByID[connection.From]
		if !ok {
			continue
		}
		name := strings.TrimSpace(attachment.Title)
		if name == "" {
			name = "Attached file"
		}
		ref := ""
		detail := "Attached content is injected into the prompt during execution."
		if attachment.File != nil && strings.TrimSpace(attachment.File.URL) != "" {
			ref = attachment.File.URL
			detail = fmt.Sprintf("Attached content from %s is injected into the prompt during execution.", attachment.File.URL)
		}
		items = append(items, TaskPreparedContextItem{
			Kind:   "attached_file",
			Ref:    ref,
			Name:   name,
			Access: "injected",
			Detail: detail,
		})
	}
	return items
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
	for _, key := range []string{"pending", "assigned", "in_progress", "waiting_for_choice", "completed", "failed", "cancelled", "timeout", "scheduled"} {
		if count := stats[key]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", key, count))
		}
	}
	return strings.Join(parts, ", ")
}

func collectTaskPromptTasksForPrompt(tasks []Task, current Task, limit int) []Task {
	if limit <= 0 || len(tasks) == 0 {
		return nil
	}

	publicInfoTask := taskPromptLooksForFreshPublicInformation(current.Description)
	inputTaskIDs := make(map[string]struct{}, len(current.InputTaskIDs))
	for _, id := range current.InputTaskIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			inputTaskIDs[trimmed] = struct{}{}
		}
	}

	openTasks := make([]Task, 0, min(limit, len(tasks)))
	for _, task := range tasks {
		if publicInfoTask && strings.TrimSpace(task.ID) != strings.TrimSpace(current.ID) {
			if _, explicitInput := inputTaskIDs[strings.TrimSpace(task.ID)]; !explicitInput {
				continue
			}
		}
		switch task.Status {
		case TaskStatusPending, TaskStatusAssigned, TaskStatusInProgress, TaskStatusWaitingForChoice:
			openTasks = append(openTasks, task)
			if len(openTasks) == limit {
				return openTasks
			}
		}
	}

	return openTasks
}

func taskPromptLooksForFreshPublicInformation(description string) bool {
	normalized := strings.ToLower(strings.TrimSpace(description))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"today",
		"current",
		"latest",
		"recent",
		"now",
		"weather",
		"forecast",
		"pollen",
		"air quality",
		"price",
		"stock",
		"score",
		"news",
		"flight",
		"hotel",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
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
func (h *LLMTaskHandler) buildTaskPrompt(ctx context.Context, task Task) string {
	var prompt strings.Builder

	prompt.WriteString("# Task Assignment\n\n")
	prompt.WriteString("You have been assigned a task in a collaborative workspace.\n\n")
	fmt.Fprintf(&prompt, "**Task ID**: %s\n", task.ID)
	fmt.Fprintf(&prompt, "**From**: %s\n", task.From)
	fmt.Fprintf(&prompt, "**Priority**: %d/5\n\n", task.Priority)

	// Process task description with placeholder substitution
	processedDescription := h.substitutePlaceholders(task)
	fmt.Fprintf(&prompt, "## Task Description\n\n%s\n\n", processedDescription)

	if details := strings.TrimSpace(task.Details); details != "" {
		prompt.WriteString("## Task Details\n\n")
		prompt.WriteString(details)
		prompt.WriteString("\n\n")
	}

	if referenceURL := strings.TrimSpace(task.ReferenceURL); referenceURL != "" {
		prompt.WriteString("## Reference URL\n\n")
		prompt.WriteString(referenceURL)
		prompt.WriteString("\n\n")
		prompt.WriteString("Treat this URL as authoritative source material for this task. Inspect it with available fetch, browser, or web tools before making claims about its contents. If you cannot inspect it, state that limitation in your final response.\n\n")
	}

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
			fmt.Fprintf(&prompt, "### %s\n\n", att.Title)
			if att.FilePath != "" {
				fmt.Fprintf(&prompt, "**File**: `%s`\n\n", att.FilePath)
			}
			if att.Body != "" {
				fmt.Fprintf(&prompt, "**Note**: %s\n\n", att.Body)
			}
			if att.Content != "" {
				prompt.WriteString("**Content**:\n```\n")
				prompt.WriteString(att.Content)
				prompt.WriteString("\n```\n\n")
			}
		}
	}

	// Handle input task results specially for better formatting. Runtime inputs
	// (rebuilt each execution) live on task.RuntimeInputs — never in Context.
	// Structured outputs from upstream tasks with an OutputSchema get rendered
	// as JSON alongside the raw text, so downstream tasks can consume either.
	if task.RuntimeInputs != nil {
		h.formatInputResults(&prompt, task.RuntimeInputs)
	}

	// Include authored context fields. With runtime inputs no longer merged
	// into Context, every key here is authored — no filtering needed.
	if len(task.Context) > 0 {
		prompt.WriteString("## Additional Context\n\n")
		for key, value := range task.Context {
			fmt.Fprintf(&prompt, "- **%s**: %v\n", key, value)
		}
		prompt.WriteString("\n")
	}

	if outputInstructions := BuildTaskOutputSpecPrompt(ActiveTaskOutputSpec(&task)); outputInstructions != "" {
		prompt.WriteString("## Required Output Format\n\n")
		prompt.WriteString(outputInstructions)
		prompt.WriteString("\n\n")
	} else if outputInstructions := BuildTaskOutputSchemaPrompt(task.OutputSchema); outputInstructions != "" {
		prompt.WriteString("## Required Output Format\n\n")
		prompt.WriteString(outputInstructions)
		prompt.WriteString("\n\n")
	} else if outputInstructions := BuildTaskOutputContractPrompt(task.OutputContract); outputInstructions != "" {
		prompt.WriteString("## Required Output Format\n\n")
		prompt.WriteString(outputInstructions)
		prompt.WriteString("\n\n")
	}

	if task.Timeout > 0 {
		fmt.Fprintf(&prompt, "**Time Limit**: %v\n\n", task.Timeout)
	}

	prompt.WriteString("Please complete this task to the best of your ability. ")
	prompt.WriteString("**Important**: Only use tools when they are explicitly necessary to complete the task. ")
	prompt.WriteString("For informational requests, meta-commands (like /tools, /help), or simple questions, ")
	prompt.WriteString("respond directly without calling tools. ")
	if ActiveTaskOutputSpec(&task) != nil || NormalizeTaskOutputSchema(task.OutputSchema) != nil || NormalizeTaskOutputContract(task.OutputContract) != nil {
		prompt.WriteString("When you are done, your final answer must be the JSON object described above and nothing else.")
	} else {
		prompt.WriteString("Provide a clear, concise response with your findings or results.")
	}

	return prompt.String()
}
