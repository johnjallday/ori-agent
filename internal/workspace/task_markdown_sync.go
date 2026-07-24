package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
	"gopkg.in/yaml.v3"
)

const taskMarkdownSchemaVersion = 1

var (
	taskMarkdownLinePattern = regexp.MustCompile(`^(\s*)-\s+\[([ xX])\]\s+(.*)$`)
	taskMarkdownMetaPattern = regexp.MustCompile(`<!--\s*ori:([^>]*)-->`)
	taskMarkdownAgentToken  = regexp.MustCompile(`(?:^|\s)@([A-Za-z0-9_.-]+)\s*$`)
)

type taskMarkdownFrontmatter struct {
	Type          string                      `yaml:"type"`
	SchemaVersion int                         `yaml:"schema_version"`
	WorkspaceID   string                      `yaml:"workspace_id"`
	UpdatedAt     string                      `yaml:"updated_at"`
	MarkdownSync  taskMarkdownSyncFrontmatter `yaml:"markdown_sync"`
}

type taskMarkdownSyncFrontmatter struct {
	LastSyncedAt string `yaml:"last_synced_at"`
	ContentHash  string `yaml:"content_hash"`
}

type TaskMarkdownImportResult struct {
	Changed  bool
	Warnings []string
}

type taskMarkdownItem struct {
	ID             string
	Description    string
	Checked        bool
	Depth          int
	LineIndex      int
	ParentLine     int
	ParentTaskID   string
	SubtaskIndex   int
	To             string
	AssignedNodeID string
	InputTaskIDs   []string
	Tags           []string
	Mode           string
}

type workspaceFolderStore interface {
	GetFolderPath(workspaceID string) (string, error)
}

type workspaceFileSyncStore interface {
	FileStore() *FileStore
}

func SyncTaskMarkdownFiles(store Store, ws *Workspace) error {
	if ws == nil {
		return nil
	}
	settings := workspacesettings.Extract(ws.SharedData).TaskMarkdown
	if !settings.Enabled {
		return nil
	}
	if err := workspacesettings.ValidateTaskMarkdownPath(settings.Path); err != nil {
		return err
	}
	folder, ok, err := workspaceFolderForTaskMarkdown(store, ws.ID)
	if err != nil || !ok {
		return err
	}
	return SyncTaskMarkdownFilesToFolder(folder, ws, settings)
}

func syncTaskMarkdownFilesToFolderIfEnabled(folder string, ws *Workspace) error {
	if ws == nil {
		return nil
	}
	settings := workspacesettings.Extract(ws.SharedData).TaskMarkdown
	if !settings.Enabled {
		return nil
	}
	if err := workspacesettings.ValidateTaskMarkdownPath(settings.Path); err != nil {
		return err
	}
	return SyncTaskMarkdownFilesToFolder(folder, ws, settings)
}

func SyncTaskMarkdownFilesToFolder(folder string, ws *Workspace, settings workspacesettings.TaskMarkdownSettings) error {
	if ws == nil || !settings.Enabled {
		return nil
	}
	taskPath, err := safeWorkspaceMarkdownPath(folder, settings.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(taskPath), 0755); err != nil {
		return fmt.Errorf("create task markdown directory: %w", err)
	}
	if err := atomicWriteFile(taskPath, []byte(RenderWorkspaceTasksMarkdown(ws))); err != nil {
		return fmt.Errorf("write task markdown: %w", err)
	}
	if settings.GenerateAgentViews {
		if err := writeAgentTaskViews(folder, ws); err != nil {
			return err
		}
	}
	return nil
}

func ImportTaskMarkdownFromStore(store Store, ws *Workspace) (*TaskMarkdownImportResult, error) {
	result := &TaskMarkdownImportResult{}
	if ws == nil {
		return result, nil
	}
	settings := workspacesettings.Extract(ws.SharedData).TaskMarkdown
	if !settings.Enabled {
		return result, nil
	}
	if err := workspacesettings.ValidateTaskMarkdownPath(settings.Path); err != nil {
		return nil, err
	}
	folder, ok, err := workspaceFolderForTaskMarkdown(store, ws.ID)
	if err != nil || !ok {
		return result, err
	}
	taskPath, err := safeWorkspaceMarkdownPath(folder, settings.Path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(taskPath)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, fmt.Errorf("read task markdown: %w", err)
	}
	if warning := taskMarkdownConflictWarning(string(data), ws.UpdatedAt); warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	items, warnings, err := ParseWorkspaceTasksMarkdown(string(data), ws.ID)
	if err != nil {
		return nil, err
	}
	result.Warnings = append(result.Warnings, warnings...)
	result.Changed = applyMarkdownItemsToWorkspace(ws, items, &result.Warnings)
	return result, nil
}

