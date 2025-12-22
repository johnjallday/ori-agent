package chathttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/version"
)

// CommandHandler handles special chat commands
type CommandHandler struct {
	store          store.Store
	workspaceStore agentstudio.Store
	enumExtractor  *pluginhttp.EnumExtractor
	shutdownFunc   func()
}

// NewCommandHandler creates a new command handler
func NewCommandHandler(store store.Store) *CommandHandler {
	return &CommandHandler{
		store:         store,
		enumExtractor: pluginhttp.NewEnumExtractor(),
	}
}

// SetWorkspaceStore sets the workspace store for workspace commands
func (ch *CommandHandler) SetWorkspaceStore(ws agentstudio.Store) {
	ch.workspaceStore = ws
}

// SetShutdownFunc sets the shutdown function to be called on exit
func (ch *CommandHandler) SetShutdownFunc(fn func()) {
	ch.shutdownFunc = fn
}

// HandleAgentStatus handles the /agent command to show agent status dashboard
func (ch *CommandHandler) HandleAgentStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get current agent information
	ag, current, ok := store.GetCurrentAgent(ch.store)
	if !ok {
		if err := orihttp.RespondInternalError(w, "current agent not found"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	apiKeyStatus := "Configured"

	// Build status dashboard
	statusResponse := fmt.Sprintf(`## 🤖 Agent Status Dashboard

**Current Agent:** %s

**Model Configuration:**
- Model: %s
- Temperature: %.1f

**API Configuration:**
- API Key: %s

**Plugin Status:**
- Total Plugins: %d`,
		current,
		ag.Settings.Model,
		ag.Settings.Temperature,
		apiKeyStatus,
		len(ag.Plugins))

	// Add plugin details
	if len(ag.Plugins) > 0 {
		statusResponse += "\n- Active Plugins:\n"
		for name, plugin := range ag.Plugins {
			statusResponse += fmt.Sprintf("  - %s %s (v%s)\n", getPluginEmoji(name), name, plugin.Version)
		}
	} else {
		statusResponse += "\n- No plugins loaded"
	}

	// Add system information
	statusResponse += "\n\n**System Status:**\n- Server: Running ✅\n- Registry: Loaded ✅"

	// Return as a response that mimics a chat message
	response := map[string]any{
		"response": statusResponse,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {

		logger.Error("Failed to encode response", logger.Fields{"response": err})

	}
}

// HandleToolsList handles the /tools command to list available functions
func (ch *CommandHandler) HandleToolsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get current agent information
	ag, _, ok := store.GetCurrentAgent(ch.store)
	if !ok {
		if err := orihttp.RespondInternalError(w, "current agent not found"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var toolsResponse strings.Builder
	toolsResponse.WriteString("## 🔧 Available Tools\n\n")

	if len(ag.Plugins) == 0 {
		toolsResponse.WriteString("No tools are currently loaded.")
	} else {
		for name, plugin := range ag.Plugins {
			// Get fresh definition to show latest dynamic enums (e.g., script lists)
			def := plugin.Definition
			if plugin.Tool != nil {
				def = plugin.Tool.Definition()
			}

			emoji := getPluginEmoji(name)
			toolsResponse.WriteString(fmt.Sprintf("### %s %s\n\n", emoji, def.Name))

			description := def.Description
			if description != "" {
				toolsResponse.WriteString(fmt.Sprintf("**Description:** %s\n\n", description))
			}

			// Extract all enum values for this plugin and show them prominently with parameters
			allEnums, err := ch.enumExtractor.GetAllEnumsFromParameter(def)
			if err == nil && len(allEnums) > 0 {
				toolsResponse.WriteString("**🎯 Available Options:**\n")

				// Get parameter info for inline display
				var parameterInfo map[string]map[string]any
				var required []string
				if def.Parameters != nil {
					if props, ok := def.Parameters["properties"].(map[string]any); ok {
						parameterInfo = make(map[string]map[string]any)
						for paramName, paramData := range props {
							if paramMap, ok := paramData.(map[string]any); ok {
								parameterInfo[paramName] = paramMap
							}
						}

						// Get required fields
						if reqField, exists := def.Parameters["required"]; exists {
							if reqSlice, ok := reqField.([]string); ok {
								required = reqSlice
							}
						}
					}
				}

				for enumProperty, enumValues := range allEnums {
					toolsResponse.WriteString(fmt.Sprintf("- **%s**:\n", enumProperty))
					for _, enumValue := range enumValues {
						// Get parameters for this operation based on operation type
						var operationParams []string

						// Define operation-specific parameter mappings
						operationParamMap := map[string][]string{
							"create_project": {"name", "bpm"},
							"open_project":   {"path"},
							"filter_project": {"name"},
							"get_settings":   {},
							"scan":           {},
							"list_projects":  {},
						}

						// Get relevant parameters for this specific operation
						if relevantParams, exists := operationParamMap[enumValue]; exists {
							for _, paramName := range relevantParams {
								if _, paramExists := parameterInfo[paramName]; paramExists {
									isRequired := false
									for _, req := range required {
										if req == paramName {
											isRequired = true
											break
										}
									}

									displayName := paramName
									if isRequired {
										displayName += "*"
									}
									operationParams = append(operationParams, displayName)
								}
							}
						} else {
							// Fallback: show all non-operation parameters for unknown operations
							for paramName := range parameterInfo {
								if paramName == enumProperty {
									continue // Skip the operation parameter itself
								}

								isRequired := false
								for _, req := range required {
									if req == paramName {
										isRequired = true
										break
									}
								}

								displayName := paramName
								if isRequired {
									displayName += "*"
								}
								operationParams = append(operationParams, displayName)
							}
						}

						if len(operationParams) > 0 {
							toolsResponse.WriteString(fmt.Sprintf("  - `%s` (%s)\n", enumValue, strings.Join(operationParams, ", ")))
						} else {
							toolsResponse.WriteString(fmt.Sprintf("  - `%s`\n", enumValue))
						}
					}
				}
				toolsResponse.WriteString("\n")
			}

			toolsResponse.WriteString("\n")
		}
	}

	// Return as a response that mimics a chat message
	response := map[string]any{
		"response": toolsResponse.String(),
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {

		logger.Error("Failed to encode response", logger.Fields{"response": err})

	}
}

// HandleSwitch handles the /switch command to switch between agents
func (ch *CommandHandler) HandleSwitch(w http.ResponseWriter, r *http.Request, agentName string) {
	w.Header().Set("Content-Type", "application/json")

	// If no agent name provided, list available agents
	if agentName == "" {
		names, current := ch.store.ListAgents()
		agentList := fmt.Sprintf("**Available agents:** %s\n\n**Current agent:** %s\n\nUsage: `/switch <agent-name>`",
			strings.Join(names, ", "), current)

		response := map[string]any{
			"response": agentList,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Get list of available agents to validate the requested agent exists
	names, current := ch.store.ListAgents()

	// Check if the requested agent exists
	found := false
	for _, name := range names {
		if name == agentName {
			found = true
			break
		}
	}

	if !found {
		errorMsg := fmt.Sprintf("❌ **Agent '%s' not found.**\n\nAvailable agents: %s",
			agentName, strings.Join(names, ", "))
		response := map[string]any{
			"response": errorMsg,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Check if we're already on the requested agent
	if current == agentName {
		response := map[string]any{
			"response": fmt.Sprintf("✅ **Already using agent '%s'**", agentName),
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Switch to the requested agent
	if err := ch.store.SwitchAgent(agentName); err != nil {
		errorMsg := fmt.Sprintf("❌ **Failed to switch to agent '%s':** %v", agentName, err)
		response := map[string]any{
			"response": errorMsg,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Success message
	successMsg := fmt.Sprintf("✅ **Switched to agent '%s'**\n\nYou are now using the '%s' agent for all interactions.",
		agentName, agentName)
	response := map[string]any{
		"response": successMsg,
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// HandleAgentsList handles the /agents command to list all available agents
func (ch *CommandHandler) HandleAgentsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get all agents and current agent
	names, current := ch.store.ListAgents()

	if len(names) == 0 {
		response := map[string]any{
			"response": "No agents found.",
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Build agents list response
	var agentsResponse strings.Builder
	agentsResponse.WriteString("## 🤖 Available Agents\n\n")

	for _, name := range names {
		if name == current {
			agentsResponse.WriteString(fmt.Sprintf("- **%s** ✓ (current)\n", name))
		} else {
			agentsResponse.WriteString(fmt.Sprintf("- %s\n", name))
		}
	}

	agentsResponse.WriteString(fmt.Sprintf("\n**Total agents:** %d\n", len(names)))
	agentsResponse.WriteString("\n💡 **Tip:** Use `/switch <agent-name>` to switch to a different agent")

	response := map[string]any{
		"response": agentsResponse.String(),
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// HandleHelp handles the /help command to show available commands
func (ch *CommandHandler) HandleHelp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	helpResponse := `## 🤖 Available Commands

**System Commands:**
- **/help** - Show this help message
- **/version** - Show application version and build info
- **/agent** - Display agent status dashboard
- **/agents** - List all available agents
- **/switch <agent-name>** - Switch to a different agent
- **/tools** - List all available plugin tools and operations
- **/tool <name> <args>** - Execute a tool directly without LLM decision-making

**Workspace Commands:**
- **/workspace** - Show active workspaces
- **/workspace tasks** - List your pending tasks
- **/workspace task <task-id>** - Show details for a specific task
- **/workspace all** - Show all tasks (any status)

**Agent Management:**
- Use **/agent** to see current agent status and available agents
- Use **/agents** to see a list of all configured agents
- Use **/switch** to change between configured agents
- Each agent can have different plugins and configurations

**Plugin Tools:**
- Use **/tools** to see all available plugin operations
- Each tool shows available options and parameters
- Tools are specific to your current agent configuration
- Use **/tool** to execute tools directly (faster, no LLM overhead)

**Direct Tool Execution:**
The **/tool** command allows you to call tools directly without LLM decision-making:
- Format: /tool <tool_name> {"key": "value"}
- Example: /tool math {"operation": "add", "a": 5, "b": 3}
- Benefits: Faster execution, no API costs, deterministic results

**Tips:**
- Commands must start with **/** (forward slash)
- Agent names are case-sensitive when switching
- Use the web interface to configure plugins and agents
- Workspaces allow multiple agents to collaborate on complex tasks
- Direct tool calls bypass the LLM for instant execution

Type any command above to get started!`

	response := map[string]any{
		"response": helpResponse,
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// HandleWorkspace handles the /workspace command and subcommands
func (ch *CommandHandler) HandleWorkspace(w http.ResponseWriter, r *http.Request, args string) {
	w.Header().Set("Content-Type", "application/json")

	// Check if workspace store is available
	if ch.workspaceStore == nil {
		response := map[string]any{
			"response": "❌ Workspace functionality is not available.",
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Get current agent
	_, current := ch.store.ListAgents()
	if current == "" {
		response := map[string]any{
			"response": "❌ No active agent found.",
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	// Create agent context
	agentCtx := agentstudio.NewAgentContext(current, ch.workspaceStore)

	// Parse subcommand
	args = strings.TrimSpace(args)
	parts := strings.Fields(args)

	// If no args, show workspace summary
	if len(parts) == 0 {
		summary, err := agentCtx.GetWorkspaceSummary()
		if err != nil {
			response := map[string]any{
				"response": fmt.Sprintf("❌ Failed to get workspace summary: %v", err),
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				logger.Error("Failed to encode response", logger.Fields{"response": err})
			}
			return
		}

		response := map[string]any{
			"response": summary,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
		return
	}

	subcommand := parts[0]

	switch subcommand {
	case "tasks":
		// Show pending tasks
		tasksSummary, err := agentCtx.GetTasksSummary()
		if err != nil {
			response := map[string]any{
				"response": fmt.Sprintf("❌ Failed to get tasks: %v", err),
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				logger.Error("Failed to encode response", logger.Fields{"response": err})
			}
			return
		}

		response := map[string]any{
			"response": tasksSummary,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}

	case "task":
		// Show specific task details
		if len(parts) < 2 {
			response := map[string]any{
				"response": "❌ Please provide a task ID. Usage: `/workspace task <task-id>`",
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				logger.Error("Failed to encode response", logger.Fields{"response": err})
			}
			return
		}

		taskID := parts[1]
		details, err := agentCtx.GetTaskDetails(taskID)
		if err != nil {
			response := map[string]any{
				"response": fmt.Sprintf("❌ Failed to get task details: %v", err),
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				logger.Error("Failed to encode response", logger.Fields{"response": err})
			}
			return
		}

		response := map[string]any{
			"response": details,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}

	case "all":
		// Show all tasks (any status)
		allTasks, err := agentCtx.GetAllTasks()
		if err != nil {
			response := map[string]any{
				"response": fmt.Sprintf("❌ Failed to get tasks: %v", err),
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				logger.Error("Failed to encode response", logger.Fields{"response": err})
			}
			return
		}

		if len(allTasks) == 0 {
			response := map[string]any{
				"response": "You have no tasks in any agentstudio.",
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				logger.Error("Failed to encode response", logger.Fields{"response": err})
			}
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## All Your Tasks (%d)\n\n", len(allTasks)))

		// Group by status
		byStatus := make(map[agentstudio.TaskStatus][]agentstudio.Task)
		for _, task := range allTasks {
			byStatus[task.Status] = append(byStatus[task.Status], task)
		}

		statuses := []agentstudio.TaskStatus{
			agentstudio.TaskStatusPending,
			agentstudio.TaskStatusAssigned,
			agentstudio.TaskStatusInProgress,
			agentstudio.TaskStatusCompleted,
			agentstudio.TaskStatusFailed,
			agentstudio.TaskStatusCancelled,
			agentstudio.TaskStatusTimeout,
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
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}

	default:
		// Unknown subcommand
		response := map[string]any{
			"response": fmt.Sprintf("❌ Unknown workspace command: `%s`\n\nAvailable commands:\n- `/workspace` - Show active workspaces\n- `/workspace tasks` - List pending tasks\n- `/workspace task <id>` - Show task details\n- `/workspace all` - Show all tasks", subcommand),
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}
	}
}

// HandleExit handles the /exit command to shut down the server
func (ch *CommandHandler) HandleExit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Send acknowledgment response first
	response := map[string]any{
		"response": "👋 **Shutting down ori-agent server...**\n\nGoodbye!",
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}

	// Flush the response to ensure client receives it
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Trigger shutdown in a goroutine to allow response to be sent
	go func() {
		// Small delay to ensure response is sent
		time.Sleep(100 * time.Millisecond)

		if ch.shutdownFunc != nil {
			logger.Info("Executing shutdown via /exit command", logger.Fields{})
			ch.shutdownFunc()
		} else {
			logger.Warn("Shutdown function not set, exiting immediately", logger.Fields{})
			os.Exit(0)
		}
	}()
}

// HandleVersion handles the /version command to show app version and build info
func (ch *CommandHandler) HandleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	info := version.GetBuildInfo()

	var versionResponse strings.Builder
	versionResponse.WriteString("## 📦 Ori Agent Version\n\n")
	versionResponse.WriteString(fmt.Sprintf("**Version:** %s\n", info["version"]))
	versionResponse.WriteString(fmt.Sprintf("**API Version:** %s\n", info["api_version"]))

	if info["git_commit"] != "unknown" {
		versionResponse.WriteString(fmt.Sprintf("**Git Commit:** %s\n", info["git_commit"]))
	}
	if info["build_date"] != "unknown" {
		versionResponse.WriteString(fmt.Sprintf("**Build Date:** %s\n", info["build_date"]))
	}

	response := map[string]any{
		"response": versionResponse.String(),
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}
