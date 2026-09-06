package workspace

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/promptvars"
	"github.com/johnjallday/ori-agent/internal/userprofile"
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

// compactTaskSystemPrompt is a small-model-sized variant of the task system
// prompt (WS5.20): imperative bullets, task-first, ordered rules/output-last,
// preserving the load-bearing semantics of the full prompt (use tools only when
// needed; read full notes/files via workspace tools rather than previews; never
// fabricate URL/filesystem contents; synthesize tool results into a final answer;
// state blockers explicitly).
const compactTaskSystemPrompt = "You are completing one task in a shared workspace. Rules:\n" +
	"- Do the task described in the prompt. Use tools only when they are needed; answer simple questions directly.\n" +
	"- The workspace snapshot shows truncated previews. To use a full note, file, session, or directory, call the matching workspace_* tool with its id instead of relying on the preview.\n" +
	"- Never invent file, folder, or URL contents. If a tool is unavailable or a target is unreachable, say so plainly.\n" +
	"- After using tools, synthesize the results into a clear final answer. Never return raw tool output as your answer.\n" +
	"- If you cannot finish, state exactly what is blocking you."

// buildTaskSystemPrompt returns the task system prompt. The compact variant is
// used for local providers, whose smaller models follow a short imperative prompt
// better than the long cloud-tuned one (WS5.20); both preserve the same rules.
func (h *LLMTaskHandler) buildTaskSystemPrompt(compact bool) string {
	if compact {
		return compactTaskSystemPrompt
	}
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

// buildTaskMemorySection renders the workspace's persistent memory for the task
// prompt. Memory tools are available during task execution, so tool guidance is
// included. Returns "" when the store can't resolve a folder path (e.g. a
// non-folder store) or memory is empty and there's nothing to guide.
// resolveTaskAgentBasePrompt resolves the closed prompt-variable vocabulary in
// the task agent's base system prompt, but ONLY when it actually uses variables
// (opt-in): the task path otherwise ignores an agent's base prompt, so injecting
// it unconditionally would change behavior for every existing agent. When the
// author used variables they clearly intend a parametric persona to apply, so we
// resolve it and let the caller lead the task prompt with it (PRD FR24). Returns
// hadVars=false (and "") for plain prompts, leaving task behavior untouched.
func (h *LLMTaskHandler) resolveTaskAgentBasePrompt(ctx context.Context, ag *resolvedTaskAgent, agentName string, task Task) (string, bool) {
	if h == nil || ag == nil || ag.Agent == nil {
		return "", false
	}
	prompt := ag.Settings.SystemPrompt
	if !promptvars.HasVariables(prompt) {
		return "", false
	}

	var ws *Workspace
	if h.workspaceStore != nil && strings.TrimSpace(task.WorkspaceID) != "" {
		ws, _ = h.workspaceStore.Get(task.WorkspaceID)
	}
	inst, _ := AgentInstanceByName(ws, agentName)

	memory := ""
	if resolver, ok := h.workspaceStore.(workspaceFolderStore); ok && strings.TrimSpace(task.WorkspaceID) != "" {
		if raw, err := NewMemoryStore(resolver).ReadRaw(task.WorkspaceID); err == nil {
			memory = raw
		}
	}

	// Fetch notes / tools only when the prompt actually uses those variables.
	notes := ""
	if h.contextStore != nil && strings.Contains(prompt, "workspace.notes.recent") && strings.TrimSpace(task.WorkspaceID) != "" {
		if items, err := h.contextStore.ListNotesByWorkspace(ctx, task.WorkspaceID); err == nil {
			lines := make([]string, 0, len(items))
			for _, n := range limitTaskPromptNotes(items, taskPromptMaxNotes) {
				line := "- " + strings.TrimSpace(n.Name)
				if p := strings.TrimSpace(n.Preview); p != "" {
					line += " — " + p
				}
				lines = append(lines, line)
			}
			notes = strings.Join(lines, "\n")
		}
	}
	tools := ""
	if strings.Contains(prompt, "workspace.tools") {
		skillNames := make([]string, 0, len(ag.EffectiveSkills))
		for _, s := range ag.EffectiveSkills {
			skillNames = append(skillNames, s.Name)
		}
		tools = FormatToolNames(skillNames, ag.MCPServers)
	}

	return ResolveAgentBasePrompt(prompt, PromptVarInputs{
		Workspace:   ws,
		Instance:    inst,
		AgentName:   agentName,
		Memory:      memory,
		NotesRecent: notes,
		Tools:       tools,
		TaskGoal:    task.Description,
	})
}

func (h *LLMTaskHandler) buildTaskMemorySection(task Task) string {
	if h.workspaceStore == nil || strings.TrimSpace(task.WorkspaceID) == "" {
		return ""
	}
	resolver, ok := h.workspaceStore.(workspaceFolderStore)
	if !ok {
		return ""
	}
	parts := make([]string, 0, 3)
	doc, err := NewMemoryStore(resolver).Read(task.WorkspaceID)
	if err != nil {
		logger.Debug("Skipping workspace memory for task prompt", logger.Fields{
			"workspace_id": task.WorkspaceID,
			"error":        err,
		})
	} else if section := RenderMemoryPromptSection(doc, true); strings.TrimSpace(section) != "" {
		parts = append(parts, section)
	}
	current, getErr := h.workspaceStore.Get(task.WorkspaceID)
	if getErr == nil && current != nil {
		station := current
		if current.GetAssistantProgramState() == nil {
			if link := current.GetAssistantProjectLink(); link != nil {
				station, _ = h.workspaceStore.Get(link.StationWorkspaceID)
			} else {
				station = nil
			}
		}
		if section := RenderAssistantProgramPromptSection(current, station); strings.TrimSpace(section) != "" {
			parts = append(parts, section)
		}
		if station != nil {
			if document, learningErr := NewAssistantLearningStore(resolver).Read(station.ID); learningErr == nil {
				if section := RenderManagedLearningPromptSection(document); strings.TrimSpace(section) != "" {
					parts = append(parts, section)
				}
			}
		}
	}
	return strings.Join(parts, "\n\n---\n")
}

func (h *LLMTaskHandler) buildUserProfileSection(ctx context.Context, ownerUserID string) string {
	if h == nil || h.userProfileStore == nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ownerUserID = strings.TrimSpace(ownerUserID)
	if ownerUserID == "" {
		ownerUserID = userprofile.LocalUserID
	}
	profile, err := h.userProfileStore.Get(ctx, ownerUserID)
	if err != nil || profile == nil {
		if err != nil {
			logger.Debug("Skipping user profile for task prompt", logger.Fields{
				"user_id": ownerUserID,
				"error":   err,
			})
		}
		return ""
	}
	return userprofile.RenderUserProfileSection(profile)
}

func (h *LLMTaskHandler) taskOwnerUserID(task Task) string {
	if h == nil || h.workspaceStore == nil || strings.TrimSpace(task.WorkspaceID) == "" {
		return userprofile.LocalUserID
	}
	ws, err := h.workspaceStore.Get(task.WorkspaceID)
	if err != nil || ws == nil {
		return userprofile.LocalUserID
	}
	return strings.TrimSpace(ws.OwnerUserID)
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
	// Surface the rest of the workspace intent (systems/capabilities/context).
	// These live in the workspace_bootstrap shared data and used to reach the
	// model only via the canonical "Workspace Description" note; injecting them
	// directly keeps the full intent available without that note.
	for _, field := range []struct{ label, key string }{
		{"Workspace Systems", "systems"},
		{"Workspace Capabilities", "capabilities"},
		{"Workspace Context", "context"},
	} {
		if value := sanitizeTaskPromptText(workspaceBootstrapField(ws.SharedData, field.key), taskPromptPreviewLimit); value != "" {
			lines = append(lines, fmt.Sprintf("- %s: %q", field.label, value))
		}
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
		lines = append(lines, "", fmt.Sprintf("### Workspace Directories (%d)", len(directories)), "")
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
	if item, ok := linkedNotesContextItem(task); ok {
		prepared.Items = append(prepared.Items, item)
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
		if directories := limitTaskPromptDirectories(ws.DirectoryReferences, taskPromptMaxDirectories); len(directories) > 0 {
			prepared.Items = append(prepared.Items, TaskPreparedContextItem{
				Kind:   "workspace_directories",
				Name:   "Workspace directory references",
				Access: "summarized",
				Detail: fmt.Sprintf("%d directory reference(s) are summarized; contents are available only through tools.", len(directories)),
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
	result := make([]DirectoryReference, 0, min(limit, len(directories)))
	for _, directory := range directories {
		if directory.Purpose == "sample_library" {
			continue
		}
		result = append(result, directory)
		if len(result) == limit {
			break
		}
	}
	return result
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

// workspaceBootstrapField reads a string field (goal/systems/capabilities/
// context) from the workspace_bootstrap shared-data map. This is the workspace
// intent the user enters at creation/setup time.
func workspaceBootstrapField(sharedData map[string]any, key string) string {
	if len(sharedData) == 0 {
		return ""
	}
	raw, ok := sharedData["workspace_bootstrap"]
	if !ok || raw == nil {
		return ""
	}
	bootstrap, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	value, ok := bootstrap[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
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

// buildTaskPrompt creates the rendered (unbudgeted) task user-prompt. Callers
// that need token budgeting use buildTaskPromptSegments + budgetPromptSegments
// and render the result themselves; this convenience wrapper keeps the previous
// behavior for any caller that just wants the string.
func (h *LLMTaskHandler) buildTaskPrompt(ctx context.Context, task Task) string {
	return renderPromptSegments(h.buildTaskPromptSegments(ctx, task))
}

// buildTaskPromptSegments assembles the task user-prompt as ordered, labeled
// segments. The task description, output-format instructions, and framing live
// in protected segments (trimOrder 0); memory, workspace snapshot, attachments,
// and upstream results carry trim ranks so the budget pass can shrink them in a
// fixed order (WS2.6) without dropping load-bearing content. Concatenating the
// segments in order reproduces the previous single-string prompt exactly when no
// trimming occurs.
func (h *LLMTaskHandler) buildTaskPromptSegments(ctx context.Context, task Task) []promptSegment {
	segs := make([]promptSegment, 0, 6)

	// Protected header: assignment framing, description, details, reference URL,
	// user profile.
	var header strings.Builder
	header.WriteString("# Task Assignment\n\n")
	header.WriteString("You have been assigned a task in a collaborative workspace.\n\n")
	fmt.Fprintf(&header, "**Task ID**: %s\n", task.ID)
	fmt.Fprintf(&header, "**From**: %s\n", task.From)
	fmt.Fprintf(&header, "**Priority**: %d/5\n\n", task.Priority)

	processedDescription := h.substitutePlaceholders(task)
	fmt.Fprintf(&header, "## Task Description\n\n%s\n\n", processedDescription)

	if details := strings.TrimSpace(task.Details); details != "" {
		header.WriteString("## Task Details\n\n")
		header.WriteString(details)
		header.WriteString("\n\n")
	}

	if referenceURL := strings.TrimSpace(task.ReferenceURL); referenceURL != "" {
		header.WriteString("## Reference URL\n\n")
		header.WriteString(referenceURL)
		header.WriteString("\n\n")
		header.WriteString("Treat this URL as authoritative source material for this task. Inspect it with available fetch, browser, or web tools before making claims about its contents. If you cannot inspect it, state that limitation in your final response.\n\n")
	}

	if userProfile := h.buildUserProfileSection(ctx, h.taskOwnerUserID(task)); userProfile != "" {
		header.WriteString(userProfile)
		header.WriteString("\n\n")
	}
	segs = append(segs, promptSegment{label: "header", text: header.String(), trimOrder: 0})

	// Workspace memory — trimmed last (WS2.6 d).
	if workspaceMemory := h.buildTaskMemorySection(task); workspaceMemory != "" {
		segs = append(segs, promptSegment{label: "memory", text: workspaceMemory + "\n\n", trimOrder: 4})
	}

	// Workspace snapshot (task/file/note/session lists) — WS2.6 c.
	if workspaceSnapshot := h.buildTaskWorkspaceSnapshot(ctx, task); workspaceSnapshot != "" {
		segs = append(segs, promptSegment{label: "workspace_snapshot", text: workspaceSnapshot + "\n\n", trimOrder: 3})
	}

	// Attached file contents — trimmed first (WS2.6 a).
	if attachmentSection := h.buildAttachmentsSection(task); attachmentSection != "" {
		segs = append(segs, promptSegment{label: "attachments", text: attachmentSection, trimOrder: 1})
	}

	// Upstream task results / structured outputs — WS2.6 b. Runtime inputs
	// (rebuilt each execution) live on task.RuntimeInputs; structured outputs from
	// upstream tasks with an OutputSchema render as JSON alongside the raw text.
	if task.RuntimeInputs != nil {
		var inputs strings.Builder
		h.formatInputResults(&inputs, task.RuntimeInputs)
		if inputs.Len() > 0 {
			segs = append(segs, promptSegment{label: "upstream_inputs", text: inputs.String(), trimOrder: 2})
		}
	}

	// Protected tail: authored context, required output format, time limit, and
	// the closing instructions.
	var tail strings.Builder
	if suggestionID, _ := task.Context["assistant_suggestion_id"].(string); strings.TrimSpace(suggestionID) != "" {
		tail.WriteString("## Assistant Suggestion Safety Gate\n\n")
		tail.WriteString("This task began as an assistant recommendation, not authority to mutate the project. Before any filesystem, project, DAW, or live-control mutation, use the ordinary host confirmation flow and re-check every required capability. If confirmation or readiness is absent, stop and report the blocker; never substitute another mutation path.\n\n")
	}
	if len(task.Context) > 0 {
		tail.WriteString("## Additional Context\n\n")
		for key, value := range task.Context {
			fmt.Fprintf(&tail, "- **%s**: %v\n", key, value)
		}
		tail.WriteString("\n")
	}

	if outputInstructions := BuildTaskOutputSpecPrompt(ActiveTaskOutputSpec(&task)); outputInstructions != "" {
		tail.WriteString("## Required Output Format\n\n")
		tail.WriteString(outputInstructions)
		tail.WriteString("\n\n")
	} else if outputInstructions := BuildTaskOutputSchemaPrompt(task.OutputSchema); outputInstructions != "" {
		tail.WriteString("## Required Output Format\n\n")
		tail.WriteString(outputInstructions)
		tail.WriteString("\n\n")
	} else if outputInstructions := BuildTaskOutputContractPrompt(task.OutputContract); outputInstructions != "" {
		tail.WriteString("## Required Output Format\n\n")
		tail.WriteString(outputInstructions)
		tail.WriteString("\n\n")
	}

	if task.Timeout > 0 {
		fmt.Fprintf(&tail, "**Time Limit**: %v\n\n", task.Timeout)
	}

	tail.WriteString("Please complete this task to the best of your ability. ")
	tail.WriteString("**Important**: Only use tools when they are explicitly necessary to complete the task. ")
	tail.WriteString("For informational requests, meta-commands (like /tools, /help), or simple questions, ")
	tail.WriteString("respond directly without calling tools. ")
	if ActiveTaskOutputSpec(&task) != nil || NormalizeTaskOutputSchema(task.OutputSchema) != nil || NormalizeTaskOutputContract(task.OutputContract) != nil {
		tail.WriteString("When you are done, your final answer must be the JSON object described above and nothing else.")
	} else {
		tail.WriteString("Provide a clear, concise response with your findings or results.")
	}
	segs = append(segs, promptSegment{label: "tail", text: tail.String(), trimOrder: 0})

	return segs
}

// buildAttachmentsSection renders the "Attached Files" prompt block, or "" when
// the task has no attachments. Attachment contents are capped (per-file and
// total) before injection (WS2.8) so a large file cannot blow the context window
// before budgeting even runs.
func (h *LLMTaskHandler) buildAttachmentsSection(task Task) string {
	attachmentContents := h.getAttachedFileContents(task)
	if len(attachmentContents) == 0 {
		return ""
	}

	var prompt strings.Builder
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
	return prompt.String()
}