// TaskMarkdownStatusForSettings reports filesystem status for a workspace's Markdown task map.
func TaskMarkdownStatusForSettings(store Store, workspaceID string, settings workspacesettings.TaskMarkdownSettings) map[string]any {
	status := map[string]any{
		"enabled":       settings.Enabled,
		"path":          settings.Path,
		"status":        "disabled",
		"exists":        false,
		"warning_count": 0,
	}
	if !settings.Enabled {
		return status
	}
	if err := workspacesettings.ValidateTaskMarkdownPath(settings.Path); err != nil {
		status["status"] = "invalid_path"
		status["warning_count"] = 1
		status["message"] = err.Error()
		return status
	}
	folder, ok, err := workspaceFolderForTaskMarkdown(store, workspaceID)
	if err != nil {
		status["status"] = "unavailable"
		status["message"] = err.Error()
		return status
	}
	if !ok {
		status["status"] = "unavailable"
		status["message"] = "workspace folder is not available"
		return status
	}
	path, err := safeWorkspaceMarkdownPath(folder, settings.Path)
	if err != nil {
		status["status"] = "invalid_path"
		status["warning_count"] = 1
		status["message"] = err.Error()
		return status
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			status["status"] = "missing"
			status["message"] = "task markdown file has not been written yet"
			return status
		}
		status["status"] = "unavailable"
		status["message"] = err.Error()
		return status
	}
	status["status"] = "ready"
	status["exists"] = true
	status["last_sync_time"] = info.ModTime().UTC().Format(time.RFC3339)
	return status
}

func RenderWorkspaceTasksMarkdown(ws *Workspace) string {
	if ws == nil {
		return ""
	}

	body := renderWorkspaceTasksMarkdownBody(ws)
	now := time.Now().UTC().Format(time.RFC3339)
	hash := sha256.Sum256([]byte(body))
	frontmatter := taskMarkdownFrontmatter{
		Type:          "ori_workspace_tasks",
		SchemaVersion: taskMarkdownSchemaVersion,
		WorkspaceID:   ws.ID,
		UpdatedAt:     now,
		MarkdownSync: taskMarkdownSyncFrontmatter{
			LastSyncedAt: now,
			ContentHash:  "sha256:" + hex.EncodeToString(hash[:]),
		},
	}
	fmData, err := yaml.Marshal(frontmatter)
	if err != nil {
		return body
	}
	return "---\n" + string(fmData) + "---\n\n" + body
}

