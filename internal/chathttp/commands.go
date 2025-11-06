package chathttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
)

// CommandHandler handles special chat commands
type CommandHandler struct {
	store          store.Store
	workspaceStore agentstudio.Store
	enumExtractor  *pluginhttp.EnumExtractor
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

// HandleAgentStatus handles the /agent command to show agent status dashboard
func (ch *CommandHandler) HandleAgentStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get current agent information
	names, current := ch.store.ListAgents()
	if current == "" && len(names) > 0 {
		current = names[0] // fallback to first available agent
	}

	ag, ok := ch.store.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	// Get API key status - this would need to be passed in or accessed differently
	// For now, we'll simplify this
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

	json.NewEncoder(w).Encode(response)
}

// HandleToolsList handles the /tools command to list available functions
func (ch *CommandHandler) HandleToolsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get current agent information
	names, current := ch.store.ListAgents()
	if current == "" && len(names) > 0 {
		current = names[0] // fallback to first available agent
	}

	ag, ok := ch.store.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	// Build tools list response
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

			description := def.Description.String()
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
							"create_project":  {"name", "bpm"},
							"open_project":    {"path"},
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

	json.NewEncoder(w).Encode(response)
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
		json.NewEncoder(w).Encode(response)
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
		json.NewEncoder(w).Encode(response)
		return
	}

	// Check if we're already on the requested agent
	if current == agentName {
		response := map[string]any{
			"response": fmt.Sprintf("✅ **Already using agent '%s'**", agentName),
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	// Switch to the requested agent
	if err := ch.store.SwitchAgent(agentName); err != nil {
		errorMsg := fmt.Sprintf("❌ **Failed to switch to agent '%s':** %v", agentName, err)
		response := map[string]any{
			"response": errorMsg,
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	// Success message
	successMsg := fmt.Sprintf("✅ **Switched to agent '%s'**\n\nYou are now using the '%s' agent for all interactions.",
		agentName, agentName)
	response := map[string]any{
		"response": successMsg,
	}
	json.NewEncoder(w).Encode(response)
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
		json.NewEncoder(w).Encode(response)
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
	json.NewEncoder(w).Encode(response)
}

// HandleHelp handles the /help command to show available commands
func (ch *CommandHandler) HandleHelp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	helpResponse := `## 🤖 Available Commands

**System Commands:**
- **/help** - Show this help message
- **/agent** - Display agent status dashboard
- **/agents** - List all available agents
- **/switch <agent-name>** - Switch to a different agent
- **/tools** - List all available plugin tools and operations

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

**Tips:**
- Commands must start with **/** (forward slash)
- Agent names are case-sensitive when switching
- Use the web interface to configure plugins and agents
- Workspaces allow multiple agents to collaborate on complex tasks

Type any command above to get started!`

	response := map[string]any{
		"response": helpResponse,
	}
	json.NewEncoder(w).Encode(response)
}
// HandleWorkspace handles the /workspace command and subcommands
func (ch *CommandHandler) HandleWorkspace(w http.ResponseWriter, r *http.Request, args string) {
	w.Header().Set("Content-Type", "application/json")

	// Check if workspace store is available
	if ch.workspaceStore == nil {
		response := map[string]any{
			"response": "❌ Workspace functionality is not available.",
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	// Get current agent
	_, current := ch.store.ListAgents()
	if current == "" {
		response := map[string]any{
			"response": "❌ No active agent found.",
		}
		json.NewEncoder(w).Encode(response)
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
			json.NewEncoder(w).Encode(response)
			return
		}

		response := map[string]any{
			"response": summary,
		}
		json.NewEncoder(w).Encode(response)
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
			json.NewEncoder(w).Encode(response)
			return
		}

		response := map[string]any{
			"response": tasksSummary,
		}
		json.NewEncoder(w).Encode(response)

	case "task":
		// Show specific task details
		if len(parts) < 2 {
			response := map[string]any{
				"response": "❌ Please provide a task ID. Usage: `/workspace task <task-id>`",
			}
			json.NewEncoder(w).Encode(response)
			return
		}

		taskID := parts[1]
		details, err := agentCtx.GetTaskDetails(taskID)
		if err != nil {
			response := map[string]any{
				"response": fmt.Sprintf("❌ Failed to get task details: %v", err),
			}
			json.NewEncoder(w).Encode(response)
			return
		}

		response := map[string]any{
			"response": details,
		}
		json.NewEncoder(w).Encode(response)

	case "all":
		// Show all tasks (any status)
		allTasks, err := agentCtx.GetAllTasks()
		if err != nil {
			response := map[string]any{
				"response": fmt.Sprintf("❌ Failed to get tasks: %v", err),
			}
			json.NewEncoder(w).Encode(response)
			return
		}

		if len(allTasks) == 0 {
			response := map[string]any{
				"response": "You have no tasks in any agentstudio.",
			}
			json.NewEncoder(w).Encode(response)
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

			sb.WriteString(fmt.Sprintf("### %s (%d)\n\n", strings.Title(string(status)), len(tasks)))
			for i, task := range tasks {
				sb.WriteString(fmt.Sprintf("%d. **%s** (`%s`)\n", i+1, task.Description, task.ID))
				sb.WriteString(fmt.Sprintf("   - From: %s | Priority: %d/5\n", task.From, task.Priority))
			}
			sb.WriteString("\n")
		}

		response := map[string]any{
			"response": sb.String(),
		}
		json.NewEncoder(w).Encode(response)

	default:
		// Unknown subcommand
		response := map[string]any{
			"response": fmt.Sprintf("❌ Unknown workspace command: `%s`\n\nAvailable commands:\n- `/workspace` - Show active workspaces\n- `/workspace tasks` - List pending tasks\n- `/workspace task <id>` - Show task details\n- `/workspace all` - Show all tasks", subcommand),
		}
		json.NewEncoder(w).Encode(response)
	}
}
