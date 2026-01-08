package chathttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// HandleWorkspace handles the /workspace command and subcommands
func (ch *CommandHandler) HandleWorkspace(w http.ResponseWriter, r *http.Request, args string) {
	w.Header().Set("Content-Type", "application/json")

	// Check if workspace store is available
	if ch.workspaceStore == nil {
		response := map[string]any{
			"response": "❌ Workspace functionality is not available.",
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Get current agent
	_, current := ch.store.ListAgents()
	if current == "" {
		response := map[string]any{
			"response": "❌ No active agent found.",
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Create agent context
	agentCtx := workspace.NewAgentContext(current, ch.workspaceStore)

	// Parse subcommand
	args = strings.TrimSpace(args)
	parts := strings.Fields(args)

	// If no args, show workspace summary
	if len(parts) == 0 {
		ch.handleWorkspaceSummary(w, agentCtx)
		return
	}

	subcommand := parts[0]

	switch subcommand {
	case "tasks":
		ch.handleWorkspaceTasks(w, agentCtx)
	case "task":
		ch.handleWorkspaceTaskDetail(w, agentCtx, parts)
	case "all":
		ch.handleWorkspaceAllTasks(w, agentCtx)
	default:
		ch.handleWorkspaceUnknown(w, subcommand)
	}
}

func (ch *CommandHandler) handleWorkspaceSummary(w http.ResponseWriter, agentCtx *workspace.AgentContext) {
	summary, err := agentCtx.GetWorkspaceSummary()
	if err != nil {
		response := map[string]any{
			"response": fmt.Sprintf("❌ Failed to get workspace summary: %v", err),
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	response := map[string]any{
		"response": summary,
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

func (ch *CommandHandler) handleWorkspaceTasks(w http.ResponseWriter, agentCtx *workspace.AgentContext) {
	tasksSummary, err := agentCtx.GetTasksSummary()
	if err != nil {
		response := map[string]any{
			"response": fmt.Sprintf("❌ Failed to get tasks: %v", err),
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	response := map[string]any{
		"response": tasksSummary,
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

func (ch *CommandHandler) handleWorkspaceTaskDetail(w http.ResponseWriter, agentCtx *workspace.AgentContext, parts []string) {
	if len(parts) < 2 {
		response := map[string]any{
			"response": "❌ Please provide a task ID. Usage: `/workspace task <task-id>`",
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	taskID := parts[1]
	details, err := agentCtx.GetTaskDetails(taskID)
	if err != nil {
		response := map[string]any{
			"response": fmt.Sprintf("❌ Failed to get task details: %v", err),
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	response := map[string]any{
		"response": details,
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

func (ch *CommandHandler) handleWorkspaceAllTasks(w http.ResponseWriter, agentCtx *workspace.AgentContext) {
	allTasks, err := agentCtx.GetAllTasks()
	if err != nil {
		response := map[string]any{
			"response": fmt.Sprintf("❌ Failed to get tasks: %v", err),
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	if len(allTasks) == 0 {
		response := map[string]any{
			"response": "You have no tasks in any workspace.",
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## All Your Tasks (%d)\n\n", len(allTasks)))

	// Group by status
	byStatus := make(map[workspace.TaskStatus][]workspace.Task)
	for _, task := range allTasks {
		byStatus[task.Status] = append(byStatus[task.Status], task)
	}

	statuses := []workspace.TaskStatus{
		workspace.TaskStatusPending,
		workspace.TaskStatusAssigned,
		workspace.TaskStatusInProgress,
		workspace.TaskStatusCompleted,
		workspace.TaskStatusFailed,
		workspace.TaskStatusCancelled,
		workspace.TaskStatusTimeout,
	}

	for _, status := range statuses {
		tasks := byStatus[status]
		if len(tasks) == 0 {
			continue
		}

		// Capitalize first letter manually (strings.Title is deprecated)
		statusStr := string(status)
		capitalizedStatus := statusStr
		if len(statusStr) > 0 && statusStr[0] >= 'a' && statusStr[0] <= 'z' {
			capitalizedStatus = strings.ToUpper(statusStr[:1]) + statusStr[1:]
		}
		sb.WriteString(fmt.Sprintf("### %s (%d)\n\n", capitalizedStatus, len(tasks)))
		for i, task := range tasks {
			sb.WriteString(fmt.Sprintf("%d. **%s** (`%s`)\n", i+1, task.Description, task.ID))
			sb.WriteString(fmt.Sprintf("   - From: %s | Priority: %d/5\n", task.From, task.Priority))
		}
		sb.WriteString("\n")
	}

	response := map[string]any{
		"response": sb.String(),
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

func (ch *CommandHandler) handleWorkspaceUnknown(w http.ResponseWriter, subcommand string) {
	response := map[string]any{
		"response": fmt.Sprintf("❌ Unknown workspace command: `%s`\n\nAvailable commands:\n- `/workspace` - Show active workspaces\n- `/workspace tasks` - List pending tasks\n- `/workspace task <id>` - Show task details\n- `/workspace all` - Show all tasks", subcommand),
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