func ParseWorkspaceTasksMarkdown(content, workspaceID string) ([]taskMarkdownItem, []string, error) {
	frontmatter, body, err := splitTaskMarkdownFrontmatter(content)
	if err != nil {
		return nil, nil, err
	}
	var fm taskMarkdownFrontmatter
	var warnings []string
	if strings.TrimSpace(frontmatter) != "" {
		if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
			return nil, nil, fmt.Errorf("parse task markdown frontmatter: %w", err)
		}
		if fm.Type != "" && fm.Type != "ori_workspace_tasks" {
			return nil, nil, fmt.Errorf("unsupported task markdown type %q", fm.Type)
		}
		if fm.WorkspaceID != "" && workspaceID != "" && fm.WorkspaceID != workspaceID {
			return nil, nil, fmt.Errorf("task markdown workspace_id %q does not match %q", fm.WorkspaceID, workspaceID)
		}
		if warning := taskMarkdownHashWarning(fm, body); warning != "" {
			warnings = append(warnings, warning)
		}
	}

	var items []taskMarkdownItem
	parentByDepth := map[int]int{}
	siblingIndexByParent := map[int]int{}
	lines := strings.Split(body, "\n")
	for lineNumber, line := range lines {
		match := taskMarkdownLinePattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		depth := len(strings.ReplaceAll(match[1], "\t", "  ")) / 2
		rawText := strings.TrimSpace(match[3])
		metaText := ""
		if metaMatch := taskMarkdownMetaPattern.FindStringSubmatch(rawText); metaMatch != nil {
			metaText = metaMatch[1]
			rawText = strings.TrimSpace(taskMarkdownMetaPattern.ReplaceAllString(rawText, ""))
		}
		item := taskMarkdownItem{
			Checked:   strings.EqualFold(match[2], "x"),
			Depth:     depth,
			LineIndex: len(items),
		}
		meta := parseTaskMarkdownMetadata(metaText)
		item.ID = meta["id"]
		item.ParentTaskID = meta["parent"]
		item.AssignedNodeID = meta["assigned_node_id"]
		item.Mode = meta["mode"]
		item.To = resolveTaskMarkdownAssignee(meta["to"], parseTaskMarkdownAgent(rawText))
		item.InputTaskIDs = splitTaskMarkdownIDs(meta["depends"])
		item.Tags = splitTaskMarkdownIDs(meta["tags"])
		if item.SubtaskIndex == 0 {
			item.SubtaskIndex = intFromString(meta["index"])
		}
		item.Description = cleanTaskMarkdownDescription(rawText)
		if item.Description == "" {
			warnings = append(warnings, fmt.Sprintf("line %d has an empty task description", lineNumber+1))
			continue
		}
		parentLine := -1
		if depth > 0 {
			if parent, ok := parentByDepth[depth-1]; ok {
				parentLine = parent
			}
		}
		item.ParentLine = parentLine
		if item.ParentTaskID == "" && parentLine >= 0 && parentLine < len(items) {
			item.ParentTaskID = items[parentLine].ID
		}
		if item.SubtaskIndex == 0 && parentLine >= 0 {
			siblingIndexByParent[parentLine]++
			item.SubtaskIndex = siblingIndexByParent[parentLine]
		}
		items = append(items, item)
		parentByDepth[depth] = item.LineIndex
		for existingDepth := range parentByDepth {
			if existingDepth > depth {
				delete(parentByDepth, existingDepth)
			}
		}
	}
	return items, warnings, nil
}

func taskMarkdownConflictWarning(content string, workspaceUpdatedAt time.Time) string {
	frontmatter, body, err := splitTaskMarkdownFrontmatter(content)
	if err != nil || strings.TrimSpace(frontmatter) == "" {
		return ""
	}
	var fm taskMarkdownFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		return ""
	}
	if taskMarkdownHashWarning(fm, body) == "" {
		return ""
	}
	lastSyncedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(fm.MarkdownSync.LastSyncedAt))
	if err != nil {
		return ""
	}
	if !workspaceUpdatedAt.IsZero() && workspaceUpdatedAt.After(lastSyncedAt) {
		return "workspace and tasks.md both changed after the last Markdown sync; runtime-only task fields were preserved"
	}
	return ""
}

func taskMarkdownHashWarning(fm taskMarkdownFrontmatter, body string) string {
	expected := strings.TrimSpace(strings.TrimPrefix(fm.MarkdownSync.ContentHash, "sha256:"))
	if expected == "" {
		return ""
	}
	actualHash := sha256.Sum256([]byte(body))
	actual := hex.EncodeToString(actualHash[:])
	if !strings.EqualFold(expected, actual) {
		return "tasks.md content differs from the last Markdown sync hash"
	}
	return ""
}

func renderWorkspaceTasksMarkdownBody(ws *Workspace) string {
	tasks := append([]Task(nil), ws.Tasks...)
	sortTasksForMarkdown(tasks)

	children := map[string][]Task{}
	taskIDs := map[string]struct{}{}
	for _, task := range tasks {
		taskIDs[task.ID] = struct{}{}
	}

	var activeRoots []Task
	var doneRoots []Task
	for _, task := range tasks {
		// Backlog items are excluded from tasks.md entirely (PRD
		// workspace-backlog FR90): they are not yet committed work and are
		// synchronized separately through BACKLOG.md. Once promoted to
		// Ready, a task is no longer Backlog and participates here normally.
		if task.Status == TaskStatusBacklog {
			continue
		}
		if task.ParentTaskID != "" {
			if _, ok := taskIDs[task.ParentTaskID]; ok {
				children[task.ParentTaskID] = append(children[task.ParentTaskID], task)
				continue
			}
		}
		if task.Status == TaskStatusCompleted {
			doneRoots = append(doneRoots, task)
		} else {
			activeRoots = append(activeRoots, task)
		}
	}
	for parentID := range children {
		sortTasksForMarkdown(children[parentID])
	}

	var sb strings.Builder
	sb.WriteString("# Tasks\n\n")
	sb.WriteString("<!-- Generated by Ori. Edit checkboxes, task text, @agent tags, and ori metadata in this file. Use the task detail page for schedules, results, execution, and workflow controls. -->\n\n")
	sb.WriteString("## Active\n\n")
	if len(activeRoots) == 0 {
		sb.WriteString("_No active tasks._\n")
	} else {
		for _, task := range activeRoots {
			writeTaskMarkdownLine(&sb, ws.ID, task, children, 0)
		}
	}
	sb.WriteString("\n## Done\n\n")
	if len(doneRoots) == 0 {
		sb.WriteString("_No completed tasks._\n")
	} else {
		for _, task := range doneRoots {
			writeTaskMarkdownLine(&sb, ws.ID, task, children, 0)
		}
	}
	return sb.String()
}

func writeTaskMarkdownLine(sb *strings.Builder, workspaceID string, task Task, children map[string][]Task, depth int) {
	indent := strings.Repeat("  ", depth)
	checked := " "
	if task.Status == TaskStatusCompleted {
		checked = "x"
	}
	description := strings.TrimSpace(strings.ReplaceAll(task.Description, "\n", " "))
	if description == "" {
		description = "Untitled task"
	}
	agentToken := ""
	if to := strings.TrimSpace(task.To); to != "" && to != "unassigned" {
		agentToken = " @" + Slugify(to)
	}
	fmt.Fprintf(sb, "%s- [%s] %s%s <!-- %s -->\n", indent, checked, description, agentToken, buildTaskMarkdownMetadata(workspaceID, task))
	for _, child := range children[task.ID] {
		writeTaskMarkdownLine(sb, workspaceID, child, children, depth+1)
	}
}

func buildTaskMarkdownMetadata(workspaceID string, task Task) string {
	parts := []string{"ori:id=" + escapeTaskMarkdownValue(task.ID)}
	if task.ParentTaskID != "" {
		parts = append(parts, "parent="+escapeTaskMarkdownValue(task.ParentTaskID))
	}
	if task.SubtaskIndex > 0 {
		parts = append(parts, fmt.Sprintf("index=%d", task.SubtaskIndex))
	}
	if len(task.InputTaskIDs) > 0 {
		parts = append(parts, "depends="+escapeTaskMarkdownValue(strings.Join(task.InputTaskIDs, ",")))
	}
	if len(task.Tags) > 0 {
		parts = append(parts, "tags="+escapeTaskMarkdownValue(strings.Join(task.Tags, ",")))
	}
	if mode := strings.TrimSpace(string(task.OrchestrationMode)); mode != "" {
		parts = append(parts, "mode="+escapeTaskMarkdownValue(mode))
	}
	if to := strings.TrimSpace(task.To); to != "" {
		parts = append(parts, "to="+escapeTaskMarkdownValue(to))
	}
	if assigned := strings.TrimSpace(task.AssignedNodeID); assigned != "" {
		parts = append(parts, "assigned_node_id="+escapeTaskMarkdownValue(assigned))
	}
	parts = append(parts, "url="+escapeTaskMarkdownValue(fmt.Sprintf("/workspaces/%s/task/%s", workspaceID, task.ID)))
	return strings.Join(parts, " ")
}

func applyMarkdownItemsToWorkspace(ws *Workspace, items []taskMarkdownItem, warnings *[]string) bool {
	changed := false
	lineToTaskID := map[int]string{}

	for i := range items {
		item := items[i]
		if item.ID != "" {
			if _, err := ws.GetTask(item.ID); err == nil {
				lineToTaskID[item.LineIndex] = item.ID
				continue
			}
		}
		task := Task{
			Description: item.Description,
			To:          item.To,
			Status:      TaskStatusPending,
			Context:     map[string]any{},
		}
		if item.Checked {
			// Markdown-import: a [x] line creates a task that's already Completed.
			// This is a Pending → Completed transition in our state machine,
			// which is legal (markdown checkbox tick).
			now := time.Now()
			if err := task.SetStatus(TaskStatusCompleted); err != nil {
				*warnings = append(*warnings, fmt.Sprintf("markdown import status transition rejected: %v", err))
				continue
			}
			task.CompletedAt = &now
		}
		// Default to the workspace coordinator (entry agent) when the markdown
		// line named no assignee; a no-op when the line specified one, so
		// markdown-declared assignees are preserved.
		ws.ApplyEntryAgentDefault(&task)
		if err := ws.AddTask(task); err != nil {
			*warnings = append(*warnings, fmt.Sprintf("failed to add task from line %d: %v", item.LineIndex+1, err))
			continue
		}
		created := ws.Tasks[len(ws.Tasks)-1]
		items[i].ID = created.ID
		lineToTaskID[item.LineIndex] = created.ID
		changed = true
	}

	for _, item := range items {
		taskID := item.ID
		if taskID == "" {
			taskID = lineToTaskID[item.LineIndex]
		}
		if taskID == "" {
			continue
		}
		task, err := ws.GetTask(taskID)
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("task %s from markdown was not found", taskID))
			continue
		}

		// applyItem encodes how a markdown item modifies a task. It is run twice:
		// once on a snapshot to detect whether anything actually changed (so we
		// don't bump UpdatedAt on no-op syncs), and once inside MutateTask
		// against the live slice element (so concurrent mutations are observed).
		applyItem := func(t *Task) {
			if t.Context == nil {
				t.Context = map[string]any{}
			}
			if item.Description != "" && item.Description != t.Description {
				t.Description = item.Description
			}
			if item.To != "" && item.To != t.To {
				t.To = item.To
			}
			if item.AssignedNodeID != "" && item.AssignedNodeID != t.AssignedNodeID {
				t.AssignedNodeID = item.AssignedNodeID
			}
			parentID := item.ParentTaskID
			if parentID == "" && item.ParentLine >= 0 {
				parentID = lineToTaskID[item.ParentLine]
			}
			if parentID != t.ParentTaskID {
				t.ParentTaskID = parentID
			}
			if parentID != "" && item.SubtaskIndex > 0 && item.SubtaskIndex != t.SubtaskIndex {
				t.SubtaskIndex = item.SubtaskIndex
			}
			if item.Mode != "" {
				mode := NormalizeTaskOrchestrationMode(item.Mode)
				if mode != t.OrchestrationMode {
					t.OrchestrationMode = mode
				}
			}
			if item.InputTaskIDs != nil && !stringSlicesEqual(item.InputTaskIDs, t.InputTaskIDs) {
				t.InputTaskIDs = item.InputTaskIDs
			}
			if item.Tags != nil {
				if tags := NormalizeWorkspaceTags(item.Tags); !stringSlicesEqual(tags, t.Tags) {
					t.Tags = tags
				}
			}
			if item.Checked && t.Status != TaskStatusCompleted {
				// User edited the markdown checkbox to checked. This is a manual
				// override — the prior state could be anything (Failed,
				// Cancelled, WaitingForChoice...) and the user is declaring it
				// done. ForceStatus bypasses the lifecycle table by design.
				now := time.Now()
				t.ForceStatus(TaskStatusCompleted)
				if t.CompletedAt == nil {
					t.CompletedAt = &now
				}
			}
			if !item.Checked && t.Status == TaskStatusCompleted {
				// Un-check: Completed → Pending is a legal reset path.
				if err := t.SetStatus(TaskStatusPending); err != nil {
					// Fall through to ForceStatus to preserve legacy behavior;
					// SetStatus only fails here if the table changes underneath.
					t.ForceStatus(TaskStatusPending)
				}
				t.CompletedAt = nil
			}
		}

		next := *task
		applyItem(&next)
		if tasksEqualForMarkdownUpdate(*task, next) {
			continue
		}

		if err := ws.MutateTask(taskID, func(t *Task) error {
			applyItem(t)
			return nil
		}); err != nil {
			*warnings = append(*warnings, fmt.Sprintf("failed to update task %s: %v", taskID, err))
			continue
		}
		changed = true
	}
	if changed {
		ws.UpdatedAt = time.Now()
		if err := ws.ValidateTaskGraph(); err != nil {
			if gErr, ok := err.(*TaskGraphError); ok {
				for _, issue := range gErr.Issues {
					*warnings = append(*warnings, "markdown import created invalid task graph: "+issue.Message)
				}
			} else {
				*warnings = append(*warnings, "markdown import created invalid task graph: "+err.Error())
			}
		}
	}
	return changed
}

func writeAgentTaskViews(folder string, ws *Workspace) error {
	byAgent := map[string][]Task{}
	for _, task := range ws.Tasks {
		agentName := strings.TrimSpace(task.To)
		if agentName == "" || agentName == "unassigned" {
			continue
		}
		byAgent[agentName] = append(byAgent[agentName], task)
	}
	for _, inst := range ws.AgentInstances {
		name := strings.TrimSpace(inst.Name)
		if name != "" {
			if _, ok := byAgent[name]; !ok {
				byAgent[name] = nil
			}
		}
	}
	for agentName, tasks := range byAgent {
		dir, err := workspaceAgentDir(folder, agentName)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create agent task view dir: %w", err)
		}
		sortTasksForMarkdown(tasks)
		content := renderAgentTaskView(ws.ID, agentName, tasks)
		if err := atomicWriteFile(filepath.Join(dir, "tasks.md"), []byte(content)); err != nil {
			return fmt.Errorf("write agent task view for %s: %w", agentName, err)
		}
	}
	return nil
}

func renderAgentTaskView(workspaceID, agentName string, tasks []Task) string {
	var active []Task
	var done []Task
	for _, task := range tasks {
		if task.Status == TaskStatusCompleted {
			done = append(done, task)
		} else {
			active = append(active, task)
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s Tasks\n\n", strings.TrimSpace(agentName))
	sb.WriteString("<!-- Generated by Ori from the canonical workspace tasks.md. Edit canonical tasks.md or open the task detail page for changes. -->\n\n")
	sb.WriteString("## Active\n\n")
	writeAgentTaskViewSection(&sb, workspaceID, active)
	sb.WriteString("\n## Done\n\n")
	writeAgentTaskViewSection(&sb, workspaceID, done)
	return sb.String()
}

func writeAgentTaskViewSection(sb *strings.Builder, workspaceID string, tasks []Task) {
	if len(tasks) == 0 {
		sb.WriteString("_No tasks._\n")
		return
	}
	for _, task := range tasks {
		checked := " "
		if task.Status == TaskStatusCompleted {
			checked = "x"
		}
		description := strings.TrimSpace(strings.ReplaceAll(task.Description, "\n", " "))
		if description == "" {
			description = "Untitled task"
		}
		taskURL := fmt.Sprintf("/workspaces/%s/task/%s", workspaceID, task.ID)
		fmt.Fprintf(sb, "- [%s] %s ([open](%s)) <!-- ori:id=%s -->\n", checked, description, taskURL, escapeTaskMarkdownValue(task.ID))
	}
}

func workspaceFolderForTaskMarkdown(store Store, workspaceID string) (string, bool, error) {
	if store == nil {
		return "", false, nil
	}
	switch typed := store.(type) {
	case *FileStore:
		if typed == nil {
			return "", false, nil
		}
	case *SyncStore:
		if typed == nil {
			return "", false, nil
		}
	}
	if withFolder, ok := store.(workspaceFolderStore); ok {
		folder, err := withFolder.GetFolderPath(workspaceID)
		return folder, err == nil, err
	}
	if withFileSync, ok := store.(workspaceFileSyncStore); ok {
		if fs := withFileSync.FileStore(); fs != nil {
			folder, err := fs.GetFolderPath(workspaceID)
			return folder, err == nil, err
		}
	}
	return "", false, nil
}

func safeWorkspaceMarkdownPath(folder, relPath string) (string, error) {
	cleanRel := filepath.Clean(filepath.FromSlash(relPath))
	if filepath.IsAbs(cleanRel) || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || cleanRel == ".." {
		return "", fmt.Errorf("task markdown path escapes workspace folder")
	}
	fullPath := filepath.Join(folder, cleanRel)
	absFolder, err := filepath.Abs(folder)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absFolder, absPath)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("task markdown path escapes workspace folder")
	}
	return absPath, nil
}

// ResolveTaskMarkdownPath resolves a configured Markdown task path inside a workspace folder.
func ResolveTaskMarkdownPath(folder, relPath string) (string, error) {
	return safeWorkspaceMarkdownPath(folder, relPath)
}

func splitTaskMarkdownFrontmatter(content string) (frontmatter, body string, err error) {
	trimmed := strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(trimmed, "---\n") {
		return "", trimmed, nil
	}
	rest := strings.TrimPrefix(trimmed, "---\n")
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return "", "", fmt.Errorf("task markdown frontmatter is missing closing delimiter")
	}
	frontmatter = strings.TrimSpace(rest[:endIdx])
	body = strings.TrimLeft(rest[endIdx+len("\n---"):], "\r\n")
	return frontmatter, body, nil
}

func parseTaskMarkdownMetadata(raw string) map[string]string {
	out := map[string]string{}
	for _, token := range strings.Fields(raw) {
		token = strings.TrimSpace(token)
		if token == "" || token == "ori:" {
			continue
		}
		token = strings.TrimPrefix(token, "ori:")
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			continue
		}
		if unescaped, err := url.QueryUnescape(value); err == nil {
			value = unescaped
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func escapeTaskMarkdownValue(value string) string {
	return url.QueryEscape(strings.TrimSpace(value))
}

func cleanTaskMarkdownDescription(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = taskMarkdownAgentToken.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = regexp.MustCompile(`\s+`).ReplaceAllString(raw, " ")
	return raw
}

func parseTaskMarkdownAgent(raw string) string {
	match := taskMarkdownAgentToken.FindStringSubmatch(raw)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func resolveTaskMarkdownAssignee(metaTo, visibleAgent string) string {
	metaTo = strings.TrimSpace(metaTo)
	visibleAgent = strings.TrimSpace(visibleAgent)
	if visibleAgent == "" {
		return metaTo
	}
	if metaTo != "" && Slugify(metaTo) == visibleAgent {
		return metaTo
	}
	return visibleAgent
}

func splitTaskMarkdownIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func intFromString(raw string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(raw))
	return value
}

func sortTasksForMarkdown(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].SubtaskIndex > 0 && tasks[j].SubtaskIndex > 0 && tasks[i].SubtaskIndex != tasks[j].SubtaskIndex {
			return tasks[i].SubtaskIndex < tasks[j].SubtaskIndex
		}
		if !tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		}
		return tasks[i].ID < tasks[j].ID
	})
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func tasksEqualForMarkdownUpdate(a, b Task) bool {
	return a.Description == b.Description &&
		a.To == b.To &&
		a.AssignedNodeID == b.AssignedNodeID &&
		a.ParentTaskID == b.ParentTaskID &&
		a.SubtaskIndex == b.SubtaskIndex &&
		a.OrchestrationMode == b.OrchestrationMode &&
		a.Status == b.Status &&
		((a.CompletedAt == nil && b.CompletedAt == nil) ||
			(a.CompletedAt != nil && b.CompletedAt != nil && a.CompletedAt.Equal(*b.CompletedAt))) &&
		stringSlicesEqual(a.InputTaskIDs, b.InputTaskIDs) &&
		stringSlicesEqual(a.Tags, b.Tags)
}

func LogTaskMarkdownWarnings(workspaceID string, warnings []string) {
	for _, warning := range warnings {
		logger.Warn("Task markdown sync warning", logger.Fields{
			"workspace_id": workspaceID,
			"warning":      warning,
		})
	}
}
